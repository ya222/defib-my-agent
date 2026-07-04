# Configuration

This document owns the **complete configuration schema**, its **precedence**, and its
**environment/override mapping**. It does not repeat the meaning of scheduling, detection, or
provider behavior — it only lists the knobs and links to where each is used.

## File locations

- Global config: `<config-dir>/config.toml` (see paths in
  [architecture.md](architecture.md#on-disk-layout)).
- Per-project config: `.defib.toml` in the Task's working directory (or any ancestor up to the
  filesystem root; the nearest one wins).
- Both are TOML. Missing files are fine — every key has a default.

## Precedence (highest wins)

1. Command-line flags (see [cli.md](cli.md)).
2. Environment variables (`DEFIB_*`, mapping below).
3. Per-project `.defib.toml`.
4. Global `config.toml`.
5. Built-in defaults.

Resolution happens **at Task creation**; the effective policy is snapshotted into
`tasks.config_json` so later config edits never change an in-flight Task's behavior.

## Environment variable mapping

- Path overrides: `DEFIB_CONFIG_DIR`, `DEFIB_STATE_DIR`, `DEFIB_RUNTIME_DIR`.
- Any scalar config key maps to `DEFIB_` + the uppercased dotted path with dots as
  underscores. Example: `retry.max_attempts` → `DEFIB_RETRY_MAX_ATTEMPTS`.
- Only scalars are settable via env; arrays/tables (like detection rules) must use a file.

## Full schema

Defaults shown are the built-in values. All keys are optional.

```toml
# ---------------------------------------------------------------------------
# Top-level
# ---------------------------------------------------------------------------
default_provider = "claude"        # provider used when --provider is omitted
default_mode     = "headless"      # "headless" | "interactive"

# ---------------------------------------------------------------------------
# [retry] — policy Caps and Backoff. Used by the Scheduler (architecture.md#scheduling)
# and the caps check (architecture.md#task-lifecycle-state-machine).
# ---------------------------------------------------------------------------
[retry]
max_attempts   = 0            # 0 = unlimited attempts (bounded only by deadline/total_wait)
deadline       = ""          # absolute-or-relative cap, e.g. "48h" or "2026-07-05T00:00:00Z"; "" = none
max_total_wait = "72h"       # cumulative time allowed to spend WAITING; "" = unlimited
backoff_base   = "30s"       # first backoff delay
backoff_factor = 2.0         # exponential growth factor
backoff_max    = "1h"        # cap on a single backoff delay
backoff_jitter = 0.2         # ±fraction of jitter applied to each delay
reset_buffer   = "15s"       # added after a detected Reset Time before resuming
on_unknown     = "retry"     # "retry" | "fail" — how to treat UNKNOWN outcomes
on_interrupt   = "backoff"   # "resume_now" | "backoff" — daemon-restart recovery (architecture.md#recovery)

# ---------------------------------------------------------------------------
# [availability] — optional credit/quota probe used for QUOTA_EXHAUSTED
# (see providers.md CheckAvailability and detection.md).
# ---------------------------------------------------------------------------
[availability]
poll_interval = "15m"        # how often to probe while a Task waits on quota
# Optional external probe. Executed as argv (NO shell). Exit 0 => available.
command       = []           # e.g. ["mycli", "credits", "--check"]

# ---------------------------------------------------------------------------
# [logging]
# ---------------------------------------------------------------------------
[logging]
level          = "info"      # "debug" | "info" | "warn" | "error"
retain_attempts = 20         # keep logs for the most recent N attempts per task; 0 = keep all
redact          = true       # redact secret-shaped strings in captured logs (security model)

# ---------------------------------------------------------------------------
# [notifications] — optional hooks fired on state changes. Executed as argv (NO shell).
# defib appends JSON event context as the final argument.
# ---------------------------------------------------------------------------
[notifications]
on_state_change = []         # e.g. ["notify-send", "defib"]
events          = ["SUCCEEDED", "FAILED"]   # which target states fire the hook

# ---------------------------------------------------------------------------
# [providers.<name>] — per-provider settings. See providers.md for meaning.
# ---------------------------------------------------------------------------
[providers.claude]
binary        = "claude"     # executable name or absolute path
model         = ""           # optional model override; "" = provider default
resume_prompt = "Continue the previous task."
unattended    = false        # if true, defib passes the provider's skip-approvals flag (DANGEROUS)
extra_args    = []           # appended to every invocation, before the `--` passthrough

[providers.copilot]
binary        = "copilot"
model         = ""
resume_prompt = "Continue the previous task."
unattended    = false
extra_args    = []

[providers.fake]
script        = ""           # path to a fake-provider script (see providers.md)

# ---------------------------------------------------------------------------
# [detect]
# ---------------------------------------------------------------------------
[detect]
scan_bytes = 65536           # bytes of each stream tail scanned by the detector

# User-defined detection rules (merged with provider built-ins; see detection.md).
# A rule whose `name` matches a built-in REPLACES that built-in.
[[detection.rules]]
name     = "example.custom_quota"
category = "QUOTA_EXHAUSTED"
priority = 86
[detection.rules.match]
any_regex = "(?i)you have run out of credits"
# Optional reset extractor:
# [detection.rules.reset_extractor]
# source = "any"
# regex  = "resets at (\\d{1,2}(?::\\d{2})?\\s?(?:am|pm))"   # exactly one capture group
# kind   = "clock_time"
# format = "3:04pm"
```

## Validation

`internal/config` validates the resolved config at Task creation and on `defib config validate`:

- `default_mode`/`mode` ∈ {headless, interactive}; interactive requires the provider's
  `Capabilities.Interactive`.
- `backoff_factor` ≥ 1.0; `backoff_jitter` ∈ [0, 1]; durations parse via Go `time.ParseDuration`.
- `deadline` parses as either a Go duration (relative to now) or an RFC3339 timestamp.
- `on_unknown` ∈ {retry, fail}; `on_interrupt` ∈ {resume_now, backoff}.
- Every `[[detection.rules]]` has a valid `category`, compilable RE2 regexes, and, if a
  `reset_extractor` is present, a `kind` from the supported set and a regex with exactly one
  capture group.
- `notifications.on_state_change` and `availability.command`, when set, reference an
  executable resolvable on `PATH` or an absolute path (warn, don't fail, if unresolved).

Validation errors are reported with the offending key path and are fatal for Task creation.

## Secrets guidance

Do not put provider API keys in `config.toml`. Providers read their own credentials from their
own config/keychain/env. defib only stores non-secret operational settings. `config.toml`
lives in a `0700` directory but is still plaintext — treat it as trusted, non-secret input.
