// Package claude adapts the Claude Code CLI (github.com/anthropics/claude-code)
// to the provider interface. Every flag below was verified against the
// pinned version:
//
//	claude 2.1.199 (`claude --version`, 2026-07-03)
//
// Verified flags: -p/--print, --session-id <uuid>, -r/--resume [id],
// --output-format {text,json,stream-json} (requires --print; stream-json
// requires --verbose), --verbose, --model <model>,
// --permission-mode <mode>, --dangerously-skip-permissions.
//
// Manual smoke test (spends real credits; never run in CI):
//
//	defib start --provider claude -p 'Reply with exactly: ok'
//	defib logs <task>   # stream-json events, session_id in the init event
//
// Real captured output lives in testdata/claude/ (see its README for
// which fixtures are captured vs documented-format).
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/provider"
)

// defaultResumePrompt is sent on resume when providers.claude.resume_prompt
// is unset, so a bare `-p ""` never reaches the CLI.
const defaultResumePrompt = "Continue the previous task."

// Claude implements provider.Provider for the Claude Code CLI.
type Claude struct{}

// New returns a Claude provider instance.
func New() *Claude {
	return &Claude{}
}

// Name implements provider.Provider.
func (*Claude) Name() string { return "claude" }

// Capabilities implements provider.Provider: Claude Code supports native
// resume, client-supplied session ids, headless and interactive runs, and
// structured (stream-json) output (docs/providers.md#claude-code-adapter).
func (*Claude) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Resume:           true,
		ClientSuppliedID: true,
		Headless:         true,
		Interactive:      true,
		StructuredOutput: true,
	}
}

// BuildStart implements provider.Provider: begins a new session, pre-supplying
// the defib-generated session id so ExtractSessionRef never needs to parse it.
func (c *Claude) BuildStart(task provider.TaskSpec) (provider.Command, error) {
	binary := binaryName(task)
	argv := []string{binary, "-p", task.Prompt}
	if task.SessionRef != "" {
		argv = append(argv, "--session-id", task.SessionRef)
	}
	argv = append(argv, "--output-format", "stream-json", "--verbose")
	argv = appendCommonFlags(argv, task)
	return provider.Command{Argv: argv}, nil
}

// BuildResume implements provider.Provider: continues an existing session by
// its session id, nudging with resume_prompt (or the default) when the task
// carries no fresh prompt.
func (c *Claude) BuildResume(task provider.TaskSpec, sessionRef string) (provider.Command, error) {
	if sessionRef == "" {
		return provider.Command{}, fmt.Errorf("claude provider: BuildResume requires a non-empty sessionRef")
	}
	binary := binaryName(task)
	argv := []string{binary, "-p", resumePrompt(task), "--resume", sessionRef, "--output-format", "stream-json", "--verbose"}
	argv = appendCommonFlags(argv, task)
	return provider.Command{Argv: argv}, nil
}

// appendCommonFlags appends the --model, extra_args, and passthrough
// arguments shared by BuildStart and BuildResume, in that order.
func appendCommonFlags(argv []string, task provider.TaskSpec) []string {
	if task.Model != "" {
		argv = append(argv, "--model", task.Model)
	}
	argv = append(argv, extraArgs(task)...)
	if len(task.Passthrough) > 0 {
		argv = append(argv, "--")
		argv = append(argv, task.Passthrough...)
	}
	return argv
}

// binaryName reads providers.claude.binary from ProviderConfig, defaulting
// to "claude" resolved from PATH.
func binaryName(task provider.TaskSpec) string {
	if raw, ok := task.ProviderConfig["binary"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return "claude"
}

// resumePrompt reads providers.claude.resume_prompt from ProviderConfig,
// defaulting to defaultResumePrompt when absent or empty.
func resumePrompt(task provider.TaskSpec) string {
	if raw, ok := task.ProviderConfig["resume_prompt"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return defaultResumePrompt
}

// extraArgs reads providers.claude.extra_args from ProviderConfig. Config
// loaders may hand back either []string (native) or []any (e.g. decoded
// from TOML/JSON), so both are handled; non-string elements are skipped.
func extraArgs(task provider.TaskSpec) []string {
	raw, ok := task.ProviderConfig["extra_args"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// initEvent is the minimal shape of a stream-json init event needed to
// recover the session id when the provider assigned it (fallback path;
// BuildStart normally pre-supplies --session-id so this is rarely reached).
type initEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
}

// ExtractSessionRef implements provider.Provider. defib always supplies
// --session-id in BuildStart, so the session id is normally already known;
// this scans stdout for the stream-json init event as a defensive fallback.
func (*Claude) ExtractSessionRef(out provider.AttemptOutput) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(out.Stdout))
	// stream-json lines (with tool output embedded) can be long; grow the
	// buffer past bufio's 64KiB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev initEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // non-JSON or unrelated line; skip
		}
		if ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" {
			return ev.SessionID, true
		}
	}
	return "", false
}

// DetectionRules implements provider.Provider. Filled in M10-T2 from
// captured fixtures.
func (*Claude) DetectionRules() []detect.Rule {
	return nil
}

// CheckAvailability implements provider.Provider. Claude Code exposes reset
// timing in the limit message itself (docs/providers.md#claude-code-adapter),
// so defib relies on the extracted Reset Time instead of a separate probe.
func (*Claude) CheckAvailability(context.Context, provider.TaskSpec) (provider.Availability, error) {
	return provider.Availability{State: provider.Unsupported}, nil
}
