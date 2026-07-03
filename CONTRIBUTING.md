# Contributing to Wattkeeper

Contributors should prefer feature branches and pull requests over pushing directly to `main`.

## Development Workflow

1. Do normal development in branches.
2. Open pull requests so CI validates lint, tests, and cross-builds.
3. Merge to `main` only after CI is green.
4. Create annotated release tags only for versions you actually want published.
5. Use `-rcN` tags to validate release automation before cutting a stable tag.

Working this way should keep routine development well below any realistic limits for the current pipeline, while avoiding unnecessary permanent release artifacts.

## CI Behavior

- Pushes to branches and pull requests run `.github/workflows/ci.yml`.
- CI runs lint, tests, and a cross-build of the agent binaries.
- The CI workflow uses concurrency cancellation, so newer pushes to the same branch or PR cancel older in-progress runs.
- Build artifacts uploaded from CI are temporary and currently retained for 7 days.

## Release Policy

Wattkeeper currently publishes packaged agent release artifacts, not a flashable Raspberry Pi image. Phase 2 image work is still pending, and `make image` is not implemented.

- Only tags matching `v*` run `.github/workflows/release.yml`.
- Stable releases use `vMAJOR.MINOR.PATCH`, for example `v0.2.0`.
- Release candidates use `vMAJOR.MINOR.PATCH-rcN`, for example `v0.2.0-rc1`.
- Other prereleases may use `vMAJOR.MINOR.PATCH-QUALIFIERN`, for example `v0.2.0-beta1`.
- Any tag containing a hyphen is published by GitHub Actions as a prerelease.
- Release candidates should prefer the `-rcN` pattern and advance monotonically: `rc1`, `rc2`, `rc3`, and so on.
- Do not reuse, retag, or force-move an existing release tag. Cut a new prerelease or patch version instead.

## Safe Release Checklist

1. Land changes through a branch and pull request.
2. Wait for CI to pass on the merge commit you intend to release.
3. Create an annotated tag from that commit.
4. Use an `-rcN` tag first when validating packaging or release behavior.
5. Promote to a stable `vMAJOR.MINOR.PATCH` tag only after the prerelease artifacts and smoke checks look correct.

You can build the same release payload locally with:

```sh
make release-agent VERSION=v0.1.0
```

## Limits and Cost Considerations

- The current workflows are lightweight and are unlikely to hit GitHub-hosted runner limits under normal branch and PR development.
- GitHub Actions usage is not unlimited on every plan. Private repositories can consume metered Actions minutes and storage.
- CI artifact storage is controlled by short retention, but GitHub Releases are persistent and will accumulate over time.
- Frequent branch pushes are generally fine, especially with concurrency enabled, but frequent release tags create permanent release assets and should be used more deliberately.
- The first work likely to change the cost profile is Phase 2 image building, since full Raspberry Pi image builds are materially heavier than the current Go binary packaging.
