---
name: pi-node-debug
description: 'Use for Raspberry Pi node bring-up and field debugging: hotplug events, generated NUT config, systemd state, agent health checks, mDNS visibility, and remote troubleshooting on Wattkeeper nodes.'
argument-hint: 'Symptom, Pi host, or subsystem to debug'
user-invocable: true
---

# Pi Node Debug

Use this skill when debugging Wattkeeper on Raspberry Pi hardware or when reasoning about the deploy/image path before writing code for it.

## When to Use

- A Pi node does not detect a UPS after hotplug.
- Generated NUT config or service restarts do not behave as expected.
- A node is not discoverable, not healthy, or not serving UPS state correctly.
- You need a repeatable field-debug flow for future deploy and image work.

## Procedure

1. Read the relevant Phase 1 or Phase 2 checklist items in [ROADMAP.md](../../../ROADMAP.md) before deciding whether a bug is in current scope.
2. Confirm the local agent state first:
   - inspect agent logs
   - inspect generated files under `/etc/nut/`
   - inspect the stable name map under `/var/lib/wattkeeper/`
3. Check systemd state in this order:
   - `systemctl status wattkeeper-agent`
   - `systemctl status nut-server`
   - `systemctl status 'nut-driver@*'`
   - `journalctl -u wattkeeper-agent -u nut-server --no-pager -n 100`
4. Check USB and NUT detection:
   - `nut-scanner -U -q`
   - `upsc <ups-name>@localhost`
   - confirm serial, vendor, and fallback identity fields are present enough for naming
5. If the bug concerns discovery or API behavior once those features exist, then check:
   - `curl http://<node>:8080/healthz`
   - `avahi-browse _wattkeeper._tcp -r`
6. Compare the observed behavior with the roadmap exit criteria before deciding whether this is a defect, missing implementation, or out-of-scope expectation.

## Notes

- Keep Linux and Debian bookworm packaging behavior in mind when reading systemd and NUT state.
- Separate missing deploy assets from runtime bugs in already-implemented packages.
- If a step depends on mDNS, health, or deploy artifacts that do not yet exist in the repo, call that out explicitly instead of papering over the gap.
