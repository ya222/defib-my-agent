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

## Releasing

Releases are cut by pushing a `v*` tag (e.g. `v1.2.3`). The
[release workflow](.github/workflows/release.yml) then:

1. builds the cross-platform binaries and publishes the GitHub release with GoReleaser, and
2. runs [`.github/scripts/supersede-releases.sh`](.github/scripts/supersede-releases.sh), which
   prepends a **"Superseded"** banner to every older release pointing at the new one — so only
   the latest release is presented as current. This is automatic; you never mark releases
   superseded by hand. The step is idempotent (it re-points predecessors and clears the banner
   from whichever release is now latest), and you can run it manually against the repo with
   `REPO=<owner>/<name> bash .github/scripts/supersede-releases.sh`.

## Reporting issues

Open an issue with: what you did, what you expected, what happened, `defib --version`, your OS,
and (if relevant) `defib doctor` output. Never paste secrets or unredacted logs.

## Code of conduct

Be respectful and constructive. Harassment or abuse is not tolerated. Maintainers may remove
contributions or contributors that violate this expectation.

## License

By contributing, you agree that your contributions are licensed under the project's
[Apache-2.0](LICENSE) license.
