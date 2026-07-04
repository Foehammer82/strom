# Contributing to Wattkeeper

Thanks for contributing. Day-to-day work here is expected to happen on feature branches and through pull requests. Direct pushes to `main` should be rare and reserved for maintainer use only.

## Table Of Contents

- [Start Here](#start-here)
- [Workflow](#workflow)
- [What To Change](#what-to-change)
- [Code Of Conduct](#code-of-conduct)
- [Testing And Validation](#testing-and-validation)
- [Release And Maintainer Notes](#release-and-maintainer-notes)
- [Project Links](#project-links)

## Start Here

If you are new to the project, read [README.md](README.md) first, then review [ROADMAP.md](ROADMAP.md) so you understand the current phase and what still belongs in scope.

If you are planning maintainer work, start in the repo root and follow the phase-specific guidance in [ROADMAP.md](ROADMAP.md). The most useful anchors for implementation work are [agent/](agent/), [controller/](controller/), [deploy/](deploy/), and [image/](image/).

## Workflow

1. Create a feature branch for your work.
2. Open a pull request early if you want feedback or need CI coverage.
3. Keep changes scoped to one phase or one fix when possible.
4. Wait for CI to pass before merging to `main`.
5. Use an annotated tag only when you are intentionally cutting a release.

For maintainers, the default review path is branch first, PR second, merge last. That keeps the repo history reviewable and makes CI the normal gate for changes.

## What To Change

This project is being built in phases. Please keep contributions aligned to the current roadmap phase unless a small prerequisite is needed to keep the repo buildable.

- Phase-specific implementation work should follow [ROADMAP.md](ROADMAP.md).
- Small fixes, documentation improvements, and test coverage are welcome at any time.
- If you are adding new behavior, include tests when practical, especially for parsers, renderers, release automation, and other text-heavy code.
- Prefer deterministic output for generated files so diffs stay stable and easy to review.

## Code Of Conduct

This project follows [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). If you need to
report a conduct issue, use the private reporting path described there rather
than opening a public issue.

## Testing And Validation

CI runs lint, tests, and the agent cross-build on pushes and pull requests through `.github/workflows/ci.yml`.

Before opening a PR, run the narrowest useful validation for your change. At minimum, make sure the relevant package tests or `make test` pass locally when your change affects code.

For image or release work, validate with the local commands described in [README.md](README.md) and [ROADMAP.md](ROADMAP.md). The image pipeline has separate build expectations from the Go binaries, so do not assume one covers the other.

## Release And Maintainer Notes

Release tags are expected to be created by maintainers from a reviewed commit, not from random feature branches.

- Stable releases use `vMAJOR.MINOR.PATCH`, for example `v0.2.0`.
- Release candidates use `vMAJOR.MINOR.PATCH-rcN`, for example `v0.2.0-rc1`.
- Other prereleases may use `vMAJOR.MINOR.PATCH-QUALIFIERN`, for example `v0.2.0-beta1`.
- Any tag containing a hyphen is published by GitHub Actions as a prerelease.
- Do not reuse, retag, or force-move an existing release tag.

If you are validating release behavior, prefer an `-rcN` tag first, then promote to a stable tag only after the artifacts and smoke checks look correct.

## Project Links

- [README.md](README.md) for the project overview and current capabilities
- [docs/](docs) for user-facing setup, feature, FAQ, and reference documentation
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community expectations and reporting guidance
- [ROADMAP.md](ROADMAP.md) for phase scope and exit criteria
- [.github/copilot-instructions.md](.github/copilot-instructions.md) for repo-specific Copilot guidance
- [.github/skills/](.github/skills) for validation and Pi-node workflows
- [Bug report template](.github/ISSUE_TEMPLATE/bug_report.md)
- [Feature request template](.github/ISSUE_TEMPLATE/feature_request.md)
- [Pull request template](.github/PULL_REQUEST_TEMPLATE.md)

If you want to improve the templates or refine the code of conduct, please open a PR for it.
