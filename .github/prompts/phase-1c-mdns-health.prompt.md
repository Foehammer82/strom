---
name: "Phase 1c mDNS Health"
description: "Use when implementing Wattkeeper roadmap Phase 1c mDNS advertisement and agent health endpoint work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 1 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Implement `agent/internal/discovery` and `agent/internal/api` per [ROADMAP.md](../../ROADMAP.md).

Discovery:
- Advertise `_wattkeeper._tcp` on port 8080 using `github.com/grandcat/zeroconf` (or justify an alternative).
- Instance name: `wkeeper-node-<last 4 of Pi serial>`.
- Read the Pi serial from `/proc/cpuinfo` (`Serial` line) or `/sys/firmware/devicetree/base/serial-number`; fall back to machine ID.
- TXT records: `id=<serial>`, `adopted=false`, `ups_count=<n>`, `version=<agent version>` set via ldflags.
- Re-announce when `ups_count` changes.

API:
- HTTP server on `--listen` address.
- `GET /healthz` returns JSON with agent version, uptime, Pi serial, CPU temperature, disk free on `/`, and the list of UPSes with name, driver, and status.
- UPS status should shell out to `upsc <name>` and parse `ups.status`, tolerating driver-starting states.
- Structure handlers so an `/adopt` endpoint fits later (router, logging middleware, JSON error helper).

Wire it into `main.go`. Update `deploy/` with the agent systemd unit (`After=network-online.target nut-server.service`, `Restart=on-failure`) and an `install.sh` that copies the binary and units, then enables them. Document manual test steps in `agent/README.md`.