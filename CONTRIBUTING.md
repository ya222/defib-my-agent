# Contributing

Thanks for helping build defib. This file covers the human process. Technical setup, build/test
commands, coding conventions, and the definition of done live in **[AGENTS.md](AGENTS.md)** —
they are not repeated here.

## Before you start

- Read [README.md](README.md) for what defib is, and [docs/architecture.md](docs/architecture.md)
  for how it is designed. The design is intentionally fixed.
- Pick the next milestone in [TODO.md](TODO.md). Create a feature branch for it
  (`milestone/m<n>-<slug>`) off the latest `main`, and work its tasks in order — one commit per
  task.

## Proposing changes

- **Milestone and task work:** work a whole milestone on one feature branch, one commit per task
  (each commit references its task id, e.g. `M6-T2`, and ticks its checkbox). When the milestone
  is complete and `make check` passes, commit, push the branch, and open a PR that lists the
  tasks it completes. Standalone bug fixes may use their own branch and PR.
- **Design changes:** open an **issue** first. Do not change component boundaries, the IPC
  protocol, the data model, dependencies, or add packages via a PR without an approved issue.
  Implementing agents are explicitly not allowed to make these calls (see [AGENTS.md](AGENTS.md)).
- **Docs:** if your code change affects a doc, update that doc in the same PR. Each fact lives in
  exactly one doc — fix it at its source, do not copy it.

## Pull request checklist

- Every task in the milestone meets the Definition of Done in
  [AGENTS.md](AGENTS.md#definition-of-done-every-task).
- `make check` passes locally (format, lint, race tests).
- The branch is named `milestone/m<n>-<slug>`; commits use
  [Conventional Commits](https://www.conventionalcommits.org/), one per task.
- No real-provider calls in tests; the `fake` provider is used instead.
- The PR lists the completed tasks and excludes unrelated changes.

## Reporting issues

Open an issue with: what you did, what you expected, what happened, `defib --version`, your OS,
and (if relevant) `defib doctor` output. Never paste secrets or unredacted logs.

## Code of conduct

Be respectful and constructive. Harassment or abuse is not tolerated. Maintainers may remove
contributions or contributors that violate this expectation.

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache-2.0](LICENSE) license.
