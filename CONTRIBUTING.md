# Contributing

Thanks for helping build defib. This file covers the human process. Technical setup, build/test
commands, coding conventions, and the definition of done live in **[AGENTS.md](AGENTS.md)** —
they are not repeated here.

## Before you start

- Read [README.md](README.md) for what defib is, and [docs/architecture.md](docs/architecture.md)
  for how it is designed. The design is intentionally fixed.
- Pick up an open issue (or open one for the change you want to make). Create a feature branch off
  the latest `main` and keep it focused on that one change.

## Proposing changes

- **Feature and fix work:** keep each branch focused on one change, and open a PR when
  `make check` passes. Standalone bug fixes use their own branch and PR.
- **Design changes:** open an **issue** first. Do not change component boundaries, the IPC
  protocol, the data model, dependencies, or add packages via a PR without an approved issue.
  Implementing agents are explicitly not allowed to make these calls (see [AGENTS.md](AGENTS.md)).
- **Docs:** if your code change affects a doc, update that doc in the same PR. Each fact lives in
  exactly one doc — fix it at its source, do not copy it.

## Pull request checklist

- The change meets the Definition of Done in
  [AGENTS.md](AGENTS.md#definition-of-done-every-task).
- `make check` passes locally (format, lint, race tests).
- Commits use [Conventional Commits](https://www.conventionalcommits.org/).
- No real-provider calls in tests; the `fake` provider is used instead.
- The PR describes the change and excludes unrelated edits.

## Reporting issues

Open an issue with: what you did, what you expected, what happened, `defib --version`, your OS,
and (if relevant) `defib doctor` output. Never paste secrets or unredacted logs.

## Code of conduct

Be respectful and constructive. Harassment or abuse is not tolerated. Maintainers may remove
contributions or contributors that violate this expectation.

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache-2.0](LICENSE) license.
