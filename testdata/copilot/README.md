# GitHub Copilot CLI fixtures

Reference captures against `GitHub Copilot CLI 1.0.65` (`copilot --version`) on 2026-07-04.

## Provenance — read before trusting a fixture

| Fixture | Provenance |
| --- | --- |
| `version.txt` | **Captured** `copilot --version` output — the pinned version the adapter's flags were verified against. |
| `help.txt` | **Captured** `copilot --help` output — the source of truth for the flags the adapter builds (`-p/--prompt`, `--session-id`, `--output-format json`, `--model`, `--allow-all-tools`). |

## No output fixtures yet

There are deliberately **no** stdout/stderr *run-output* fixtures here. Copilot's
rate-limit / quota / credit-exhaustion output cannot be triggered on demand (it requires
actually exhausting real AI credits), it is not documented in `copilot --help` or the
`copilot help billing` topic, and calling the real provider from code/tests/CI is forbidden
(AGENTS.md). Without a verifiable sample, writing failure-classification rules would be
guessing, which AGENTS.md prohibits.

Consequently the adapter (`internal/provider/copilot`) ships only a SUCCESS rule
(`exit_code == 0`); non-zero exits classify as `UNKNOWN` (retry + backoff per config) until
real fixtures exist. Adding the Copilot rate-limit/quota rule set to `docs/detection.md` and
`internal/provider/copilot/rules.go` is deferred to **M12-T2**, tracked in issue #18.
When a real limit is observed, capture the run's stdout/stderr here and add the rules.

Never call the real `copilot` binary from code, tests, or CI. To refresh these reference
captures manually, rerun `copilot --version` / `copilot --help` and commit the output.
