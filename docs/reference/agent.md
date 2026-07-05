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

`POST /api/settings/ui/policy` is the controller-authenticated endpoint used after adoption to apply local UI policy (`managed` + `enabled`) through the same backing auth state used by `/settings`.

`POST /api/agent/update` is the controller-authenticated OTA endpoint for signed agent binary pushes. The node expects `version`, `binary_base64`, `sha256`, and `signature_base64`, verifies the signature against the adopted controller CA certificate, and atomically replaces the local agent binary when verification succeeds.

On an uninitialized node with auth enabled, visiting `/` presents a bootstrap page that creates the local admin account. After bootstrap, `/`, `/status/details`, `/healthz`, and `/settings` require a session cookie created by `POST /auth/login`. For development only, auth can be bypassed with `--http-auth=false`.

To manually reset local web auth on a node, remove `/var/lib/wattkeeper/webui-auth.json` and revisit `/`.

When the controller marks local UI policy as managed for an adopted node, the local settings toggle is locked. Releasing policy from the controller returns control to the node-local admin in `/settings`.

To return an adopted node to pending discovery state, run `sudo wattkeeper-agent reset` and restart the agent service. That clears `/var/lib/wattkeeper/adoption.json` and the node controller API TLS certificate/key so the node advertises `adopted=false` again on the next start.

For offline recovery scenarios, you can also request a factory reset from the boot partition:

1. Power down the node and mount the boot partition.
2. Create an empty marker file named `wattkeeper-factory-reset` at `/boot/firmware/` (or `/boot/` on older layouts).
3. Boot the node.

At startup, the agent consumes that marker and clears:

- `/var/lib/wattkeeper/adoption.json`
- `/var/lib/wattkeeper/node-api.crt`
- `/var/lib/wattkeeper/node-api.key`
- `/var/lib/wattkeeper/names.json`
- `/var/lib/wattkeeper/webui-auth.json`

The node then returns to pending adoption and local web bootstrap state.

When OTA updates are applied successfully, the node reports `restart_required=true` so operations can restart `wattkeeper-agent` to run the new binary.

For local UI and API development away from Pi hardware, run `go run ./agent/cmd/agent --dev-ui --listen :8080` from WSL or another Linux environment. That mode serves sample data and skips hotplug, scanner, and system service integration.

## Discovery Advertisement

The node advertises `_wattkeeper._tcp.local` and includes TXT metadata such as:

- node identifier
- adoption state
- UPS count
- agent version

## Current Deployment Model

The current deployment model is one node near one or more USB UPS devices on the local network. The controller can discover and adopt nodes today; controller-side metrics polling, richer fleet UI, and alerting remain in progress.
