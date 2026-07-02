# CLI reference

The complete `defib` command surface. This document owns command syntax, flags, and exit
codes. It does not re-explain concepts — it links to the canonical docs.

## Conventions

- Global form: `defib [global-flags] <command> [command-flags] [args]`.
- **Passthrough separator `--`:** everything after `--` is forwarded verbatim to the provider
  command (appended after defib-controlled flags). Use it to pass provider-specific options.
- A **task selector** is either a Task id (full UUID or unambiguous prefix) or a Task `--name`.
- Times/durations accept Go duration syntax (`30m`, `2h`) or RFC3339 timestamps where noted.

## Global flags

| Flag | Description |
| --- | --- |
| `--config <file>` | Use a specific global config file instead of the default. |
| `--no-autostart` | Do not auto-start the Daemon if it is not running; fail instead. |
| `--json` | Machine-readable JSON output (for scripting). |
| `-q, --quiet` | Suppress non-essential output. |
| `-v, --verbose` | Increase client log verbosity (repeatable). |
| `--version` | Print version and schema version, then exit. |

## Commands

### `defib start` — create and start a Task

```
defib start [flags] [-- <provider passthrough args>]
```

| Flag | Default | Description |
| --- | --- | --- |
| `-p, --prompt <text>` | — | The instruction for the agent. Use `--prompt-file` for long input. |
| `--prompt-file <file>` | — | Read the prompt from a file (`-` for stdin). |
| `--provider <name>` | config `default_provider` | Provider to use (see [providers.md](providers.md)). |
| `--mode <headless\|interactive>` | config `default_mode` | Execution mode (interactive is a later milestone). |
| `--session <new\|ID>` | `new` | Start a new Session, or attach to an existing provider Session `ID` (`session_mode=existing`). |
| `--cwd <dir>` | current dir | Working directory the provider runs in. |
| `--name <name>` | short id | Human-friendly Task name (must be unique among active Tasks). |
| `--model <model>` | provider default | Provider model override. |
| `--unattended` | config value | Opt into the provider's skip-approvals flag. **Dangerous** — see [architecture.md](architecture.md#security-model). |
| `--max-attempts <n>` | config `retry.max_attempts` | Override attempt cap for this Task. |
| `--deadline <dur\|ts>` | config `retry.deadline` | Override deadline cap for this Task. |
| `--detach` | on | Return immediately after the Task is registered (Daemon keeps running it). |
| `--attach` | off | After creating, stream events/logs (equivalent to `start --detach=false` then `attach`). |

On success prints the Task id (and name). Exit code `0`.

### `defib attach` — stream a running Task

```
defib attach <task>
```
Streams state-change events and live log lines until the Task reaches a terminal state or the
user detaches with `Ctrl-C`. **Detaching does not stop the Task** — the Daemon keeps
supervising it. Uses IPC `events.subscribe` + `task.logs --follow`.

### `defib list` — list Tasks

```
defib list [--all] [--status <STATE>]
```
Shows id, name, provider, status, attempt count, and next wake time. `--all` includes terminal
Tasks; by default only non-terminal Tasks are shown. `--json` emits an array.

### `defib status` — detailed Task status

```
defib status <task>
```
Prints the Task fields, the Attempt history (Outcome, exit code, matched rule, Reset Time), the
current `next_wake_at`, and the `exit_reason` if terminal.

### `defib logs` — view captured logs

```
defib logs <task> [--attempt <n>] [--follow] [--stream <stdout|stderr|both>]
```
Prints stored logs for the given Attempt (default: latest). `--follow` tails live output of a
`RUNNING` Attempt.

### `defib resume` — resume now

```
defib resume <task>
```
Forces an immediate next Attempt, skipping any remaining wait. Valid for `WAITING` and
`PAUSED` Tasks; `conflict` error otherwise. Uses the stored Session Ref (native Resume).

### `defib pause` / `defib stop` / `defib cancel`

```
defib pause <task>     # stop scheduling further Attempts; current child is allowed to finish
defib stop  <task>     # hard stop: kill the running child (process group) and mark STOPPED
defib cancel <task>    # alias for stop
```
See the pause vs stop distinction in
[architecture.md](architecture.md#task-lifecycle-state-machine).

### `defib rm` — remove a Task

```
defib rm <task> [--force]
```
Removes a terminal Task and its stored artifacts (attempt logs). Refuses non-terminal Tasks
unless `--force` (which stops it first).

### `defib config` — inspect/validate config

```
defib config path                 # print resolved config file locations
defib config show [--effective]   # print merged config (redacted); --effective resolves for cwd
defib config validate [--config <file>]
defib config get <key>
defib config set <key> <value>    # writes to the global config file
```

### `defib providers` — list providers

```
defib providers
```
Lists registered providers and their `Capabilities` (see [providers.md](providers.md)).

### `defib daemon` — manage the Daemon

```
defib daemon run       # run in the foreground (used by the service and by auto-start, detached)
defib daemon start     # start the Daemon detached if not already running
defib daemon stop      # graceful shutdown (IPC daemon.shutdown)
defib daemon status    # print pid, uptime, socket path, task counts
```

### `defib install-service` / `defib uninstall-service`

```
defib install-service [--start]     # install systemd user unit (Linux) or launchd agent (macOS)
defib uninstall-service
```
Enables machine-restart Recovery by running `defib daemon run` on login/boot
(see [architecture.md](architecture.md#recovery)).

### `defib doctor` — environment diagnostics

```
defib doctor
```
Checks: provider binaries present and versions, config validity, state/runtime dir
permissions, Daemon reachability, and whether the service is installed. Prints actionable
findings.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Generic error. |
| `2` | Invalid usage / bad flags (`invalid_params`). |
| `3` | Task not found (`not_found`). |
| `4` | Illegal state transition (`conflict`). |
| `5` | Daemon unreachable and auto-start failed / `--no-autostart`. |
| `6` | Provider unavailable (`provider_unavailable`). |

`--json` output includes `{ "error": { "code": "...", "message": "..." } }` on failure,
mirroring the IPC error codes in [architecture.md](architecture.md#ipc-protocol).
