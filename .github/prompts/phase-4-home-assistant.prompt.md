---
name: "Phase 4 Home Assistant"
description: "Use when implementing Wattkeeper roadmap Phase 4 Home Assistant bridge work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the Phase 4 checklist in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Implement the Home Assistant bridge per [ROADMAP.md](../../ROADMAP.md) Phase 4.

- Add `internal/mqtt` using `eclipse/paho.golang`: connect from config and publish HA MQTT discovery for each adopted UPS.
- Publish sensors for battery charge, UPS load, battery runtime, input voltage, and UPS status, plus binary sensors for on-battery, low-battery, and online.
- Publish buttons for the whitelisted instcmds and group entities under stable HA devices.
- Reuse the existing 15s poll loop for state publishing, publishing on change plus a 5-minute heartbeat, with retained config and state and an LWT for controller availability.
- Use per-node availability so a single offline node only affects its UPSes.
- Optionally expose an aggregate controller-side NUT server for the subset of protocol features Home Assistant needs.
- Write `docs/home-assistant.md` covering broker setup, what appears automatically, and example automations.