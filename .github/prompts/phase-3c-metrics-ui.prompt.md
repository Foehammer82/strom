---
name: "Phase 3c Metrics UI"
description: "Use when implementing Wattkeeper roadmap Phase 3c metrics polling and fleet UI work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 3 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Finish controller Phase 3 per [ROADMAP.md](../../ROADMAP.md).

Backend:
- `internal/nutpoll`: for every adopted node, maintain a NUT client connection, poll all UPS variables every 15s, write samples, retain 30 days, reconnect with backoff, and mark nodes offline after 3 missed polls.
- `POST /api/ups/{id}/command {cmd}`: pass through whitelisted `INSTCMD` values and return the NUT response verbatim.
- Alert engine: rules table for `on_battery`, `low_battery`, `node_offline`, and `comms_lost`, firing webhooks with debounce.
- `GET /api/ups/{id}/history?var=battery.charge&hours=24` for charts.

Frontend:
- Fleet grid with one card per UPS showing status, charge, load, runtime, and node location, plus a pending-adoption banner.
- UPS detail page with 24h charts using `recharts`, instcmd buttons with confirm dialogs, and rename support.
- Node detail page with CPU temperature, disk, agent version, its UPSes, and a forget-node action.