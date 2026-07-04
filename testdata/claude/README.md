# Claude Code output fixtures

Captured against `claude 2.1.199` (`--output-format stream-json --verbose`) on 2026-07-03.

## Provenance — read before trusting a fixture

| Fixture | Provenance |
| --- | --- |
| `success.stream-json.stdout.log` | **Captured** from a real headless run (`claude -p "Reply with exactly: ok" --output-format stream-json --verbose --model claude-haiku-4-5-20251001`). |
| `auth-error.stdout.log` | **Captured** from a real run with an invalid `ANTHROPIC_API_KEY` (exit 1, `"error":"authentication_failed"`, `api_error_status: 401`). |
| `session-not-found.stdout.log` / `.stderr.log` | **Captured** from a real `--resume <id>` against a session id the CLI has no record of (exit 1, `"subtype":"error_during_execution"`, `"No conversation found with session ID: <id>"`, no `api_error_status`). Drives `claude.session_not_found` → `FATAL_ERROR`. |
| `*.documented.stdout.log` | **Not captured.** Rate limits, usage limits, low credit, and 529 overloads cannot be triggered on demand without spending/exhausting real quota. These fixtures use the *captured* result-event shape (from `auth-error.stdout.log`) filled with Anthropic's documented error formats (`api_error_status`, `overloaded_error`, the documented low-credit message) and the widely observed `Claude AI usage limit reached|<unix-epoch>` text line. Replace each with a real capture when one occurs — tracked in the repo issue "Replace documented-format Claude fixtures with real captures". |

Detection rules in `internal/provider/claude` prefer structural fields that appear in the
captured fixtures (`api_error_status`, `"error":"authentication_failed"`) over prose, so a
wording change in a documented-format fixture should not silently break classification.

Never call the real `claude` binary from code, tests, or CI (AGENTS.md). To refresh the
captured fixtures manually, rerun the commands above and commit the new output.

## Capturing a real limit fixture (issue #11)

The four `*.documented.stdout.log` fixtures still await real captures. You don't need to run
`claude` by hand — defib already writes each attempt's raw stdout/stderr under
`<state>/tasks/<task-id>/attempts/<n>/`. So the next time a real limit occurs, copy the
offending attempt's files straight in and record its exit code:

```sh
defib status <task>          # find the attempt <n> that hit the limit + its exit code
A=~/.local/state/defib/tasks/<task-id>/attempts/<n>
cp "$A/stdout.log" testdata/claude/usage-limit.stdout.log   # or rate-limit-429 / credit-low / overloaded-529
cp "$A/stderr.log" testdata/claude/usage-limit.stderr.log
```

Then delete the matching `*.documented.*` fixture, point the rules test at the new file, adjust
the rule regex if the real output differs from the documented shape, and update the provenance
table above and `docs/detection.md`.
