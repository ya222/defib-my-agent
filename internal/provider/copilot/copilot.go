package copilot

import (
	"context"
	"fmt"

	"github.com/ya222/defib-my-agent/internal/provider"
)

// defaultResumePrompt is sent on resume when providers.copilot.resume_prompt
// is unset, so a bare `-p ""` never reaches the CLI.
const defaultResumePrompt = "Continue the previous task."

// Copilot implements provider.Provider for the GitHub Copilot CLI.
type Copilot struct{}

// New returns a Copilot provider instance.
func New() *Copilot {
	return &Copilot{}
}

// Name implements provider.Provider.
func (*Copilot) Name() string { return "copilot" }

// Capabilities implements provider.Provider: the Copilot CLI supports
// session resume, client-supplied session ids, headless and interactive
// runs, and structured (json) output.
func (*Copilot) Capabilities() provider.Capabilities {
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
func (c *Copilot) BuildStart(task provider.TaskSpec) (provider.Command, error) {
	binary := binaryName(task)
	argv := []string{binary, "-p", task.Prompt}
	if task.SessionRef != "" {
		argv = append(argv, "--session-id", task.SessionRef)
	}
	argv = append(argv, "--output-format", "json")
	argv = appendCommonFlags(argv, task)
	return provider.Command{Argv: argv}, nil
}

// BuildResume implements provider.Provider: continues an existing session by
// its session id, nudging with resume_prompt (or the default) when the task
// carries no fresh prompt.
func (c *Copilot) BuildResume(task provider.TaskSpec, sessionRef string) (provider.Command, error) {
	if sessionRef == "" {
		return provider.Command{}, fmt.Errorf("copilot provider: BuildResume requires a non-empty sessionRef")
	}
	binary := binaryName(task)
	argv := []string{binary, "-p", resumePrompt(task), "--session-id", sessionRef, "--output-format", "json"}
	argv = appendCommonFlags(argv, task)
	return provider.Command{Argv: argv}, nil
}

// skipPermissionsFlag is the Copilot CLI 1.0.65 skip-approvals flag. It is
// only ever added on explicit opt-in (providers.copilot.unattended /
// --unattended) and defib prints the security warning when it is; never
// default-on (docs/architecture.md#security-model).
const skipPermissionsFlag = "--allow-all-tools"

// appendCommonFlags appends the --model, unattended, extra_args, and
// passthrough arguments shared by BuildStart and BuildResume, in that order.
func appendCommonFlags(argv []string, task provider.TaskSpec) []string {
	if task.Model != "" {
		argv = append(argv, "--model", task.Model)
	}
	if unattended, ok := task.ProviderConfig["unattended"].(bool); ok && unattended {
		argv = append(argv, skipPermissionsFlag)
	}
	argv = append(argv, extraArgs(task)...)
	if len(task.Passthrough) > 0 {
		argv = append(argv, "--")
		argv = append(argv, task.Passthrough...)
	}
	return argv
}

// binaryName reads providers.copilot.binary from ProviderConfig, defaulting
// to "copilot" resolved from PATH.
func binaryName(task provider.TaskSpec) string {
	if raw, ok := task.ProviderConfig["binary"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return "copilot"
}

// resumePrompt reads providers.copilot.resume_prompt from ProviderConfig,
// defaulting to defaultResumePrompt when absent or empty.
func resumePrompt(task provider.TaskSpec) string {
	if raw, ok := task.ProviderConfig["resume_prompt"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return defaultResumePrompt
}

// extraArgs reads providers.copilot.extra_args from ProviderConfig. Config
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

// ExtractSessionRef implements provider.Provider. defib always supplies
// --session-id in BuildStart, so the session id is already known; the
// Copilot CLI's json output format for the session id has not been
// verified, so there is no fallback parsing path here.
func (*Copilot) ExtractSessionRef(provider.AttemptOutput) (string, bool) {
	return "", false
}

// CheckAvailability implements provider.Provider. The Copilot CLI offers no
// verified cheap availability probe yet.
func (*Copilot) CheckAvailability(context.Context, provider.TaskSpec) (provider.Availability, error) {
	return provider.Availability{State: provider.Unsupported}, nil
}
