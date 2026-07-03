---
name: "Phase 1b Config Services"
description: "Use when implementing Wattkeeper roadmap Phase 1b config generation and service management. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 1 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Extend `agent/internal/nutconf` with config rendering and add `agent/internal/services`.

NUT config rendering:
- `RenderUPSConf([]DetectedUPS) string`: one section per UPS. Section name = stable name derived from serial: `ups-` + lowercase alphanumeric serial, truncated to 20 chars. On collision, append `-2`, `-3`, and so on. Persist a name map to `/var/lib/wattkeeper/names.json` so a UPS keeps its name across reboots and port moves even if serial is missing.
- Render `nut.conf` (`MODE=netserver`), `upsd.conf` (`LISTEN 0.0.0.0 3493`, plus `LISTEN ::`), and `upsd.users` from a struct. Phase 1 uses a single user from `/etc/wattkeeper/agent.yaml` with a plaintext password. Mark that with a TODO noting it will be replaced by controller provisioning in Phase 3.
- `WriteIfChanged(path, content)`: write atomically (temp file + rename), return whether content changed (compare hash), use mode `0640`, owner `root:nut`.

Services package:
- `Reload(changed bool, upsNames []string)` using `systemctl`: if `ups.conf` changed, restart `nut-driver-enumerator` or `nut-driver@<name>` units as appropriate for Debian bookworm's NUT packaging, then reload-or-restart `nut-server`.
- Detect and log failures with `journalctl` hints. Shell out to `systemctl` behind an interface so tests can fake it.

Wire the full loop in `main.go`: hotplug event to scan to render to write to reload if changed. Add an integration-style test that runs the loop against a fake scanner and fake `systemctl`, asserting configs land correctly and no reload happens when nothing changed.