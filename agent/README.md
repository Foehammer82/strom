# Wattkeeper Agent

The agent runs on a Raspberry Pi node, manages local NUT configuration, advertises itself over mDNS, and serves a local node dashboard plus status API on port 80 by default.

By default, the node dashboard and detailed status routes require a first-run bootstrap to create a local admin account, followed by session-based sign-in in the browser. Only `GET /status` remains public.

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

On a fresh node, the first browser visit to `/` prompts for local admin creation. After that, `/`, `/status/details`, `/healthz`, and `/settings` use a session cookie set by the sign-in flow unless the process is explicitly started with `--http-auth=false`.

The settings page lets the local admin sign out, reset node-local web auth, and toggle the local dashboard on or off. That toggle is the current node-side hook for the future controller-managed UI policy.

To return an adopted node to pending discovery state for re-adoption, stop the service and run:

```sh
sudo wattkeeper-agent reset
sudo systemctl restart wattkeeper-agent
```

That removes `/var/lib/wattkeeper/adoption.json` and the node controller API TLS material. On the next start, the agent advertises `adopted=false` again and rewrites runtime NUT credentials from `/etc/wattkeeper/agent.yaml`.

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
