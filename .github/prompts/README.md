# Wattkeeper Prompt Commands

This directory contains workspace Copilot prompt files for roadmap-driven work in the `wattkeeper` repo.

## How to use them

- In Copilot Chat, type `/` and pick the prompt by name.
- You can also open any `*.prompt.md` file in this directory and run it from the editor.
- Before using one, keep [ROADMAP.md](../../ROADMAP.md) in context. The prompts assume roadmap-first execution and require updating completed roadmap checklist items in the same change.

## Available prompts

- `Phase 0 Scaffold`: repo scaffold, workspace wiring, Makefile, lint/test setup, CI stub.
- `Phase 1a Hotplug Scanner`: udev hotplug watcher and `nut-scanner` parsing.
- `Phase 1b Config Services`: NUT config rendering, stable naming, write-if-changed flow, service reload logic.
- `Phase 1c mDNS Health`: mDNS advertisement, health API, deploy assets for the agent.
- `Phase 2 Image`: pi-gen image pipeline and flashable image workflow.
- `Phase 3a Controller Discovery`: controller skeleton, SQLite registry, mDNS discovery, initial frontend.
- `Phase 3b Adoption`: secure node adoption, controller CA, TLS pinning, reset flow.
- `Phase 3c Metrics UI`: metrics polling, alerts, fleet UI, UPS detail flows.
- `Phase 4 Home Assistant`: MQTT and Home Assistant bridge work.
- `Stretch Simulation Rig`: simulated nodes, dummy UPS files, docker-compose rig, smoke coverage.

## When to add or change prompts

- Add a new prompt when a workflow is a repeatable, single-purpose slash command.
- Update existing prompts when roadmap scope changes.
- Keep this directory, [.github/skills/](../skills), [.github/copilot-instructions.md](../copilot-instructions.md), [README.md](../../README.md), and [ROADMAP.md](../../ROADMAP.md) aligned.