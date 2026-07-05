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
- supports boot-partition factory reset markers for offline node recovery

### Controller

- discovers pending and adopted nodes over mDNS
- adopts nodes with pinned TLS trust and encrypted stored credentials
- persists node metadata such as display name and location labels
- polls adopted-node NUT telemetry into SQLite on an interval
- exposes recent UPS summaries, per-UPS detail/history APIs, and trusted instant commands
- derives battery runtime-decay trends and replacement estimates from stored UPS runtime history in the UPS detail view
- evaluates webhook alert rules for on-battery, low-battery, node-offline, and comms-lost conditions
- serves a GUI-driven React fleet interface with fleet, node, UPS, and alerts views
- can enable or disable each adopted node local web UI from the controller fleet card
- can release controller local UI policy so node-local admin control is restored
- can push signed agent OTA payloads to adopted nodes via the trusted node API
- re-serves adopted UPS inventory through a controller aggregate NUT listener on `:3493`
  with protocol support for `LIST UPS`, `LIST VAR`, `GET VAR`, `LIST CMD`,
  `GET CMDDESC`, and `INSTCMD`
- provides a controller settings UI control to enable or disable the aggregate
  NUT listener without restarting the controller
- supports controller database backup and restore command paths

### Home Assistant Bridge

- publishes retained MQTT discovery and state payloads for adopted UPS entities
- publishes controller and per-node availability topics using MQTT LWT semantics
- bridges Home Assistant button command topics to trusted UPS instant commands
- documents setup and operator troubleshooting in [Home Assistant Setup](home-assistant.md)

## Planned Features

### Lifecycle And Hardening

- more long-term operational hardening

## Compatibility Notes

- the current image target is `arm64`
- Pi Zero 2 W is the primary validated target
- older 32-bit-only boards such as the original Pi Zero W are not expected to work with the current image

## Current Limitations

- Home Assistant functionality still benefits from real-environment validation and long-run broker/HA soak testing
- Phase 3 still needs real-hardware validation against its exit criteria
- MQTT alert delivery is not yet part of the shipped Home Assistant bridge scope
