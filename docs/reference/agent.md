# Agent Reference

## Purpose

The agent is the hardware-facing process that runs on a Raspberry Pi node near the UPS.

## Current Responsibilities

- detect USB UPS attach and remove events
- run `nut-scanner` to discover supported UPS devices
- render NUT configuration under `/etc/nut`
- manage relevant NUT services when generated config changes
- advertise the node over mDNS
- expose a local health endpoint

## Health Endpoint

The agent exposes `GET /healthz` on port `8080`.

The health response is intended to describe the current node state, including version and UPS-related status.

## Discovery Advertisement

The node advertises `_wattkeeper._tcp.local` and includes TXT metadata such as:

- node identifier
- adoption state
- UPS count
- agent version

## Current Deployment Model

The current deployment model is one node near one or more USB UPS devices on the local network. The controller-side adoption workflow is not yet shipped.