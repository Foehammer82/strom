# Image And Flashing Reference

## Artifact Names

The image build produces artifacts like:

- `strom-node-v0.1.0.img.xz`
- `strom-node-v0.1.0.img.xz.sha256`

## Build Command

From the repo root:

```sh
uv run strom image node --version v0.1.0
```

## Checksum Validation

```sh
cd dist
sha256sum -c strom-node-v0.1.0.img.xz.sha256
```

## Flashing Notes

- use Raspberry Pi Imager
- choose `Use custom`
- select the `.img.xz` artifact directly
- configure WiFi before writing
- optionally configure SSH public-key access for the `strom` user

## Compatibility

The current node image is built for `arm64` Raspberry Pi OS Lite.

Likely compatible boards include:

- Pi Zero 2 W
- Pi 3 Model B and B+
- Pi 4 Model B
- Pi 5
- Raspberry Pi 400
- Compute Module 4

Pi Zero 2 W is the primary validated target today.

Older 32-bit-only boards such as the original Pi Zero W are not expected to work with the current image as built.

## First-Boot Behavior

The current image flow relies on Raspberry Pi OS first boot for filesystem expansion and standard Pi Imager customization handling.

Strom adds a first-boot service that:

- suppresses Raspberry Pi OS first-user setup prompts by shipping a pre-created `strom` account
- locks that account password on first boot so password login is not part of the normal workflow
- sets the hostname to `strom-node-<last4 serial>`
- mounts the dedicated `strom-state` ext4 partition at `/var/lib/strom`
- enables Raspberry Pi OverlayFS for a read-mostly root filesystem only after persistent node state is mounted
- marks itself complete and disables itself

The `strom-state` partition preserves local admin credentials, controller trust material, adoption state, and stable UPS names across normal reboots and power loss. When OverlayFS is enabled, first boot may include a one-time additional reboot.

On normal boots, the Strom agent starts local HTTP, USB scanning, and NUT configuration without waiting for WiFi to become online. mDNS registration retries in the background until the network is available, so discovery may follow the local dashboard and NUT service when WiFi is slow to reconnect.

Images built before this change may have enabled Raspberry Pi OverlayFS without the persistent `strom-state` partition. To restore persistence on an existing node until it can be reflashed, run:

```sh
sudo raspi-config nonint do_overlayfs 1
sudo reboot
```

After the reboot, confirm `findmnt -n -o FSTYPE /` does not report `overlay`. Set the local admin password again afterward. If the node had been adopted before the power loss, re-adopt it because its on-node controller trust material may also have been lost.

## Local Validation

When working on the image pipeline or Pi provisioning flow, the current validation sequence is:

1. Run `uv run strom image node --version v0.1.0-rc1` and wait for the `.img.xz` and `.sha256` artifacts in `dist/`.
2. If you are iterating on the custom pi-gen stage after a failed run, retry with `uv run strom image node --version v0.1.0-rc1 --continue`.
3. Flash the image with Raspberry Pi Imager and apply WiFi customization there. Add SSH public keys only if you want shell access.
4. Boot a Pi Zero 2 W and attach a USB UPS.
5. Verify there is no first-boot username or password prompt, then verify hostname rewrite, the `strom-state` mount at `/var/lib/strom`, mDNS advertisement, and remote `upsc` access.
6. Verify `findmnt -n -o FSTYPE /` reports `overlay`, then set the local admin password, reboot or power-cycle the node, and verify the original password signs in rather than returning to the bootstrap page.

## Security Notes

- no WiFi credentials are baked into source-controlled image artifacts
- no SSH authorized keys are baked into the image by default
- no NUT passwords or controller credentials are baked into the image
- SSH password authentication is disabled; use Raspberry Pi Imager to inject keys for the `strom` user if you need shell access
