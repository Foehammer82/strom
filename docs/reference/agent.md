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

The agent listens on port `80` by default. All JSON errors use an `error` field. The API never accepts arbitrary shell commands; diagnostics run a fixed, read-only command set.

### Authentication

`GET /status` is the only public operational endpoint. It returns only aggregate status and UPS count so it is safe for discovery and quick reachability checks.

All local-admin routes require the `strom_session` cookie after bootstrap. JSON clients can create that session with `POST /auth/bootstrap` or `POST /auth/login`; browser form submissions also require a CSRF token. Settings can also issue node-local read and write API keys for integrations, supplied as `Authorization: Bearer <key>`. An adopted controller authenticates with its separate node-specific bearer token. The controller token can read health/diagnostics and perform UPS control; controller-policy and OTA routes require that token specifically.

The read key can access detailed health, diagnostics, and UPS inventory/detail routes. The write key includes every read permission plus UPS commands and writable-variable updates. Local keys never grant adoption, controller policy, or OTA access. Settings requires the current local-admin password to reveal or rotate either key; replacing a key invalidates its predecessor immediately. The pair is stored in `/var/lib/strom/webui-auth.json` with the local admin configuration, which is restricted to the agent owner (`0600`) and removed by local-auth or factory reset.

On an uninitialized node, opening `/` redirects to `/auth/bootstrap`, where the single local `admin` password is created. There is no default password. `--http-auth=false` disables these protections only for local development.

### Endpoint Reference

| Method and path | Access | Description |
| --- | --- | --- |
| `GET /` | Local admin | Node dashboard with live UPS telemetry and controls. |
| `GET /status` | Public | Minimal JSON: aggregate node state and UPS count. |
| `GET /status/details` | Local admin, read/write key, or controller | Detailed node health, inventory, temperature, disk capacity, and UPS summaries. |
| `GET /healthz` | Local admin, read/write key, or controller | Compatibility alias for the detailed health payload. |
| `GET /api/health` | Local admin, read/write key, or controller | Detailed health payload for programmatic clients. |
| `GET /diagnostics` | Local admin | Browser diagnostics page for USB and NUT detection. |
| `GET /api/diagnostics` | Local admin, read/write key, or controller | JSON diagnostic snapshot: `lsusb`, `nut-scanner -U -q`, generated `ups.conf`, `nut-server` state, and the agent inventory. |
| `GET /api/ups` | Local admin, read/write key, or controller | UPS inventory with live summary metrics. |
| `GET /api/ups/{name}` | Local admin, read/write key, or controller | Raw NUT variables, normalized metrics, supported instant commands, and writable variables for one UPS. |
| `POST /api/ups/{name}/command` | Local admin, write key, or controller | Runs an advertised NUT instant command. Body: `{"cmd":"..."}`. |
| `POST /api/ups/{name}/setvar` | Local admin, write key, or controller | Updates an advertised writable NUT variable. Body: `{"var":"...","value":"..."}`. |
| `GET`, `POST /auth/bootstrap` | Public until initialized | Shows or submits first-run local-admin setup. JSON body: `new_password`, `confirm_password`. A successful bootstrap creates a session. |
| `GET`, `POST /auth/login` | Public after bootstrap | Shows or submits local-admin login. JSON body: `username`, `password`. A successful login creates or rotates a session. |
| `POST /auth/logout` | Local admin | Ends the current local-admin session. |
| `POST /auth/reset` | Local admin | Clears local web authentication and returns the node to first-run bootstrap. |
| `GET /settings` | Local admin | Browser settings page for password, API keys, and local UI policy. |
| `POST /settings/api-key` | Local admin + CSRF | Password-confirmed key reveal or generation. JSON body: `scope` (`read` or `write`), `action` (`reveal` or `regenerate`), and `password`. |
| `POST /settings/password` | Local admin | Changes the local-admin password. |
| `POST /settings/ui` | Local admin | Enables or disables the local dashboard when policy is not controller-managed. |
| `POST /adopt` | Pending controller | Stores controller trust material and credentials during one-time adoption. Re-adoption is rejected. |
| `POST /api/settings/ui/policy` | Controller | Applies controller-managed local UI policy. Body: `{"managed":true,"enabled":true}`. |
| `POST /api/agent/update` | Controller | Applies a controller-signed agent update. Body includes `version`, `binary_base64`, `sha256`, and `signature_base64`; success reports `restart_required=true`. |
| `GET /api/agent/updates/status` | Local admin, read/write key | Reports the installed version, any pending activation, and the last checked/available GitHub release. |
| `POST /api/agent/updates/check` | Local admin, write key | Polls GitHub releases for a newer signed manifest and reports the result; does not install anything. |
| `POST /api/agent/updates/install` | Local admin, write key | Downloads, verifies, and stages a specific signed release version, then restarts the agent to activate it. Body: `{"version":"vX.Y.Z"}`. |

