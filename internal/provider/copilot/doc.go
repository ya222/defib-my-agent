// Package copilot adapts the GitHub Copilot CLI to the provider interface.
// Every flag below was verified against the pinned version:
//
//	GitHub Copilot CLI 1.0.65 (`copilot --version`, 2026-07-04)
//
// Verified flags: -p/--prompt <text>, --session-id <id> (sets the id for a new
// session and resumes an existing one by id), --output-format {text,json},
// --model <model>, --allow-all-tools (skip-approvals; opt-in only).
//
// Session strategy: pre-generate. defib supplies --session-id up front, so
// ExtractSessionRef never needs to parse output.
//
// Detection: only a low-priority SUCCESS rule (exit code 0) is shipped here.
// Copilot's rate-limit/quota output format is not yet captured, so failure
// classification rules are deferred (tracked in issue #18); until
// then non-zero exits classify as UNKNOWN (retry+backoff per config).
//
// Manual smoke test (spends real AI credits; never run in CI):
//
//	defib start --provider copilot -p 'Reply with exactly: ok' --unattended
//	defib logs <task>
package copilot
