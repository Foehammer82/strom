# Wattkeeper Image Build

This directory contains the Raspberry Pi OS Lite image pipeline for Wattkeeper nodes.

## Host Requirements

Builds are supported on x86_64 Linux hosts with:

- Docker
- a Linux kernel with `binfmt_misc` available
- permission to run privileged helper containers for `arm64` binfmt registration
- outbound network access to clone `github.com/RPi-Distro/pi-gen`

The build runs pi-gen through its Docker wrapper, but pi-gen still depends on host kernel features for arm64 chroots. The Wattkeeper wrapper tries to keep local setup minimal:

- if `qemu-aarch64` or `qemu-aarch64-static` already exists on the host, it reuses it
- otherwise it extracts a temporary `qemu-aarch64` helper from `multiarch/qemu-user-static`
- if `qemu-aarch64` is not registered in `binfmt_misc`, it first tries `docker run --privileged --rm tonistiigi/binfmt --install arm64`

That means a normal local build can often succeed with Docker alone, as long as the host kernel exposes `binfmt_misc` and allows privileged containers.

## Build

From the repo root:

```sh
uv run wk image node --version v0.1.0
```

That target:

1. cross-compiles the agent with `uv run wk build agent --version v0.1.0`
2. clones the `bookworm-arm64` pi-gen branch into a temporary workspace without spaces in the path
3. injects the Wattkeeper custom stage plus the `dist/wattkeeper-agent-linux-arm64` payload and deploy assets
4. runs `build-docker.sh`
5. copies the resulting image to `dist/wattkeeper-node-v0.1.0.img.xz`

The target also writes `dist/wattkeeper-node-v0.1.0.img.xz.sha256`.

## User Documentation

The user-facing flow for building, flashing, and validating a node image now lives in the docs set:

- [docs/getting-started.md](../docs/getting-started.md) for the current build-and-flash path
- [docs/reference/image.md](../docs/reference/image.md) for image artifacts, compatibility, first-boot behavior, and validation notes
- [docs/faq.md](../docs/faq.md) for common image and hardware questions

## First-Boot Implementation

The image relies on Raspberry Pi OS first boot for filesystem expansion. Wattkeeper adds a separate oneshot service that:

- suppresses the Raspberry Pi OS first-user creation flow by shipping a pre-created `wattkeeper` account
- locks that account password on first boot so password login is not part of the standard workflow
- sets the hostname to `wkeeper-node-<last4 serial>`
- creates `/var/lib/wattkeeper`
- records completion and disables itself

This service does not replace or overwrite Raspberry Pi Imager's boot partition customization flow. WiFi settings, SSH key injection, and other standard Imager data still flow through the usual `firstrun.sh` handling provided by Raspberry Pi OS.

The first-boot script also enables Raspberry Pi OverlayFS by default through `raspi-config nonint do_overlayfs 0` and triggers a one-time reboot when that change is applied. This gives nodes a read-mostly root filesystem that is more resilient to SD card wear.

If you need writable-root behavior for a particular deployment, create `wattkeeper-overlayfs-disable` on the boot partition before first boot (for example `/boot/firmware/wattkeeper-overlayfs-disable` once mounted on Linux).

## Security Constraints

- No WiFi credentials are baked into the image.
- No SSH authorized keys are baked into the image.
- No NUT passwords or controller credentials are baked into the image.
- SSH is enabled in the base image, but password authentication is disabled; use Pi Imager to inject public keys for the `wattkeeper` user if you need shell access.