The diagnostics endpoints are the first place to investigate a USB UPS that does not appear in the dashboard: an entry in `lsusb` but not `nut-scanner` points to NUT driver support or scanner behavior, while scanner output with no generated `ups.conf` points to agent configuration/reload behavior.

To manually reset local web auth on a node, remove `/var/lib/strom/webui-auth.json` and revisit `/`; the node returns to the bootstrap flow and any local API keys are invalidated.

When the controller marks local UI policy as managed for an adopted node, the local settings toggle is locked. Releasing policy from the controller returns control to the node-local admin in `/settings`.

To return an adopted node to pending discovery state, run `sudo strom-agent reset` and restart the agent service. That clears `/var/lib/strom/adoption.json` and the node controller API TLS certificate/key so the node advertises `adopted=false` again on the next start.

For offline recovery scenarios, you can also request a factory reset from the boot partition:

1. Power down the node and mount the boot partition.
2. Create an empty marker file named `strom-factory-reset` at `/boot/firmware/` (or `/boot/` on older layouts).
3. Boot the node.

At startup, the agent consumes that marker and clears:

- `/var/lib/strom/adoption.json`
- `/var/lib/strom/node-api.crt`
- `/var/lib/strom/node-api.key`
- `/var/lib/strom/names.json`
- `/var/lib/strom/webui-auth.json`

The node then returns to pending adoption and local web bootstrap state.

When OTA updates are applied successfully, the node reports `restart_required=true` so operations can restart `strom-agent` to run the new binary.

For local UI and API development away from Pi hardware, run `go run ./agent/cmd/agent --dev-ui --listen :8080` from WSL or another Linux environment. That mode serves sample data and skips hotplug, scanner, and system service integration.

## Standalone Signed Updates

A node with no adopting controller can still check for and install software updates on its own, by polling GitHub releases for the `strom` repository. This is independent of the controller-driven `POST /api/agent/update` OTA route above, and does not require adoption.

- A daily `strom-update-check.timer` (`systemd`, notify-only, randomized up to 6 hours) runs `strom-agent update check`, which fetches the newest stable GitHub release, verifies its manifest's Ed25519 signature against a public key built into the agent binary, and records the result for the dashboard's "Software updates" section. No update is installed automatically.
- An operator installs an available update from the node dashboard's Settings page, or by running `strom-agent update install <version>`. The agent downloads the matching release archive, verifies both the manifest signature and the archive/binary checksums, and stages the new binary under `/var/lib/strom/agent/<version>/` before atomically repointing the `current` symlink and restarting.
- `/usr/local/bin/strom-agent` is a small launcher script, not the agent binary itself: it execs whichever release the `current` symlink points to, falling back to a read-only recovery copy of the agent at `/usr/local/libexec/strom-agent-recovery` if the active release is missing or not executable. This means a corrupted or broken update can never leave a node with no working agent binary to boot into.
- After activating a new release, the agent polls its own `/healthz` for up to two minutes. If it does not become healthy in that window, the next restart automatically rolls back the `current` symlink to the previously installed release.
- Release manifests (`strom-agent-manifest.json` + detached `strom-agent-manifest.json.sig`) are generated and signed by `strom release agent`/`strom release sign-manifest` in CI, using a private key stored only as the `STROM_RELEASE_SIGNING_KEY_PEM` repository secret; the release workflow refuses to publish if that secret is missing. See `CONTRIBUTING.md` for the signing-key generation and rotation procedure.

## Discovery Advertisement

The node advertises `_strom._tcp.local` and includes TXT metadata such as:

- node identifier
- adoption state
- UPS count
- agent version

## Current Deployment Model

The current deployment model is one node near one or more USB UPS devices on the local network. The controller can discover and adopt nodes today; controller-side metrics polling, richer fleet UI, and alerting remain in progress.
