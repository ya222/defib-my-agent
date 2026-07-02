# TODO — implementation plan

This is the **execution roadmap**. Work top to bottom, one task per pull request. Every task
lists the packages it touches, exact acceptance criteria, and its dependencies. Do not start a
task whose dependencies are unchecked. Do not make design decisions — the design is fixed in
[docs/](docs/). If a task is under-specified for you to implement without guessing, open an
issue instead of guessing (see [AGENTS.md](AGENTS.md)).

**Legend:** `[ ]` not started · `[~]` in progress · `[x]` done. Task ids are `M<milestone>-T<n>`.

**Global rules for every task:** meet the Definition of Done in [AGENTS.md](AGENTS.md), keep the
relevant doc in sync, and never call a real provider in tests.

---

## M0 — Scaffolding & tooling

Goal: a buildable, testable, lintable empty skeleton matching the fixed layout.

- [x] **M0-T1 — Initialize the module and layout.**
  - Create `go.mod` (`module github.com/<org>/defib`, Go 1.22), the directory tree from
    [docs/architecture.md](docs/architecture.md#repository-layout), and a `cmd/defib/main.go`
    that prints version and exits.
  - Add `internal/version` with `Version` and `SchemaVersion` constants.
  - Accept: `go build ./...` succeeds; `./cmd/defib` (built) prints a version string.
- [x] **M0-T2 — Makefile and lint config.**
  - Add `Makefile` targets `build test lint fmt check e2e tools` (as in
    [AGENTS.md](AGENTS.md#standard-commands)) and a `.golangci.yml` with a sensible default
    linter set (govet, staticcheck, errcheck, ineffassign, revive).
  - Accept: `make build`, `make fmt`, `make lint`, `make test` all run (tests may be empty) and
    exit `0` on a clean tree.
- [x] **M0-T3 — CI workflow.**
  - Add a CI config running `make check` on Linux and macOS with Go 1.22. Cache modules.
  - Accept: CI is green on a no-op PR; race detector enabled in the test step.

## M1 — Paths & configuration

Goal: resolve directories and load/validate layered config exactly as specified.

Depends on: M0.

- [x] **M1-T1 — `internal/paths`.**
  - Implement Config/State/Runtime dir resolution for Linux (XDG) and macOS, honoring
    `DEFIB_CONFIG_DIR`/`DEFIB_STATE_DIR`/`DEFIB_RUNTIME_DIR`, per
    [docs/architecture.md](docs/architecture.md#on-disk-layout). Create dirs with `0700` when
    missing.
  - Accept: unit tests cover env overrides, XDG defaults, and macOS defaults using a fake HOME.
- [x] **M1-T2 — Config structs + defaults.**
  - Implement the full schema structs in `internal/config` with the exact defaults from
    [docs/configuration.md](docs/configuration.md). Parse TOML via `pelletier/go-toml/v2`.
  - Accept: loading an empty file yields all documented defaults; round-trip test of a full
    example config from `testdata/`.
- [x] **M1-T3 — Layering + env + precedence.**
  - Merge built-in < global < project `.defib.toml` (nearest ancestor) < env (`DEFIB_*`
    scalars) < explicit overrides, per
    [docs/configuration.md](docs/configuration.md#precedence-highest-wins).
  - Accept: table tests prove precedence for representative keys, including a `.defib.toml`
    discovered in a parent directory.
- [x] **M1-T4 — Validation.**
  - Implement all validation rules in
    [docs/configuration.md](docs/configuration.md#validation). Errors report the key path.
  - Accept: tests for each failure mode; valid configs pass.

## M2 — Persistence

Goal: crash-safe SQLite store and the on-disk task tree.

Depends on: M1.

- [x] **M2-T1 — Store bootstrap + migrations.**
  - `internal/store` opens `defib.db` with WAL + foreign keys, runs embedded ordered
    migrations, and records `schema_version` in `daemon_meta`, per
    [docs/architecture.md](docs/architecture.md#data-model).
  - Accept: opening a fresh DB creates all tables; re-opening is a no-op; schema version matches
    `internal/version.SchemaVersion`.
- [ ] **M2-T2 — Models + CRUD.**
  - Implement typed models and transactional CRUD for `tasks`, `attempts`, `events` (single
    writer connection). Provide `CreateTask`, `UpdateTaskTx`, `AddAttempt`, `AppendEvent`,
    `ListTasks`, `GetTask`.
  - Accept: tests cover create/read/update, cascade delete, and that a state change writes
    task+attempt+event atomically (one transaction; rollback on error leaves DB unchanged).
- [ ] **M2-T3 — Task artifact directories.**
  - Helpers to create/resolve `tasks/<id>/attempts/<n>/` with Task-id validation
    (`^[a-f0-9-]{36}$`) and `0700` perms.
  - Accept: path-traversal inputs are rejected; log paths returned match the layout in
    [docs/architecture.md](docs/architecture.md#on-disk-layout).

## M3 — Process runner & logging

Goal: spawn a child, capture output to files, kill the whole tree, redact secrets.

Depends on: M2.

- [ ] **M3-T1 — `internal/logging`.**
  - slog setup (JSON to `daemon.log`, level from config) and a redactor for the secret shapes
    in [docs/architecture.md](docs/architecture.md#security-model).
  - Accept: redactor tests for `sk-…`, `ghp_…`, `Bearer …`, `Authorization:` and
    `*_TOKEN/_KEY/_SECRET` env values; non-secrets pass through unchanged.
- [ ] **M3-T2 — `internal/process` runner.**
  - Spawn a `Command` (argv + env + cwd) in its own process group (`Setpgid`), stream stdout and
    stderr through the redactor to the attempt log files, enforce a max-output guard, and expose
    `Wait()` returning exit code. `Kill()` terminates the entire process group.
  - Accept: tests using short helper scripts prove: output is captured verbatim (minus
    redaction), exit code is reported, and `Kill()` reaps children. Uses an injectable clock/ctx.
- [ ] **M3-T3 — Live log tailing.**
  - Provide a follow-reader so `task.logs --follow` can stream an in-progress attempt file.
  - Accept: a reader receives appended lines while the writer is still open.

## M4 — Provider abstraction & the fake provider

Goal: the interface, the registry, and a deterministic provider to test everything else.

Depends on: M3.

- [ ] **M4-T1 — Provider interface + registry.**
  - Define `Provider`, `Capabilities`, `Command`, `TaskSpec`, `Availability` exactly as in
    [docs/providers.md](docs/providers.md); add a registry with `Register`/`Get`/`List`.
  - Accept: interface compiles; registry lookup + capability listing tested.
- [ ] **M4-T2 — Fake provider.**
  - Implement `internal/provider/fake` with the script format and built-in detection rules in
    [docs/providers.md](docs/providers.md#the-fake-provider-internalproviderfake--required-for-testing)
    and [docs/detection.md](docs/detection.md#fake-provider-deterministic-for-tests). Supports
    `ClientSuppliedID` and `Resume` (advance to next Attempt block).
  - Accept: given a multi-block script, successive `BuildStart`/`BuildResume` invocations, run
    through the M3 runner, produce the scripted output and exit codes deterministically.

## M5 — Detection engine

Goal: classify Attempts into Outcomes + Reset Times from rules.

Depends on: M4.

- [ ] **M5-T1 — Rule types + engine.**
  - Implement `Rule`, `Match`, `Extractor` and the priority-ordered, first-match engine over a
    bounded stream tail (`detect.scan_bytes`), per [docs/detection.md](docs/detection.md).
  - Accept: table tests map fixture outputs to expected `(category, matched_rule)`; AND-semantics
    and priority ordering verified.
- [ ] **M5-T2 — Reset-time extractors.**
  - Implement all five extractor kinds and the "past reset time is ignored" rule.
  - Accept: tests for each kind, including HTTP-date `Retry-After`, relative durations, and
    `clock_time` next-occurrence, using a fixed injected clock.
- [ ] **M5-T3 — Rule merging.**
  - Merge provider built-ins with user config rules; same-`name` user rule replaces the built-in.
  - Accept: tests prove replacement and additive merge, ordered by priority.

## M6 — Scheduler

Goal: compute next wake time; manage per-task timers.

Depends on: M5.

- [ ] **M6-T1 — Wake-time computation.**
  - Implement the exact formula in [docs/architecture.md](docs/architecture.md#scheduling):
    reset-time preference, full-jitter backoff, deadline clamp.
  - Accept: deterministic tests with an injected clock and seeded RNG cover reset-present,
    reset-absent, and deadline-clamped cases.
- [ ] **M6-T2 — Caps evaluation.**
  - Implement `max_attempts`, `deadline`, `max_total_wait` checks returning which cap was hit.
  - Accept: boundary tests for each cap.
- [ ] **M6-T3 — Timer management.**
  - One timer per waiting Task; firing posts a `timer_fire` event; timers are cancelable and
    re-armable. No polling/sleep loops.
  - Accept: tests using a fake clock verify fire, cancel, re-arm, and immediate-wake for past
    times.

## M7 — Supervisor state machine (in-process)

Goal: the per-Task lifecycle, wired to store + provider + detector + scheduler, without the
daemon/IPC yet.

Depends on: M6.

- [ ] **M7-T1 — State machine core.**
  - Implement the transition table and supervisor loop from
    [docs/architecture.md](docs/architecture.md#task-lifecycle-state-machine) as pure logic
    consuming events and emitting actions. Persist each transition in one transaction (M2-T2).
  - Accept: table-driven tests drive every transition (including caps-exceeded → FAILED) with a
    fake provider + fake clock; each transition persists task+attempt+event.
- [ ] **M7-T2 — Session handling.**
  - New vs existing Session, pre-generate vs parse (via `ExtractSessionRef`), and Resume path,
    per [docs/providers.md](docs/providers.md#session-strategy-important).
  - Accept: tests prove first Attempt of `session_mode=existing` uses `BuildResume`; a parsed ref
    is stored before any Resume; pre-generated ids are passed through.
- [ ] **M7-T3 — Availability probe integration.**
  - While waiting on `QUOTA_EXHAUSTED`, run the configured probe at `availability.poll_interval`
    and wake early on success; no probe configured ⇒ pure schedule.
  - Accept: fake-probe tests show early wake on success and normal wake otherwise.

## M8 — Daemon, IPC, and CLI client

Goal: the running system a user can drive end-to-end (headless, fake + Claude-shaped).

Depends on: M7.

- [ ] **M8-T1 — IPC transport.**
  - `internal/ipc`: newline-delimited JSON framing, request/response + streaming envelopes, and
    a Unix-socket server + client, per
    [docs/architecture.md](docs/architecture.md#ipc-protocol). Socket `0600`; refuse unsafe
    socket paths.
  - Accept: round-trip tests for single-shot and streaming methods; error-code mapping tested.
- [ ] **M8-T2 — Daemon server.**
  - `internal/daemon`: task registry, per-Task goroutine + event channel, event bus for
    subscribers, wiring of store/provider/detect/scheduler/process. Implements all IPC methods
    from [docs/architecture.md](docs/architecture.md#ipc-protocol).
  - Accept: an in-process test creates a Task on the fake provider and observes it run to
    `SUCCEEDED`, and a scripted rate-limit Task waits then resumes to `SUCCEEDED`.
- [ ] **M8-T3 — Daemon lifecycle + auto-start.**
  - `defib daemon run|start|stop|status`; client auto-starts a detached daemon unless
    `--no-autostart`; `daemon.pid` + graceful `daemon.shutdown`.
  - Accept: starting a client with no daemon spawns one; `daemon stop` shuts it down cleanly.
- [ ] **M8-T4 — CLI commands.**
  - `internal/cli`: implement `start, attach, list, status, logs, resume, pause, stop, cancel,
    rm, config, providers, daemon, doctor` per [docs/cli.md](docs/cli.md), including global
    flags, `--json`, and exit codes. Client stays thin.
  - Accept: e2e test (`make e2e`) drives a full fake-provider Task through
    `start → attach → (rate-limit wait) → resume → SUCCEEDED`, plus `list/status/logs` output
    assertions and exit-code checks.

## M9 — Recovery

Goal: survive daemon restarts and reboots.

Depends on: M8.

- [ ] **M9-T1 — Reconcile on startup.**
  - Implement the idempotent `daemon.Reconcile` for RUNNING/WAITING/PAUSED tasks per
    [docs/architecture.md](docs/architecture.md#recovery), honoring `on_interrupt`.
  - Accept: e2e test starts a fake Task, kills the daemon mid-attempt, restarts it, and the Task
    resumes (via stored session ref) to `SUCCEEDED`; a WAITING task with a past `next_wake_at`
    wakes immediately; a PAUSED task stays paused. `Reconcile` run twice changes nothing.

## M10 — Claude Code adapter

Goal: first real provider.

Depends on: M9.

- [ ] **M10-T1 — Adapter + flag verification.**
  - Implement `internal/provider/claude` per
    [docs/providers.md](docs/providers.md#claude-code-adapter-internalproviderclaude--first-class).
    **Verify every flag** against a pinned `claude` version; record the version in the package
    doc comment and capture real output fixtures in `testdata/claude/`.
  - Accept: unit tests build expected start/resume argv; `ExtractSessionRef` parses the fixture;
    manual smoke-test instructions documented. No live calls in CI.
- [ ] **M10-T2 — Claude detection rules.**
  - Replace the illustrative rules in
    [docs/detection.md](docs/detection.md#claude-code-illustrative--verify) with rules validated
    against captured fixtures.
  - Accept: fixture-based tests classify real rate-limit/usage-limit/credit/overloaded outputs
    into the correct categories with correct Reset Times.
- [ ] **M10-T3 — Unattended safety.**
  - Implement `providers.claude.unattended` / `--unattended` → provider skip-approvals flag +
    prominent warning; never default-on.
  - Accept: tests prove the flag is absent by default and present only when opted in, and the
    warning is emitted.

## M11 — Service install & reboot recovery

Goal: machine-restart durability.

Depends on: M9 (M10 recommended).

- [ ] **M11-T1 — systemd user unit (Linux).**
  - `internal/service` generates and installs/enables a user unit running `defib daemon run`;
    `install-service`/`uninstall-service` commands.
  - Accept: on a Linux CI/container, install → `systemctl --user` shows the unit; uninstall
    removes it. (Actual reboot is manually verified; document the steps.)
- [ ] **M11-T2 — launchd agent (macOS).**
  - Generate/install a `LaunchAgent` plist equivalent.
  - Accept: on macOS CI, `launchctl` lists the agent after install; uninstall removes it.

## M12 — GitHub Copilot CLI adapter

Goal: second provider through the same abstraction.

Depends on: M10.

- [ ] **M12-T1 — Adapter + flag verification.**
  - Implement `internal/provider/copilot` per
    [docs/providers.md](docs/providers.md#github-copilot-cli-adapter-internalprovidercopilot--planned);
    verify flags against a pinned `copilot` version; capture fixtures in `testdata/copilot/`.
  - Accept: start/resume argv tests; session strategy chosen from real capabilities; no live CI
    calls.
- [ ] **M12-T2 — Copilot detection rules + docs.**
  - Add the Copilot rule set to [docs/detection.md](docs/detection.md) from fixtures; update the
    provider table in [README.md](README.md).
  - Accept: fixture-based classification tests pass.

## M13 — Notifications & richer availability

Goal: user-visible signals and better quota handling.

Depends on: M8.

- [ ] **M13-T1 — Notification hooks.**
  - Fire `notifications.on_state_change` (argv, no shell) for configured target states with JSON
    context appended, per [docs/configuration.md](docs/configuration.md).
  - Accept: a fake hook receives the expected event JSON on `SUCCEEDED`/`FAILED`.
- [ ] **M13-T2 — Availability command polish.**
  - Harden the external `availability.command` probe: timeout, exit-code handling, backoff on
    probe failure.
  - Accept: tests for available/unavailable/erroring probes.

## M14 — Interactive (PTY) mode

Goal: support providers/flows that need a terminal.

Depends on: M10.

- [ ] **M14-T1 — PTY runner.**
  - Add a PTY-backed path in `internal/process` using `creack/pty`; capture + tee output;
    resize handling; only used when `mode=interactive` and the provider advertises it.
  - Accept: a PTY test drives an interactive fake and captures output; headless path unchanged.
- [ ] **M14-T2 — Attach passthrough.**
  - `defib attach` forwards input to an interactive Task's PTY; detaching leaves it running.
  - Accept: e2e test types into an interactive fake and observes the response; detach keeps the
    Task alive.

## M15 — Packaging, release, polish

Goal: shippable v1.

Depends on: M10, M11.

- [ ] **M15-T1 — Release build.**
  - Add `goreleaser` config producing Linux/macOS (amd64/arm64) binaries + checksums; wire a
    tag-triggered release workflow.
  - Accept: a dry-run release produces artifacts for all targets.
- [ ] **M15-T2 — `defib doctor` completeness.**
  - Implement all checks in [docs/cli.md](docs/cli.md#defib-doctor--environment-diagnostics).
  - Accept: doctor reports provider presence/versions, dir perms, daemon reachability, and
    service state with actionable messages.
- [ ] **M15-T3 — Docs pass + install instructions.**
  - Finalize install instructions in [README.md](README.md); ensure every doc cross-link
    resolves and no information is duplicated across docs.
  - Accept: link check passes; a reviewer confirms each fact lives in exactly one doc.

---

## Deferred / out of scope for v1

- Windows support (named pipes; Scheduled Task) — revisit after v1.
- Remote/multi-machine orchestration, web UI, hosted service.
- Provider *output* evaluation (judging the agent's code). defib supervises processes only.
