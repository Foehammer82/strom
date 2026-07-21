# Strom Image Build

This directory contains the Raspberry Pi OS Lite image pipeline for Strom nodes.

## Host Requirements

Builds are supported on x86_64 Linux hosts with:

- Docker
- a Linux kernel with `binfmt_misc` available
- permission to run privileged helper containers for `arm64` binfmt registration
- outbound network access to clone `github.com/RPi-Distro/pi-gen`

The build runs pi-gen through its Docker wrapper, but pi-gen still depends on host kernel features for arm64 chroots. The Strom wrapper tries to keep local setup minimal:

- if `qemu-aarch64` or `qemu-aarch64-static` already exists on the host, it reuses it
- otherwise it extracts a temporary `qemu-aarch64` helper from `multiarch/qemu-user-static`
- if `qemu-aarch64` is not registered in `binfmt_misc`, it first tries `docker run --privileged --rm tonistiigi/binfmt --install arm64`

That means a normal local build can often succeed with Docker alone, as long as the host kernel exposes `binfmt_misc` and allows privileged containers.

## Build

From the repo root:

```sh
uv run strom image node --version v0.1.0
```

That target:

1. cross-compiles the agent with `uv run strom build agent --version v0.1.0`
2. clones the `bookworm-arm64` pi-gen branch into a temporary workspace without spaces in the path
3. injects the Strom custom stage plus the `dist/strom-agent-linux-arm64` payload and deploy assets
4. runs `build-docker.sh`
5. copies the resulting image to `dist/strom-node-v0.1.0.img.xz`

The target also writes `dist/strom-node-v0.1.0.img.xz.sha256`.

## User Documentation

The user-facing flow for building, flashing, and validating a node image now lives in the docs set:

- [docs/getting-started.md](../docs/getting-started.md) for the current build-and-flash path
- [docs/reference/image.md](../docs/reference/image.md) for image artifacts, compatibility, first-boot behavior, and validation notes
- [docs/faq.md](../docs/faq.md) for common image and hardware questions

## First-Boot Implementation

The image relies on Raspberry Pi OS first boot for filesystem expansion. Strom adds a separate oneshot service that:

- suppresses the Raspberry Pi OS first-user creation flow by shipping a pre-created `strom` account
- locks that account password on first boot so password login is not part of the standard workflow
- sets the hostname to `strom-node-<last4 serial>`
- creates `/var/lib/strom`
- records completion and disables itself

This service does not replace or overwrite Raspberry Pi Imager's boot partition customization flow. WiFi settings, SSH key injection, and other standard Imager data still flow through the usual `firstrun.sh` handling provided by Raspberry Pi OS.

The image adds a `strom-state` ext4 partition and mounts it at `/var/lib/strom`. This preserves local admin credentials, adoption state, controller TLS material, and stable UPS names across restarts and power loss. After that mount is available, first boot enables Raspberry Pi OverlayFS to keep ordinary root filesystem writes in RAM and reduce SD card wear; this may cause one additional reboot.

On later boots, `strom-agent` does not wait for WiFi to become online before it starts its local HTTP, USB scanning, and NUT setup. Its mDNS advertisement retries until networking is available, so it can become discoverable shortly after WiFi reconnects.

Images built before this change may have enabled Raspberry Pi OverlayFS without the `strom-state` partition. To restore persistence on an existing node until it can be reflashed, run `sudo raspi-config nonint do_overlayfs 1`, reboot, and confirm `findmnt -n -o FSTYPE /` no longer reports `overlay`. Set the local admin password again afterward. If the node had been adopted before the power loss, re-adopt it because its on-node controller trust material may also have been lost.

## Security Constraints

- No WiFi credentials are baked into the image.
- No SSH authorized keys are baked into the image.
- No NUT passwords or controller credentials are baked into the image.
- SSH is enabled in the base image, but password authentication is disabled; use Pi Imager to inject public keys for the `strom` user if you need shell access. After the node-local `admin` dashboard account has been configured, an operator can explicitly enable password SSH from node Settings. That creates a sudo-capable Linux `admin` account and uses the same password as the dashboard. Disabling SSH access or resetting the local dashboard auth revokes this password-login path.
