# GitHub Copilot CLI fixtures

Reference captures against the GitHub Copilot CLI (`copilot --version`).

## Provenance — read before trusting a fixture

| Fixture | Version | Provenance |
| --- | --- | --- |
| `version.txt` | 1.0.65 | **Captured** `copilot --version` output — the pinned version the adapter's flags were verified against (M12-T1). |
| `help.txt` | 1.0.65 | **Captured** `copilot --help` output — the source of truth for the flags the adapter builds (`-p/--prompt`, `--session-id`, `--output-format json`, `--model`, `--allow-all-tools`). |
| `quota-exceeded.stdout.log` / `.stderr.log` | 1.0.70 | **Captured** from a real headless run (`copilot -p "…" --output-format json`) once the account's **monthly premium-request budget was exhausted**. The line-delimited json stream carries a `model.call_failure` and terminal `session.error` event with `"statusCode":402`, `"errorType":"quota"`, `"errorCode":"quota_exceeded"` ("You have exceeded your monthly quota"), a `quotaSnapshots` block whose `premium_interactions` shows `usedRequests == entitlementRequests` and a `"resetDate"` (RFC3339 monthly reset), and a terminal `result` event with `"exitCode":1`. stderr is empty. Drives `copilot.quota` → `QUOTA_EXHAUSTED` with the `resetDate` as Reset Time. Captured 2026-07-10. |
| `invalid-session.stdout.log` / `.stderr.log` | 1.0.70 | **Captured** from resuming a `--session-id` the CLI has no record of. stdout is empty; stderr is `No session or task matched '<id>'. …`, exit 1. Drives `copilot.session_not_found` → `FATAL_ERROR` (a bad/unknown session id can never succeed on retry). Captured 2026-07-10. |

The output fixtures were captured on `copilot 1.0.70`; the flag-reference captures
(`version.txt`, `help.txt`) remain pinned to `1.0.65` from M12-T1 and are unchanged.

## What is captured vs inferred

`copilot.quota` and `copilot.session_not_found` are **validated against the real captures**
above. `copilot.auth` / `copilot.rate_limit` / `copilot.transient` key on the **confirmed**
`"statusCode":<int>` schema (a real field of the captured `session.error`/`model.call_failure`
events) using **standard HTTP semantics** (401/403 auth, 429 throttle, 5xx transient); those
specific non-402 status codes are structural inferences, not yet observed from a real copilot
limit. Copilot exposes no distinct session/weekly cap surface — its only budget is the monthly
quota above — so no `SESSION_LIMIT` rule is shipped. Replace the inferred rows with real
captures when observed (issue #18); `internal/provider/copilot/rules_test.go` already pins the
intended mapping so a real capture can drop straight in.

Never call the real `copilot` binary from code, tests, or CI. These fixtures were captured
manually (the account was genuinely out of quota) and committed; to refresh the flag-reference
captures, rerun `copilot --version` / `copilot --help` and commit the output.
