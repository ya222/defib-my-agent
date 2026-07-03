# Claude Code output fixtures

Captured against `claude 2.1.199` (`--output-format stream-json --verbose`) on 2026-07-03.

## Provenance — read before trusting a fixture

| Fixture | Provenance |
| --- | --- |
| `success.stream-json.stdout.log` | **Captured** from a real headless run (`claude -p "Reply with exactly: ok" --output-format stream-json --verbose --model claude-haiku-4-5-20251001`). |
| `auth-error.stdout.log` | **Captured** from a real run with an invalid `ANTHROPIC_API_KEY` (exit 1, `"error":"authentication_failed"`, `api_error_status: 401`). |
| `*.documented.stdout.log` | **Not captured.** Rate limits, usage limits, low credit, and 529 overloads cannot be triggered on demand without spending/exhausting real quota. These fixtures use the *captured* result-event shape (from `auth-error.stdout.log`) filled with Anthropic's documented error formats (`api_error_status`, `overloaded_error`, the documented low-credit message) and the widely observed `Claude AI usage limit reached|<unix-epoch>` text line. Replace each with a real capture when one occurs — tracked in the repo issue "Replace documented-format Claude fixtures with real captures". |

Detection rules in `internal/provider/claude` prefer structural fields that appear in the
captured fixtures (`api_error_status`, `"error":"authentication_failed"`) over prose, so a
wording change in a documented-format fixture should not silently break classification.

Never call the real `claude` binary from code, tests, or CI (AGENTS.md). To refresh the
captured fixtures manually, rerun the commands above and commit the new output.
