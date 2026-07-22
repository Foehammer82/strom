# Contributing to Strom

Thanks for contributing. Day-to-day work here is expected to happen on feature branches and through pull requests. Direct pushes to `main` should be rare and reserved for maintainer use only.

## Table Of Contents

- [Start Here](#start-here)
- [Workflow](#workflow)
- [What To Change](#what-to-change)
- [Naming Conventions](#naming-conventions)
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

## Naming Conventions

To keep language consistent across docs, code, and operations, use this naming pattern:

- Use Strom as the official product name in public docs, release notes, and user-facing UI text.
- Use WK as the engineering shorthand in internal technical writing and compact labels where the full name is too long.
- Keep repository, module, package, and artifact paths as `strom` unless there is an explicit migration plan.
- Keep explicit service and binary names such as `strom-agent` and `strom-controller` for clarity.
- Use the `WK_` prefix for new environment variables unless an existing interface already defines a different prefix.
- Treat Whitaker as an optional informal spoken nickname only; do not use it in user-facing docs, product copy, URLs, or release assets.

## Code Of Conduct

This project follows [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). If you need to
report a conduct issue, use the private reporting path described there rather
than opening a public issue.

## Testing And Validation

CI runs lint, tests, and the agent cross-build on pushes and pull requests through `.github/workflows/ci.yml`.

Use the shared `strom` CLI for local operational workflows whenever possible so contributor and Copilot usage stays aligned and tooling improvements benefit both paths.

Before opening a PR, run the narrowest useful validation for your change. At minimum, make sure the relevant package tests or `uv run strom check test` pass locally when your change affects code.

For editor and commit-time hygiene, install the repo's pre-commit hooks with `uv run strom hooks install` and use `uv run strom hooks run` to check the full tracked tree. The hook set covers whitespace, final newlines, line endings, YAML/TOML/JSON syntax, merge-conflict markers, large files, executable shell script metadata, and `gofmt`.

For image or release work, validate with the local commands described in [README.md](README.md) and [ROADMAP.md](ROADMAP.md). The image pipeline has separate build expectations from the Go binaries, so do not assume one covers the other.

## Release And Maintainer Notes

Release tags are created by CI, not by hand-pushing tags from a workstation. Do not reuse, retag, or force-move an existing release tag.

- `.github/release/version.toml` holds the active `major`/`minor` release train and RC build toggles. It is the single source of truth consumed by `strom release next-version`.
- Every push to `main` (except docs-only changes and commits with `[skip release]` in the message) triggers `.github/workflows/auto-release.yml`, which computes the next patch tag for the configured train, pushes an immutable annotated tag, and calls `.github/workflows/release.yml` to build and publish it.
- Stable releases use `vMAJOR.MINOR.PATCH`, for example `v0.2.0`.
- Release candidates use `vMAJOR.MINOR.PATCH-rcN`, for example `v0.2.0-rc1`. Other prereleases may use `vMAJOR.MINOR.PATCH-QUALIFIERN`, for example `v0.2.0-beta1`.
- Any tag containing a hyphen is published by GitHub Actions as a prerelease, and never receives the `latest` container image tag.
- To bump the major or minor version, run the "Version Train Bump" workflow (`workflow_dispatch` on `.github/workflows/version-bump.yml`) with the new `major`/`minor` inputs. It runs `strom release set-train` and opens a pull request updating `version.toml` so the change has a normal review and audit trail; merge that PR before the next push to `main` picks up the new train.
- Pull requests targeting `main` automatically get release-candidate agent binaries as workflow artifacts (`.github/workflows/rc.yml`). Add the `release-candidate` label to a PR, or run the workflow manually, to also validate a build of the controller and agent images; those images are never pushed.
- For an out-of-band hotfix or manual validation, you can still push a tag by hand (`git tag -a v0.2.1 -m "..."` then `git push origin v0.2.1`); `release.yml` triggers directly on any `v*` tag push regardless of how the tag was created.
- Repository tag protection should restrict creation of `v*` tags to the GitHub Actions bot/CI identity and block force-pushes or deletions of existing release tags; configure this as a tag protection rule or ruleset in the repository settings.

If you are validating release behavior, prefer an `-rcN` tag first, then promote to a stable tag only after the artifacts and smoke checks look correct.

### Signed Node Update Manifests

Standalone nodes (with no adopting controller) verify GitHub release updates against an Ed25519-signed manifest, independent of the controller-driven OTA route. `release.yml` builds and signs this manifest as part of every release; the signing step fails the workflow if the key is unavailable, so no unsigned manifest can be published.

- The signing private key is stored only as the `STROM_RELEASE_SIGNING_KEY_PEM` repository secret (a PEM-encoded Ed25519 private key). It is never committed to the repository or embedded in any build artifact.
- The corresponding public key is embedded in the agent binary as `defaultReleasePublicKeyHex` in `agent/internal/updates/checker.go`. Changing the signing key requires updating that constant in the same change that rotates the secret, otherwise agents already in the field cannot verify newly signed releases.
- To generate a new keypair, run `uv run strom release generate-signing-key`. Store the printed private key PEM as the `STROM_RELEASE_SIGNING_KEY_PEM` secret and update `defaultReleasePublicKeyHex` with the printed public key hex.
- To build and sign a manifest locally (for validation only, never for a real release), run `uv run strom release agent --version <tag>` followed by `STROM_RELEASE_SIGNING_KEY_PEM="$(cat key.pem)" uv run strom release sign-manifest`.
- Rotating the signing key is a breaking change for any node that has not yet updated past the last release signed with the old key: plan rotations around a stable release that ships the new public key before retiring the old private key.

## Project Links

- [README.md](README.md) for the project overview and current capabilities
- [docs/](docs) for user-facing setup, feature, FAQ, and reference documentation
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community expectations and reporting guidance
- [SECURITY.md](SECURITY.md) for private security vulnerability reporting guidance
- [ROADMAP.md](ROADMAP.md) for phase scope and exit criteria
- [.github/copilot-instructions.md](.github/copilot-instructions.md) for repo-specific Copilot guidance
- [.github/skills/](.github/skills) for validation and Pi-node workflows
- [Bug report template](.github/ISSUE_TEMPLATE/bug_report.md)
- [Feature request template](.github/ISSUE_TEMPLATE/feature_request.md)
- [Pull request template](.github/PULL_REQUEST_TEMPLATE.md)

If you want to improve the templates or refine the code of conduct, please open a PR for it.
