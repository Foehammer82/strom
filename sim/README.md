# Wattkeeper Simulation Rig

The simulation rig runs containerized Wattkeeper services with fake UPS fixtures.

Default topology:
- N replicas of `wattkeeper-agent` (2 by default)
- One `wattkeeper-controller`
- One Mosquitto broker
- Optional Home Assistant service behind the `ha` compose profile

## Usage

From the repository root:

```sh
uv run wk sim up --replicas 2
uv run wk sim scenario ci-smoke --replicas 2
uv run wk sim scenario on_battery --replicas 2
uv run wk sim scenario restore --replicas 2
uv run wk sim scenario node_loss --replicas 2
uv run wk sim down
```

`ci-smoke` validates both the MQTT bridge and the controller aggregate NUT listener
(`:3493`) by exercising auth, `LIST UPS`, `LIST CMD`, `GET CMDDESC`, `GET VAR`,
and `INSTCMD` against the simulated adopted fleet.

Enable Home Assistant in the same stack:

```sh
docker compose -f sim/docker-compose.yml --profile ha up -d --build --scale wattkeeper-agent=2
```

## mDNS caveat

mDNS discovery can be inconsistent on Docker bridge networks depending on host platform/firewall behavior. If pending nodes do not appear in the controller, use host networking for the node/controller containers or an mDNS reflector sidecar in the test environment.
