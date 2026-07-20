# Wattkeeper Copilot Instructions

This repository is the `wattkeeper` monorepo. Read `ROADMAP.md` before making non-trivial changes so the current phase, architecture, and intended repo layout stay aligned.

## Copilot workflow

- Workspace slash-command prompts for roadmap work live in `.github/prompts/`. When a task maps to one of those phases, prefer the matching prompt file over ad hoc phase prompts.
- Project-specific reusable workflows live in `.github/skills/`. Use those for repeatable validation and Raspberry Pi debugging tasks instead of re-deriving the procedure each time.
- For operational command execution (docs, simulation, smoke, hooks, and recurring repo tasks), use the `cli-tools-workflow` skill and prefer `uv run wk ...` (or `wk ...` in an activated environment) so Copilot exercises the same tooling users rely on.
- Treat `CONTRIBUTING.md` as a required companion to this file: follow the contributor workflow, validation expectations, and naming conventions documented there for all non-trivial changes.
- Before starting roadmap-driven work, review the relevant checklist items in `ROADMAP.md`. As items become fully complete, update `ROADMAP.md` in the same change and check off only the items that are actually done.
- As roadmap-driven work changes what ships today, how releases are produced, or how operators should understand the current product surface, update `README.md` in the same change so it stays accurate as implementation progresses.
- If you change the repo's prompt or skill workflow, keep `.github/prompts/`, `.github/skills/`, `README.md`, `ROADMAP.md`, and `CONTRIBUTING.md` aligned during roadmap-driven development. Once Phase 10 is complete and `ROADMAP.md` is deleted, refresh `CONTRIBUTING.md` and any remaining guidance that still assumes roadmap-first execution.
- Do not reintroduce `PROMPTS.md`.
- Keep versioning and release automation aligned with the current GitHub Actions tag pattern: stable releases use `vMAJOR.MINOR.PATCH`, prereleases use `vMAJOR.MINOR.PATCH-QUALIFIER` such as `v0.1.0-rc1` or `v0.1.0-beta1`.
- Preserve the release workflow conventions documented in `README.md`: prefer annotated tags, use `-rcN` tags to validate prereleases, and never reuse or force-move an existing release tag.

## Project defaults

- Primary languages: Go for `agent/` and `controller/`; React + TypeScript only when Phase 3 frontend work begins.
- Target runtime for the agent: Go 1.26+ on Raspberry Pi OS Lite (Debian bookworm), especially Pi Zero 2 W (`linux/arm64`, with `linux/arm` fallback where the roadmap calls for it).
- Prefer the Go standard library unless an external dependency clearly reduces risk or complexity. If adding a dependency, keep it small and justify it in code comments, docs, or the PR summary.
- Keep generated configs, install assets, systemd units, udev rules, and image/deployment artifacts in `deploy/` or under the phase-specific rendering/output path described in `ROADMAP.md`.

## Architecture and scope

- Treat this as a phased build. Implement only the requested phase unless a small prerequisite is required to keep the code buildable or testable.
- Preserve the monorepo structure from `ROADMAP.md`: shared top-level build/test automation, with separate Go modules in `agent/` and `controller/` coordinated by `go.work`.
- The agent is the hardware-facing node process. The controller is the central discovery/adoption/metrics service. Do not blur those responsibilities.
- Favor simple, explicit designs over framework-heavy abstractions. This project is intended to be operable on small devices and easy to debug remotely.

## Implementation guidance

- Keep Linux and Debian bookworm behavior in mind for any service management, file paths, systemd integration, or NUT interactions.
- Avoid cgo unless the roadmap explicitly justifies it. Pure Go solutions are preferred, especially for agent-side hardware/event handling.
- Use stable, deterministic rendering for generated text files so config diffs are predictable and easy to test.
- Keep the docs visual theme aligned with the app's current UI direction. When node or controller UI colors, tone, or surface styling change materially, update the MkDocs theme/assets in the same change or note the mismatch explicitly.
- When implementing parsers or text renderers, write table-driven tests with realistic fixtures. This is required for scanner parsing, config generation, protocol parsing, and similar text-heavy behavior.
- For hardware-dependent code, isolate shelling out, filesystem access, clocks, and service control behind small interfaces when practical so tests can fake them.
- When a feature interacts with NUT, udev, mDNS, SQLite, MQTT, or TLS, follow the roadmap's explicit constraints before inventing new behavior.

## Build and verification

- Keep `uv run wk check test` and the relevant build command working after each change.
- Prefer focused unit tests first, then repo-level verification using the top-level `wk` CLI commands.
- Before you finish any code-changing task, run `uv run wk hooks run` (or the equivalent `pre-commit run --all-files`) and make sure it passes; if it fails, fix the hooks or the affected files before stopping.
- When release automation or packaging changes, preserve the current version injection and tag-driven workflow behavior in the `wk` CLI and `.github/workflows/` files unless the task explicitly changes the release strategy.
- When asked to help with releases, default to the safe sequence: verify the target commit is merged and green, cut an annotated `-rcN` tag first when validating automation, then cut a stable SemVer tag only after the prerelease result is acceptable.
- For Go code, keep APIs small, use clear package boundaries under `internal/`, and avoid speculative abstractions for later phases.
- For frontend work, treat the controller as a full React + TypeScript GUI app and keep it directly aligned with the controller API, roadmap milestones, and the shared Wattkeeper visual language already established by the node UI.

## What to avoid

- Do not add cloud-only assumptions, desktop-only dependencies, or workflows that conflict with Raspberry Pi deployment.
- Do not bake secrets, Wi-Fi credentials, or controller-provisioned credentials into source-controlled files, images, or generated artifacts.
- Do not silently change the versioning pattern, release tag rules, or prerelease semantics without updating the workflows and the release guidance in `README.md`.
- Do not implement stretch goals or future-phase behavior unless the current task explicitly asks for it.
