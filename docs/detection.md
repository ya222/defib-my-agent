# Detection

Detection turns a finished Attempt (exit code + captured stdout/stderr) into an **Outcome
Category** and, when possible, a **Reset Time**. This document owns the Outcome semantics and
the rule format. It does not repeat the state machine (see [architecture.md](architecture.md))
or provider command details (see [providers.md](providers.md)).

## Outcome categories (canonical)

This enum is the single source of truth. The Supervisor maps each category to an action per
the transition table in [architecture.md](architecture.md#task-lifecycle-state-machine).

| Category | Meaning | Default supervisor action |
| --- | --- | --- |
| `SUCCESS` | The Attempt completed the work (clean exit, no limit hit). | → `SUCCEEDED`. |
| `RATE_LIMIT` | Temporary throttling; usually short; often carries a Reset Time. | Wait until Reset Time, else short Backoff; Resume. |
| `QUOTA_EXHAUSTED` | Credits/usage budget spent; may need a longer window or top-up. | Wait until Reset Time if known; else Backoff and/or Availability Probe; Resume. |
| `SESSION_LIMIT` | Session/usage cap or context limit reached for this session. | Wait until Reset Time if known; else Backoff; Resume (or new session per policy). |
| `TRANSIENT_ERROR` | Network blip, 5xx, "overloaded", provider crash. | Short Backoff; Resume. |
| `FATAL_ERROR` | Non-retryable: auth failure, invalid usage, provider refused. | → `FAILED`. |
| `UNKNOWN` | Nothing matched. | Per config `on_unknown` (default `retry` with Backoff; can be `fail`). |

## How classification works

1. Gather Attempt inputs: `exit_code`, tail of `stdout`, tail of `stderr` (bounded to
   `detect.scan_bytes`, default last 64 KiB of each stream), and, if the provider emits
   structured output, parsed fields.
2. Evaluate the merged rule set (provider built-ins from `Provider.DetectionRules()` +
   user rules from config) ordered by **descending `priority`**. The **first rule that
   matches wins**.
3. If a matching rule has a `reset_extractor`, run it to produce a Reset Time.
4. If no rule matches, the Outcome is `UNKNOWN` (unless a rule explicitly maps success — see
   below).

**Success determination:** a clean provider exit is `exit_code == 0` **and** no
higher-priority failure rule matched. Provide an explicit built-in `SUCCESS` rule with low
priority matching `exit_code==0` so the default is deterministic. Some providers exit `0` even
on a rate limit in headless mode — that is exactly why failure rules have higher priority than
the success rule.

## Rule format

Rules are data. Built-in rules are Go literals; user rules are TOML (see
[configuration.md](configuration.md)). Both deserialize to the same struct.

```go
type Rule struct {
    Name     string          // unique, appears in attempts.matched_rule
    Category string          // one of the Outcome categories above
    Priority int             // higher = evaluated first
    Match    Match           // all present conditions must hold (AND)
    ResetExtractor *Extractor // optional; sets Reset Time when the rule matches
}

type Match struct {
    ExitCodeIn   []int   // matches if exit code is in this set (empty = ignore exit code)
    StdoutRegex  string  // Go RE2; matches against stdout tail (empty = ignore)
    StderrRegex  string  // Go RE2; matches against stderr tail (empty = ignore)
    AnyRegex     string  // matches against stdout+stderr combined (convenience)
}

type Extractor struct {
    Source string // "stdout" | "stderr" | "any" | "header"
    Regex  string // MUST contain one capture group with the raw value
    Kind   string // how to interpret the captured value (see below)
    Format string // optional, for Kind="clock_time" or custom layouts
}
```

### Reset-time extractor kinds

| Kind | Captured value example | Interpretation |
| --- | --- | --- |
| `rfc3339` | `2026-07-02T15:04:05Z` | Absolute time, parsed directly. |
| `unix_seconds` | `1751468645` | Absolute time from a Unix timestamp. |
| `http_retry_after` | `3600` or an HTTP-date | Seconds-from-now, or an RFC1123 date. |
| `relative_duration` | `5m`, `2h30m`, `90s` | Added to "now". |
| `clock_time` | `3:00pm`, `15:00` | Next occurrence of that local wall-clock time; use `Format`. |

A Reset Time in the past is ignored (treated as "no reset time"), so the Scheduler falls back
to Backoff.

## Matching semantics and safety

- All non-empty conditions in a `Match` are ANDed.
- Regexes are Go RE2 (linear time, no catastrophic backtracking) — safe on untrusted output.
- Matching runs against the redacted log tail (secret redaction happens first; see
  [architecture.md](architecture.md#security-model)), so rules must not depend on secret values.
- Rules are matched against a bounded byte tail to keep detection O(1) in log size.

## Built-in rule sets

> These patterns are **starting points and MUST be verified** against real provider output
> captured in `testdata/` during each adapter milestone. Refine the regexes from real
> fixtures; do not ship guesses as final.

### Generic (applies to all providers, low priority)

| Name | Category | Priority | Match (illustrative) |
| --- | --- | --- | --- |
| `generic.overloaded` | `TRANSIENT_ERROR` | 40 | any regex `(?i)overloaded|503|502|temporarily unavailable` |
| `generic.network` | `TRANSIENT_ERROR` | 40 | any regex `(?i)connection reset|timeout|ETIMEDOUT|ECONNRESET` |
| `generic.auth` | `FATAL_ERROR` | 90 | any regex `(?i)unauthorized|401|invalid api key|authentication failed` |
| `generic.success` | `SUCCESS` | 1 | exit_code in [0] |

### Claude Code (validated against fixtures — `claude 2.1.199`)

Validated in M10-T2 against the fixtures in `testdata/claude/` (see its README for which are
real captures vs documented formats; refreshing is tracked in issue #11). Rules prefer the
structural `"api_error_status":<code>` field of the headless stream-json result event over
prose, so message-wording changes don't silently break classification. The authoritative rule
set is `internal/provider/claude/rules.go`; this table summarizes it.

| Name | Category | Priority | Match | Reset extractor |
| --- | --- | --- | --- | --- |
| `claude.auth` | `FATAL_ERROR` | 95 | any regex `(?i)"api_error_status":40[13]|"error":"authentication_failed"|invalid api key|authentication failed|unauthorized` | — |
| `claude.session_not_found` | `FATAL_ERROR` | 90 | any regex `(?i)no conversation found with session id` | — |
| `claude.credit` | `QUOTA_EXHAUSTED` | 85 | any regex `(?i)credit balance is too low|insufficient credit|quota exceeded|billing` | — |
| `claude.usage_limit` | `SESSION_LIMIT` | 82 | any regex `(?i)usage limit reached|limit will reset` | `unix_seconds` from `usage limit reached\|(\d{9,12})` |
| `claude.rate_limit` | `RATE_LIMIT` | 80 | any regex `(?i)"api_error_status":429|rate limit` | — |
| `claude.overloaded` | `TRANSIENT_ERROR` | 70 | any regex `(?i)"api_error_status":529|overloaded_error|overloaded` | — |
| `claude.network` | `TRANSIENT_ERROR` | 40 | any regex `(?i)connection reset|connection refused|ETIMEDOUT|ECONNRESET|ENETUNREACH|network error` | — |
| `claude.success` | `SUCCESS` | 1 | exit_code in [0] | — |

`claude.usage_limit` sits above `claude.rate_limit` so a message mentioning both the usage
cap and rate limiting classifies as the session cap, which carries the reset epoch.
`claude.session_not_found` is `FATAL_ERROR`, not retryable: resuming a session id the CLI has
no record of (`"No conversation found with session ID: <id>"`) can never succeed on retry, so
failing fast beats looping as UNKNOWN.

### GitHub Copilot CLI (illustrative — verify during its milestone)

Add the analogous rows once real output is captured. Follow the same category mapping.

### Fake provider (deterministic, for tests)

| Name | Category | Priority | Match | Reset extractor |
| --- | --- | --- | --- | --- |
| `fake.reset` | `RATE_LIMIT` | 80 | stdout regex `FAKE_RESET_AT=(\S+)` | `rfc3339` from the same capture |
| `fake.quota` | `QUOTA_EXHAUSTED` | 85 | stdout regex `FAKE_QUOTA_EXHAUSTED` | — |
| `fake.fatal` | `FATAL_ERROR` | 95 | stdout regex `FAKE_FATAL` | — |
| `fake.success` | `SUCCESS` | 1 | exit_code in [0] | — |

## User overrides

Users add or override rules in `config.toml` under `[[detection.rules]]` (schema in
[configuration.md](configuration.md)). User rules are merged with built-ins; when a user rule
shares a `name` with a built-in, the user rule **replaces** it. Otherwise both apply, ordered
by priority. This lets users patch classification for a new provider message without a code
change.
