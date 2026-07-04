---
name: cli-tools-workflow
description: 'Use for day-to-day repository operations through the shared wk/uv CLI paths so Copilot and developers exercise the same workflow surface and improvements benefit both.'
argument-hint: 'Command intent (docs, sim, smoke, hooks, release checks)'
user-invocable: true
---

# CLI Tools Workflow

Use this skill whenever a task can be completed through project CLI commands, especially docs, simulation, smoke checks, hooks, and common validation loops.

## Why this skill exists

- Keep Copilot and humans on the same command surface (`wk` and `uv run wk`).
- Ensure UX issues found during agent runs are fixed in the same tooling developers use.
- Reduce one-off ad hoc shell commands that bypass maintained wrappers.

## Preferred command order

1. Use `uv run wk ...` when no environment is activated.
2. Use `wk ...` when the project environment is already active.
3. Use direct underlying commands (`docker compose`, `mkdocs`, etc.) only for troubleshooting or when no wrapper exists yet.

## Common workflows

- Docs:
  - `uv run wk docs`
  - `uv run wk docs build --strict`
- Simulation:
  - `uv run wk sim up --replicas 2`
  - `uv run wk sim ps`
  - `uv run wk sim logs --service wattkeeper-controller --tail 200`
  - `uv run wk sim down`
- Scenarios and smoke:
  - `uv run wk sim scenario on_battery --replicas 2`
  - `uv run wk sim smoke --strict --replicas 2`
- Hooks:
  - `uv run wk hooks install`
  - `uv run wk hooks run`

## Maintenance rule

When you need to run a repeated operation and it is not represented in `wk` yet, add or improve the `wk` subcommand first when practical, then use it for validation.

## Completion checks

1. Run the narrowest command set that proves the change.
2. Run `uv run wk hooks run` before finishing code changes.
3. Update `README.md` and/or `CONTRIBUTING.md` if CLI behavior or recommended workflow changed.
