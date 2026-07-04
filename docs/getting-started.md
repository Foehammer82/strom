# Getting Started

This guide covers the current supported path for trying Wattkeeper on a Raspberry Pi node.

## What You Need

- a supported Raspberry Pi, preferably a Pi Zero 2 W
- a microSD card
- a USB UPS to attach to the node
- network access for the Pi
- Raspberry Pi Imager
- either a downloaded release image or a locally built image artifact

## Supported Image Artifact

Wattkeeper currently ships a compressed Raspberry Pi disk image in `.img.xz` format. It is not an ISO.

If you build locally, the expected outputs are:

- `dist/wattkeeper-node-<version>.img.xz`
- `dist/wattkeeper-node-<version>.img.xz.sha256`

## Option 1: Download A Release Image

1. Open the GitHub Releases page for Wattkeeper.
2. Download the `wattkeeper-node-<version>.img.xz` artifact.
3. Download the matching `.sha256` file if you want to verify the image before flashing.

## Option 2: Build The Image Locally

From the repo root:

```sh
make image VERSION=v0.1.0-rc1
```

When the build completes, verify that the `.img.xz` and `.sha256` files exist in `dist/`.

## Verify The Checksum

Optional but recommended:

```sh
cd dist
sha256sum -c wattkeeper-node-v0.1.0-rc1.img.xz.sha256
```

You should see `OK`.

## Flash The SD Card

!!! info "Need Raspberry Pi Imager?"
   Download it from [raspberrypi.com/software/](https://www.raspberrypi.com/software/).

1. Open Raspberry Pi Imager.
2. Choose `Use custom`.
3. Select `wattkeeper-node-<version>.img.xz`.
4. Select the target SD card.
5. Open the Imager customization dialog.
6. Configure:
   - WiFi SSID, password, and country
   - SSH enabled with at least one public key
   - key-based authentication
7. Write the image to the SD card.

Raspberry Pi Imager can write the compressed `.img.xz` directly. Do not unpack it first unless you have a separate reason to do that.

## First Boot

1. Insert the SD card into the Pi.
2. Boot the Pi and allow first boot to finish.
3. Attach the UPS over USB.
4. Wait for the node to settle on the network.

Expected first-boot behavior:

- the filesystem expands
- the hostname becomes `wkeeper-node-<last4 serial>`
- `/var/lib/wattkeeper` is created
- the agent starts automatically

## Validate The Node

After boot:

- verify the node is reachable on your LAN
- verify `_wattkeeper._tcp` is advertised
- verify the UPS appears through NUT from another machine with `upsc <ups-name>@<pi-ip>`

## What Comes Next

Today, Wattkeeper is node-first. There is not yet a shipped controller UI or adoption flow. The current value is automatic local UPS discovery and a flashable node image.
