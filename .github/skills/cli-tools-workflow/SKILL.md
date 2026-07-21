---
name: cli-tools-workflow
description: 'Use for day-to-day repository operations through the shared strom/uv CLI paths so Copilot and developers exercise the same workflow surface and improvements benefit both.'
argument-hint: 'Command intent (docs, sim, smoke, hooks, release checks)'
user-invocable: true
---

# CLI Tools Workflow

Use this skill whenever a task can be completed through project CLI commands, especially docs, simulation, smoke checks, hooks, and common validation loops.

## Why this skill exists

- Keep Copilot and humans on the same command surface (`strom` and `uv run strom`).
- Ensure UX issues found during agent runs are fixed in the same tooling developers use.
- Reduce one-off ad hoc shell commands that bypass maintained wrappers.

## Preferred command order

1. Use `uv run strom ...` when no environment is activated.
2. Use `strom ...` when the project environment is already active.
3. Use direct underlying commands (`docker compose`, `mkdocs`, etc.) only for troubleshooting or when no wrapper exists yet.

## Common workflows

- Docs:
  - `uv run strom docs`
  - `uv run strom docs build --strict`
- Simulation:
  - `uv run strom sim up --replicas 2`
  - `uv run strom sim ps`
  - `uv run strom sim logs --service strom-controller --tail 200`
  - `uv run strom sim down`
- Scenarios and smoke:
  - `uv run strom sim scenario on_battery --replicas 2`
  - `uv run strom sim smoke --strict --replicas 2`
- Hooks:
  - `uv run strom hooks install`
  - `uv run strom hooks run`

## Maintenance rule

When you need to run a repeated operation and it is not represented in `strom` yet, add or improve the `strom` subcommand first when practical, then use it for validation.

## Completion checks

1. Run the narrowest command set that proves the change.
2. Run `uv run strom hooks run` before finishing code changes.
3. Update `README.md` and/or `CONTRIBUTING.md` if CLI behavior or recommended workflow changed.
