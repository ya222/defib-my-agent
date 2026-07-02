// Package fake is the deterministic provider used by tests and local
// development. It replays a user-supplied script of attempt blocks, so the
// whole supervise/detect/schedule/recover loop can be exercised without
// calling a real provider (see docs/providers.md#the-fake-provider).
package fake

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/provider"
)

// RunMode is the hidden argv[1] marker that switches a defib (or test)
// binary into fake-provider child mode; dispatchers pass the remaining
// args to Main.
const RunMode = "__defib-fake-run"

// Fake implements provider.Provider by re-executing the current binary in
// RunMode with the script path and the attempt block to replay.
type Fake struct {
	mu sync.Mutex
	// nextBlock tracks, per session ref, which script block the next
	// BuildResume should replay: resume simply advances to the next block.
	nextBlock map[string]int
}

// New returns a fake provider instance.
func New() *Fake {
	return &Fake{nextBlock: make(map[string]int)}
}

// Name implements provider.Provider.
func (f *Fake) Name() string { return "fake" }

// Capabilities implements provider.Provider: headless with client-supplied
// session ids and resume; interactive support arrives with the PTY milestone.
func (f *Fake) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Resume:           true,
		ClientSuppliedID: true,
		Headless:         true,
	}
}

// BuildStart implements provider.Provider: a new session replays block 1.
func (f *Fake) BuildStart(task provider.TaskSpec) (provider.Command, error) {
	return f.command(task, task.SessionRef, 1)
}

// BuildResume implements provider.Provider: each resume advances to the
// next attempt block of the script.
func (f *Fake) BuildResume(task provider.TaskSpec, sessionRef string) (provider.Command, error) {
	f.mu.Lock()
	block, ok := f.nextBlock[sessionRef]
	if !ok {
		// Resuming a session this process never started (e.g. after a
		// daemon restart): the first attempt already ran, continue at 2.
		block = 2
	}
	f.mu.Unlock()
	return f.command(task, sessionRef, block)
}

func (f *Fake) command(task provider.TaskSpec, sessionRef string, block int) (provider.Command, error) {
	script, err := scriptPath(task)
	if err != nil {
		return provider.Command{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return provider.Command{}, fmt.Errorf("fake provider: resolve executable: %w", err)
	}
	f.mu.Lock()
	f.nextBlock[sessionRef] = block + 1
	f.mu.Unlock()
	return provider.Command{
		Argv: []string{exe, RunMode, "--script", script, "--block", strconv.Itoa(block)},
	}, nil
}

func scriptPath(task provider.TaskSpec) (string, error) {
	raw, ok := task.ProviderConfig["script"]
	if !ok {
		return "", fmt.Errorf("fake provider: providers.fake.script is not set")
	}
	script, ok := raw.(string)
	if !ok || script == "" {
		return "", fmt.Errorf("fake provider: providers.fake.script must be a non-empty string, got %v", raw)
	}
	return script, nil
}

// ExtractSessionRef implements provider.Provider. Session ids are always
// client-supplied, so there is never anything to parse.
func (f *Fake) ExtractSessionRef(provider.AttemptOutput) (string, bool) {
	return "", false
}

// DetectionRules implements provider.Provider with the deterministic rule
// set from docs/detection.md#fake-provider-deterministic-for-tests.
func (f *Fake) DetectionRules() []detect.Rule {
	return []detect.Rule{
		{
			Name:     "fake.reset",
			Category: detect.CategoryRateLimit,
			Priority: 80,
			Match:    detect.Match{StdoutRegex: `FAKE_RESET_AT=(\S+)`},
			ResetExtractor: &detect.Extractor{
				Source: "stdout",
				Regex:  `FAKE_RESET_AT=(\S+)`,
				Kind:   "rfc3339",
			},
		},
		{
			Name:     "fake.quota",
			Category: detect.CategoryQuotaExhausted,
			Priority: 85,
			Match:    detect.Match{StdoutRegex: `FAKE_QUOTA_EXHAUSTED`},
		},
		{
			Name:     "fake.fatal",
			Category: detect.CategoryFatalError,
			Priority: 95,
			Match:    detect.Match{StdoutRegex: `FAKE_FATAL`},
		},
		{
			Name:     "fake.success",
			Category: detect.CategorySuccess,
			Priority: 1,
			Match:    detect.Match{ExitCodeIn: []int{0}},
		},
	}
}

// CheckAvailability implements provider.Provider; the fake has no separate
// availability signal, tests drive it via scripted reset hints instead.
func (f *Fake) CheckAvailability(context.Context, provider.TaskSpec) (provider.Availability, error) {
	return provider.Availability{State: provider.Unsupported}, nil
}
