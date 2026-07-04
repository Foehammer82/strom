---
name: "Stretch Simulation Rig"
description: "Use when implementing Wattkeeper stretch simulation rig work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the stretch checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Build the `sim/` virtual test rig per the stretch section in [ROADMAP.md](../../ROADMAP.md). Keep it lightweight and container-first.

- Add and publish a Docker container image for the agent (multi-arch release artifact, similar to the controller image).
- Add `--simulate <dir>` to the agent: bypass `nut-scanner` and udev, treat each `*.dev` file as a detected UPS, and watch the directory with `fsnotify` so file drops/edits simulate hotplug.
- Add deterministic `--demo-mode` behavior for evaluation workflows, with state changes driven by scenario scripts.
- Add sample `.dev` fixtures under `sim/dummy-ups/` modeled on a Back-UPS BE1050G3.
- Build `sim/docker-compose.yml` with configurable agent replica count (default 2), one controller, Mosquitto, and optional Home Assistant via Compose profile. Document the host-networking caveat for mDNS.
- Add scenario scripts under `sim/scenarios/` to simulate on-battery, restore, and node-loss cases.
- Extend the `wk` CLI with `sim up`, `sim down`, and `scenario on_battery`, supporting replica overrides.
- Add a CI smoke job that brings up the rig, asserts two pending nodes, adopts them, runs a scenario, and verifies metrics flow through MQTT topics.
