# Features

This page separates what Wattkeeper ships today from what is still planned.

## Available Today

### Node Agent

- detects USB UPS devices on Raspberry Pi nodes
- runs `nut-scanner` and parses discovered UPS metadata
- generates deterministic NUT configuration
- restarts or reloads NUT services only when generated config changes
- advertises the node over mDNS as `_wattkeeper._tcp`
- exposes a local node dashboard on port `80`
- serves a minimal public JSON node status at `/status`
- serves detailed node JSON at `/status/details`
- preserves `/healthz` as a detailed compatibility endpoint
- requires first-run bootstrap and session-based local auth for the dashboard and detailed endpoints unless auth is explicitly disabled for development
- includes a local settings surface for sign-out, auth reset, and node UI enable/disable

### Flashable Node Image

- builds a Raspberry Pi OS Lite image for `arm64`
- includes the Wattkeeper agent, service units, and udev rules
- supports Raspberry Pi Imager WiFi and SSH customization
- runs a first-boot service to set the node hostname and create runtime state

### Controller

- discovers pending and adopted nodes over mDNS
- adopts nodes with pinned TLS trust and encrypted stored credentials
- persists node metadata such as display name and location labels
- polls adopted-node NUT telemetry into SQLite on an interval
- exposes recent UPS summaries, per-UPS detail/history APIs, and trusted instant commands
- evaluates webhook alert rules for on-battery, low-battery, node-offline, and comms-lost conditions
- serves a GUI-driven React fleet interface with fleet, node, UPS, and alerts views

## Planned Features

### Home Assistant Bridge

- MQTT publishing
- Home Assistant discovery entities
- UPS diagnostic and control integration

### Lifecycle And Hardening

- OTA updates
- backup and restore flows
- node reset and recovery paths
- more long-term operational hardening

## Compatibility Notes

- the current image target is `arm64`
- Pi Zero 2 W is the primary validated target
- older 32-bit-only boards such as the original Pi Zero W are not expected to work with the current image

## Current Limitations

- Home Assistant integration is not yet available
- Phase 3 still needs real-hardware validation against its exit criteria
- MQTT alert delivery is deferred to the Home Assistant bridge phase
