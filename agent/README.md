# Wattkeeper Agent

The agent runs on a Raspberry Pi node, manages local NUT configuration, advertises itself over mDNS, and serves a local node dashboard plus status API on port 80 by default.

By default, the node dashboard and detailed status routes require session-based sign-in with a single local `admin` account, which is auto-provisioned the first time the agent starts (no manual bootstrap step). Only `GET /status` remains public.

## Manual Test Steps

1. Build the agent binary from the repo root:

```sh
uv run wk build agent
```

2. On the Pi, create `/etc/wattkeeper/agent.yaml` with placeholder Phase 1 credentials if it does not already exist:

```yaml
nut:
  username: agent
  password: change-me
```

3. Install the binary and service assets:

```sh
sudo ./deploy/install.sh ./dist/wattkeeper-agent-linux-arm64
```

4. Confirm the service is running:

```sh
systemctl status wattkeeper-agent --no-pager
```

5. Verify the public status API responds with minimal JSON:

```sh
curl http://127.0.0.1/status
```

6. Open the local dashboard in a browser:

```sh
xdg-open http://127.0.0.1/
```

7. Verify the node is advertising on the LAN:

```sh
avahi-browse -rt _wattkeeper._tcp
```

8. Plug in a USB UPS and wait for the scan/reload cycle to complete. Then confirm:

```sh
curl http://127.0.0.1/status
curl http://127.0.0.1/status/details
curl http://127.0.0.1/healthz
upsc <stable-ups-name>@<pi-ip>
```

The public status response should stay minimal: overall node status and UPS count only. The browser dashboard at `/`, `GET /status/details`, and `GET /healthz` carry the richer node details used for local troubleshooting and future authenticated access.

On a fresh node, the `admin` account is created automatically on first start. If `AGENT_ADMIN_PASSWORD` is not set, the account uses a built-in default password and the agent logs a startup warning; the login and settings pages also show a visible security warning until the password is changed (via `/settings`) or `AGENT_ADMIN_PASSWORD` is set. After signing in, `/`, `/status/details`, `/healthz`, and `/settings` use a session cookie unless the process is explicitly started with `--http-auth=false`.

Node-local auth contract:

- Browser HTML form flows (`/auth/login`, `/auth/logout`, `/auth/reset`, `/settings/ui`, `/settings/password`) require a CSRF token (`csrf_token` form field or `X-CSRF-Token` header).
- JSON API clients can log in with `Content-Type: application/json` and then use the returned `wattkeeper_session` cookie for authenticated endpoints (`/status/details`, `/healthz`, and protected `/api/*` routes).
- Session cookies expire after the configured session TTL (12h by default), and a successful login rotates any existing session token.
- Resetting local auth (`/auth/reset`) clears the current admin account and all sessions, then immediately re-provisions the `admin` account (using `AGENT_ADMIN_PASSWORD` if set, otherwise the built-in default) so the node never requires a manual recovery step.
- When requests arrive over TLS (or `X-Forwarded-Proto: https`), auth and CSRF cookies are emitted with `Secure` in addition to `HttpOnly` and `SameSite=Strict`.

The settings page lets the local admin sign out, reset node-local web auth, and toggle the local dashboard on or off.

For adopted nodes, the controller uses the same node-side policy surface (`POST /api/settings/ui/policy`) to manage local UI availability. When the controller has policy management enabled, node-local UI toggles are blocked in settings; when the controller releases policy, the node returns to local admin control. Local reset paths are:

- reset node-local web auth from the settings page (or by removing `/var/lib/wattkeeper/webui-auth.json`) to clear local auth/session state on that node
- run `wattkeeper-agent reset` to return an adopted node to pending state and clear controller adoption material

To return an adopted node to pending discovery state for re-adoption, stop the service and run:

```sh
sudo wattkeeper-agent reset
sudo systemctl restart wattkeeper-agent
```

That removes `/var/lib/wattkeeper/adoption.json` and the node controller API TLS material. On the next start, the agent advertises `adopted=false` again and rewrites runtime NUT credentials from `/etc/wattkeeper/agent.yaml`.

For offline field recovery, you can also create `/boot/firmware/wattkeeper-factory-reset` (or `/boot/wattkeeper-factory-reset` on older layouts) before boot. The agent consumes that marker at startup and clears adoption/TLS material, local auth state, and persisted UPS naming state, returning the node to first-run bootstrap plus pending adoption.

## Local UI/API Development

For UI and API work, you do not need to build and flash a Pi image.

Run the agent in sample-data mode from WSL or another Linux environment:

```sh
uv run wk dev node-ui
```

To override the listen address:

```sh
uv run wk dev node-ui --listen 127.0.0.1:8081
```

To disable auth requirements for local UI iteration only:

```sh
uv run wk dev node-ui-open
```

Container note: the agent entrypoint defaults to `AGENT_HTTP_AUTH=true`.
Set `AGENT_HTTP_AUTH=false` only for explicit local development or simulation
scenarios.

Or use the shorthand target:

```sh
uv run wk dev node-ui-open
```

To clear local web auth state during development without using the settings page:

```sh
rm -f /var/lib/wattkeeper/webui-auth.json
```

That mode skips hotplug, `nut-scanner`, NUT config writes, and systemd reloads. It serves:

- `http://127.0.0.1:8080/` for the node dashboard
- `http://127.0.0.1:8080/status` for minimal public JSON status
- `http://127.0.0.1:8080/status/details` for richer JSON status intended for later authenticated use
- `http://127.0.0.1:8080/healthz` for the legacy detailed health payload
- `http://127.0.0.1:8080/settings` for local node UI settings once signed in

Use normal agent mode on a Pi when you need to validate real device discovery, NUT integration, service reload behavior, or mDNS advertisement.
