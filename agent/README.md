# Wattkeeper Agent

The agent runs on a Raspberry Pi node, manages local NUT configuration, advertises itself over mDNS, and serves a local health API on port 8080 by default.

## Manual Test Steps

1. Build the agent binary from the repo root:

```sh
make agent
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

5. Verify the health endpoint responds with JSON:

```sh
curl http://127.0.0.1:8080/healthz
```

6. Verify the node is advertising on the LAN:

```sh
avahi-browse -rt _wattkeeper._tcp
```

7. Plug in a USB UPS and wait for the scan/reload cycle to complete. Then confirm:

```sh
curl http://127.0.0.1:8080/healthz
upsc <stable-ups-name>@<pi-ip>
```

The health response should include the stable UPS name, driver, and status, and the mDNS TXT record should update `ups_count` when devices are added or removed.