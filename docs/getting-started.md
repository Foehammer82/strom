# Getting Started

## What You Need

- a supported Raspberry Pi, preferably a Pi Zero 2 W
- a microSD card
- a USB UPS to attach to the node
- network access for the Pi
- [Raspberry Pi Imager](https://www.raspberrypi.com/software/)

## 1. Get The Image

Download the `strom-node-<version>.img.xz` artifact from the [GitHub Releases page](https://github.com/Foehammer82/strom/releases) for Strom.

Prefer to build it yourself instead? From the repo root:

```sh
uv run strom image node --version v0.1.0-rc1
```

This produces `dist/strom-node-<version>.img.xz` and a `.sha256` file you can verify with `sha256sum -c`.

## 2. Flash The SD Card

1. Open Raspberry Pi Imager and choose `Use custom`.
2. Select the `.img.xz` file (do not unpack it first) and your SD card.
3. Open the customization dialog and set:
    - WiFi SSID, password, and country
    - optionally, SSH with a public key for the `strom` user
4. Write the image.

## 3. Boot And Connect

1. Insert the SD card, power on the Pi, and let first boot finish (it may reboot once more automatically).
2. Attach the UPS over USB.
3. Once the node is on your network, open `http://<pi-ip>/` in a browser.

The hostname defaults to `strom-node-<last4 serial>`.

## 4. Set The Admin Password

The first time you open the node, you're prompted to set a password for the single local `admin` account. There is no built-in default password — you choose one during this first-run step, then sign in with it going forward.

## Validate The Node

- `http://<pi-ip>/` prompts to set the admin password on first visit, then loads the sign-in page and dashboard after signing in
- `http://<pi-ip>/status` returns the minimal public node status payload
- `avahi-browse -rt _strom._tcp` (run from another Linux machine) shows the node advertised over mDNS
- `upsc <ups-name>@<pi-ip>` (run from another machine) shows the attached UPS through NUT

## What Comes Next

Strom also has a controller for discovering and adopting nodes, reviewing fleet telemetry, and managing alerts across multiple nodes. The controller is a regular Linux service, not tied to Pi hardware, so pick whichever install method fits:

## Other Deployment Options

### Run With Docker

```sh
docker run -d --name strom-controller \
  -p 9000:9000 \
  -v strom-controller-data:/data \
  ghcr.io/foehammer82/strom-controller:latest
```

Or use the compose file included in the repo, which builds the image from source instead of pulling it:

```sh
docker compose -f deploy/docker-compose.controller.yml up -d --build
```

### Build From Source

From the repo root:

```sh
uv run strom build controller-web
uv run strom dev controller
```

Then open `http://127.0.0.1:9000/` to reach the controller interface.
