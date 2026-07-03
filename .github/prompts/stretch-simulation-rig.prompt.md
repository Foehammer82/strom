---
name: "Stretch Simulation Rig"
description: "Use when implementing Wattkeeper stretch simulation rig work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the stretch checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Build the `sim/` virtual test rig per the stretch section in [ROADMAP.md](../../ROADMAP.md). Keep it lightweight.

- Add `--simulate <dir>` to the agent: bypass `nut-scanner` and udev, treat each `*.dev` file as a detected UPS, and watch the directory with `fsnotify` so file drops simulate hotplug.
- Add two sample `.dev` files under `sim/dummy-ups/` modeled on a Back-UPS BE1050G3.
- Build `sim/docker-compose.yml` with two simulated nodes, one controller, and Mosquitto. Document the host-networking caveat for mDNS.
- Add scenario scripts under `sim/scenarios/` to simulate on-battery, restore, and node-loss cases.
- Extend the `Makefile` with `sim-up`, `sim-down`, and `sim-scenario NAME=on_battery`.
- Add a CI smoke job that brings up the rig, asserts two pending nodes, adopts them, and verifies metrics flow.