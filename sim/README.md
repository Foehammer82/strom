# Strom Simulation Rig

The simulation rig runs containerized Strom services with fake UPS fixtures.

Default topology:
- N replicas of `strom-agent` (2 by default)
- One `strom-controller`
- One Mosquitto broker
- Optional Home Assistant service behind the `ha` compose profile

Each agent replica publishes its web UI (`:80` in-container) to a localhost-only
host port in the `18080-18179` range.

## Usage

From the repository root:

```sh
uv run strom sim up --replicas 2
uv run strom sim scenario ci-smoke --replicas 2
uv run strom sim scenario on_battery --replicas 2
uv run strom sim scenario restore --replicas 2
uv run strom sim scenario node_loss --replicas 2
uv run strom sim scenario multi_ups --replicas 2
uv run strom sim down
```

Find the current host port mapping for each agent replica:

```sh
uv run strom sim ps
# or
docker compose -f sim/docker-compose.yml ps strom-agent
```

Then open each mapped host port in your browser, for example:
- `http://127.0.0.1:18080`
- `http://127.0.0.1:18081`

`ci-smoke` validates both the MQTT bridge and the controller aggregate NUT listener
(`:3493`) by exercising auth, `LIST UPS`, `LIST CMD`, `GET CMDDESC`, `GET VAR`,
and `INSTCMD` against the simulated adopted fleet.

Enable Home Assistant in the same stack:

```sh
docker compose -f sim/docker-compose.yml --profile ha up -d --build --scale strom-agent=2
```

## mDNS caveat

mDNS discovery can be inconsistent on Docker bridge networks depending on host platform/firewall behavior. If pending nodes do not appear in the controller, use host networking for the node/controller containers or an mDNS reflector sidecar in the test environment.
