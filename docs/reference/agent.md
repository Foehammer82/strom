# Agent Reference

## Purpose

The agent is the hardware-facing process that runs on a Raspberry Pi node near the UPS.

## Current Responsibilities

- detect USB UPS attach and remove events
- run `nut-scanner` to discover supported UPS devices
- render NUT configuration under `/etc/nut`
- manage relevant NUT services when generated config changes
- advertise the node over mDNS
- expose a local node dashboard and status API

## HTTP Endpoints

The agent exposes a node dashboard at `/` plus JSON endpoints at `GET /status` and `GET /healthz` on port `80` by default.

`GET /status` is the public, minimal response intended for quick checks and discovery-oriented state.

`GET /status/details` is the richer node status payload intended for the local UI and a later authenticated audience.

`GET /healthz` is the richer compatibility health payload and includes the same class of detailed node state.

`GET /settings` exposes the signed-in settings surface for local node UI policy, sign-out, and auth reset.

On an uninitialized node with auth enabled, visiting `/` presents a bootstrap page that creates the local admin account. After bootstrap, `/`, `/status/details`, `/healthz`, and `/settings` require a session cookie created by `POST /auth/login`. For development only, auth can be bypassed with `--http-auth=false`.

To manually reset local web auth on a node, remove `/var/lib/wattkeeper/webui-auth.json` and revisit `/`.

To return an adopted node to pending discovery state, run `sudo wattkeeper-agent reset` and restart the agent service. That clears `/var/lib/wattkeeper/adoption.json` and the node controller API TLS certificate/key so the node advertises `adopted=false` again on the next start.

For local UI and API development away from Pi hardware, run `go run ./agent/cmd/agent --dev-ui --listen :8080` from WSL or another Linux environment. That mode serves sample data and skips hotplug, scanner, and system service integration.

## Discovery Advertisement

The node advertises `_wattkeeper._tcp.local` and includes TXT metadata such as:

- node identifier
- adoption state
- UPS count
- agent version

## Current Deployment Model

The current deployment model is one node near one or more USB UPS devices on the local network. The controller can discover and adopt nodes today; controller-side metrics polling, richer fleet UI, and alerting remain in progress.
