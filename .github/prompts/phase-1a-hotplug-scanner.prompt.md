---
name: "Phase 1a Hotplug Scanner"
description: "Use when implementing Wattkeeper roadmap Phase 1a hotplug watcher and NUT scanner work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 1 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Implement `agent/internal/hotplug` and the scanner half of `agent/internal/nutconf` per [ROADMAP.md](../../ROADMAP.md) Phase 1.

Hotplug package:
- Watch udev events via netlink (kernel uevent socket, no libudev cgo, pure Go). Filter to `SUBSYSTEM=usb`, `ACTION add/remove`.
- Debounce: UPS enumeration fires multiple events; coalesce into a single `bus changed` notification if no further events arrive for 3s (configurable). Expose as a channel.
- Also emit one synthetic event on startup so boot-time detection uses the same code path.

NUT scanner package:
- `Scan()` runs `nut-scanner -U -q` (path configurable for tests), parses the ini-like output into `[]DetectedUPS{Driver, Port, VendorID, ProductID, Product, Serial, Vendor, Bus}`.
- Handle zero devices, multiple devices, missing serial (fall back to a `bus+vendorid+productid` composite key and log a warning), and `nut-scanner` nonzero exit.
- Add table-driven tests with several realistic captured `nut-scanner` fixtures (for example APC Back-UPS devices with vendor ID `051d`).

Wire it into `main.go`: on each debounced hotplug event, run `Scan()` and log the diff versus the previous scan (added and removed UPSes). No config writing yet.