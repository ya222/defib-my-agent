# AGENTS.md

Guidance for AI agents and contributors working **on** the defib codebase. This file is about
*how to work here*. It does not describe the system design — that lives in
[docs/architecture.md](docs/architecture.md) and its siblings. Read those before writing code.

## Prime directives

1. **You are not authorized to make architectural decisions.** The design is fixed in
   [docs/](docs/). If a task seems to require a choice not covered there (a new dependency, a
   new package, a protocol change, a schema change), **stop and open an issue** describing the
   gap. Do not invent a solution.
2. **Work one milestone at a time on its own feature branch**, completing that milestone's
   [TODO.md](TODO.md) tasks in order (one commit per task). Do not pull work forward from a
   later milestone or refactor unrelated code.
3. **Never call a real provider (Claude Code, Copilot CLI) in code, tests, or CI.** Use the
   `fake` provider (see [docs/providers.md](docs/providers.md)). Real providers cost money and
   are non-deterministic.
4. **Match the docs, don't contradict them.** If your implementation would differ from a doc,
   the doc is right until an issue changes it. If a doc is wrong, fix the doc in the same PR and
   call it out.
5. **No scope creep.** Only change what the task requires. Do not add features, abstractions,
   comments, or config that the task did not ask for.

## Where design authority lives

| Question | Read |
| --- | --- |
| What are the components / state machine / IPC / data model / recovery? | [docs/architecture.md](docs/architecture.md) |
| How is a provider driven? What's the interface? | [docs/providers.md](docs/providers.md) |
| How are failures classified? | [docs/detection.md](docs/detection.md) |
| What config keys exist? | [docs/configuration.md](docs/configuration.md) |
| What are the commands/flags/exit codes? | [docs/cli.md](docs/cli.md) |
| What does a term mean? | [docs/glossary.md](docs/glossary.md) |
| What do I build next, and when is it done? | [TODO.md](TODO.md) |

The **repository layout** and the **fixed technology choices** are defined once, in
[docs/architecture.md](docs/architecture.md#repository-layout) and
[#technology-choices](docs/architecture.md#technology-choices). Follow them exactly; do not add
top-level packages or swap libraries.

## Environment setup

- Go 1.22+ (`go version`). No cgo — the SQLite driver is pure Go
  (`modernc.org/sqlite`). Do **not** introduce a cgo dependency.
- Install dev tools: `make tools` (installs `golangci-lint`).

## Standard commands

Run these from the repo root. If `make` targets do not exist yet (early M0), create them as
part of the M0 task, then use them.

| Command | Purpose |
| --- | --- |
| `make build` | Build the `defib` binary into `./bin/defib`. |
| `make test` | Run `go test ./...` with race detector. |
| `make lint` | Run `golangci-lint run`. |
| `make fmt` | `gofmt`/`goimports` the tree. |
| `make check` | `fmt` + `lint` + `test` — run before every PR. |
| `make e2e` | End-to-end tests using the `fake` provider. |

## Coding conventions

- **Formatting:** `gofmt` + `goimports`; CI rejects unformatted code.
- **Errors:** return `error`; wrap with `fmt.Errorf("...: %w", err)`. Library/`internal`
  packages must **not** call `os.Exit`, `panic`, or `log.Fatal` — only `cmd/defib` and the
  daemon's top-level may exit the process.
- **Context:** every blocking or spawning function takes `context.Context` as its first arg and
  honors cancellation. Never `time.Sleep` in a supervision loop — use timers/`ctx` (see the
  concurrency model in [docs/architecture.md](docs/architecture.md#concurrency-model)).
- **Logging:** use `log/slog` via `internal/logging`. Never log secrets; rely on the redactor.
- **Concurrency:** one goroutine owns a Task's mutable state (see the concurrency model). Do not
  share Task structs across goroutines; communicate via the Task's event channel.
- **Package boundaries:** respect the dependency direction in
  [docs/architecture.md](docs/architecture.md#repository-layout). Lower-level packages must not
  import `daemon`/`supervisor`/`cli`.
- **No shell:** external hooks/probes run via `os/exec` argv arrays, never `sh -c`.
- **Comments:** explain *why*, not *what*. Do not add comments to code you did not change.

## Testing requirements

- Every package ships table-driven unit tests. New logic is not "done" without tests.
- Use the `fake` provider and fixtures in `testdata/` for anything provider-related. Detection
  rules are tested against recorded output fixtures, not live output.
- Time-dependent code (scheduler, recovery) must accept an injectable clock so tests are
  deterministic — no real sleeps in tests.
- `make check` must pass locally before you open a PR. Do not disable the race detector.

## Git & PR conventions

- **Conventional Commits:** `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`. Scope
  optional, e.g. `feat(scheduler): ...`.
- **One feature branch per milestone.** Before starting a milestone's first task, branch off the
  latest `main` as `milestone/m<n>-<slug>` (e.g. `milestone/m0-scaffolding`). Do every task in
  that milestone on this branch.
- **One commit per TODO task,** in order. Each commit references the task id (e.g. `M6-T2`) and
  ticks that task's checkbox in [TODO.md](TODO.md) **in the same commit**.
- **Finish the milestone with a PR.** When every task in the milestone is done and `make check`
  passes, **commit, push the branch, and open a pull request** whose title/description names the
  milestone (e.g. `M0`) and lists the tasks it completes. The PR — not the individual task — is
  the review unit.
- Keep commits small and reviewable. If a task turns out to be too big, split it into multiple
  commits and note the split in an issue — do not silently expand scope or pull in the next
  milestone.
- Do not commit generated binaries, `bin/`, coverage files, or local state. See `.gitignore`.
- Never use `--no-verify` or bypass CI gates.

## Definition of done (every task)

1. Code compiles; `make check` passes (fmt, lint, race tests).
2. New/changed behavior is covered by tests using the `fake` provider or fixtures.
3. Any doc that the change affects is updated in the same PR (no doc/code drift).
4. The task's acceptance criteria in [TODO.md](TODO.md) are met and its checkbox is ticked.
5. No new dependencies, packages, config keys, or IPC methods beyond what the task specifies.

## Guardrails recap (do not violate)

- No real-provider calls anywhere in the automated test path.
- No cgo. No new third-party dependency without an approving issue.
- Socket `0600`, state/config dirs `0700`; validate Task ids before building file paths.
- `--unattended` / skip-approvals is never the default and always prints the warning.
