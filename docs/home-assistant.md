# Home Assistant Setup

This guide explains how to connect Strom controller telemetry and controls to Home Assistant over MQTT.

## What You Get Automatically

For each adopted UPS, the controller publishes Home Assistant discovery entities that include:

- sensors: battery charge, UPS load, battery runtime, input voltage, and UPS status
- binary sensors: on battery, low battery, and online connectivity
- button entities for supported instant commands exposed by the node

Entities are grouped under stable per-node devices so multiple UPSes on one node stay organized.

## Prerequisites

- a running MQTT broker reachable by the controller
- a running Strom controller with adopted nodes
- Home Assistant with MQTT integration enabled

## Configure The Controller MQTT Bridge

Start the controller with MQTT options:

```sh
strom-controller \
  --mqtt-broker mqtt://mqtt.local:1883 \
  --mqtt-username homeassistant \
  --mqtt-password '<broker-password>' \
  --mqtt-discovery-prefix homeassistant \
  --mqtt-state-prefix strom
```

Notes:

- `--mqtt-broker` is required to enable publishing.
- `--mqtt-discovery-prefix` defaults to `homeassistant`.
- `--mqtt-state-prefix` defaults to `strom`.
- Published discovery and state messages are retained.
- The controller publishes availability using MQTT Last Will and Testament (LWT).

## Configure Aggregate NUT Listener (Optional)

If you also want native NUT client compatibility from the controller endpoint:

1. Open the controller Settings page.
2. Enable Aggregate NUT listener.
3. Set listen address (default `:3493`) and save.

You can disable it from the same page at any time without restarting the
controller.

## Home Assistant MQTT Integration

1. In Home Assistant, open Settings -> Devices and Services.
2. Add MQTT integration if it is not already configured.
3. Confirm Home Assistant can connect to the same broker used by Strom.
4. Wait for discovery. New Strom devices and entities should appear automatically.

## Topic Model (Reference)

State and availability topics are published under the configured state prefix:

- `strom/controller/availability`
- `strom/nodes/<node-id>/availability`
- `strom/nodes/<node-id>/ups/<ups-name>/state`
- `strom/nodes/<node-id>/ups/<ups-name>/command`

Discovery topics are published under the configured discovery prefix, for example:

- `homeassistant/sensor/.../config`
- `homeassistant/binary_sensor/.../config`
- `homeassistant/button/.../config`

## Command Buttons

Home Assistant button entities publish the command payload to each UPS command topic.
The controller subscribes to those topics and forwards supported commands to the trusted node UPS command API.

If a command is not currently supported for the target UPS, it is ignored.

## Example Automations

### Notify When A UPS Goes On Battery

```yaml
alias: Notify UPS on battery
triggers:
  - trigger: state
    entity_id: binary_sensor.rack_ups_on_battery
    to: "on"
actions:
  - action: notify.mobile_app_phone
    data:
      title: "Power event"
      message: "Rack UPS is on battery power."
mode: single
```

### Run A UPS Self Test Button

```yaml
alias: Run weekly UPS quick test
triggers:
  - trigger: time
    at: "03:30:00"
conditions:
  - condition: time
    weekday:
      - sun
actions:
  - action: button.press
    target:
      entity_id: button.rack_ups_test_battery_start_quick
mode: single
```

## Troubleshooting

- No entities discovered:
  - verify controller was started with `--mqtt-broker`
  - verify Home Assistant and controller use the same MQTT broker
  - verify broker credentials for both services
- Entities unavailable:
  - verify `controller/availability` and node availability topics are `online`
  - verify adopted nodes are still reachable and polling
- Command button does nothing:
  - verify the UPS advertises that command in the controller UPS detail view
  - verify the node is adopted and trusted controller-to-node API calls are healthy

## Current Scope

- This guide covers MQTT discovery/state/button workflows.
- The controller now also provides aggregate NUT server mode on `:3493` with
  global enable/disable control from the controller Settings page.
