# Features

This page separates what Wattkeeper ships today from what is still planned.

## Available Today

### Node Agent

- detects USB UPS devices on Raspberry Pi nodes
- runs `nut-scanner` and parses discovered UPS metadata
- generates deterministic NUT configuration
- restarts or reloads NUT services only when generated config changes
- advertises the node over mDNS as `_wattkeeper._tcp`
- exposes a local health endpoint on port `8080`

### Flashable Node Image

- builds a Raspberry Pi OS Lite image for `arm64`
- includes the Wattkeeper agent, service units, and udev rules
- supports Raspberry Pi Imager WiFi and SSH customization
- runs a first-boot service to set the node hostname and create runtime state

## Planned Features

### Controller

- fleet discovery and adoption
- SQLite-backed node registry
- live UPS metrics collection
- web UI for fleet management and health

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

- there is no shipped controller application yet
- there is no end-user web UI yet
- Home Assistant integration is not yet available
- multi-node management is still future work