# Wattkeeper Skills

This directory contains project-specific Copilot skills for recurring workflows that are too procedural for always-on instructions and too reusable for one-off prompts.

## Available skills

- `nut-agent-validation`: repeatable Phase 1 validation for scanner parsing, config rendering, stable naming, service reload behavior, and focused Go test selection.
- `pi-node-debug`: Raspberry Pi node bring-up and field-debug flow for hotplug, generated NUT config, systemd, `upsc`, health, and mDNS troubleshooting.
- `cli-tools-workflow`: shared CLI workflow for `wk` and `uv run wk` so Copilot and developers use the same operational tooling.
- `mui-development`: reference conventions for building or migrating `controller/web` to Material UI (MUI) — reference docs, stack assumptions, theming, component preferences, do/don't list, and accessibility baseline.

## How to use them

- In Copilot Chat, type `/` and select the skill by name.
- Skills are also auto-discoverable when the task description matches their workflow.
- Keep [ROADMAP.md](../../ROADMAP.md) in context when using either skill so roadmap scope and checklist updates stay accurate.
