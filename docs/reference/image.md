# Image And Flashing Reference

## Artifact Names

The image build produces artifacts like:

- `wattkeeper-node-v0.1.0.img.xz`
- `wattkeeper-node-v0.1.0.img.xz.sha256`

## Build Command

From the repo root:

```sh
uv run wk image node --version v0.1.0
```

## Checksum Validation

```sh
cd dist
sha256sum -c wattkeeper-node-v0.1.0.img.xz.sha256
```

## Flashing Notes

- use Raspberry Pi Imager
- choose `Use custom`
- select the `.img.xz` artifact directly
- configure WiFi before writing
- optionally configure SSH public-key access for the `wattkeeper` user

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

Wattkeeper adds a first-boot service that:

- suppresses Raspberry Pi OS first-user setup prompts by shipping a pre-created `wattkeeper` account
- locks that account password on first boot so password login is not part of the normal workflow
- sets the hostname to `wkeeper-node-<last4 serial>`
- creates `/var/lib/wattkeeper`
- enables Raspberry Pi OverlayFS for a read-mostly root filesystem
- marks itself complete and disables itself

When OverlayFS is enabled, first boot may include a one-time additional reboot.

To opt out for a specific node, place a `wattkeeper-overlayfs-disable` file on the boot partition before first boot.

## Local Validation

When working on the image pipeline or Pi provisioning flow, the current validation sequence is:

1. Run `uv run wk image node --version v0.1.0-rc1` and wait for the `.img.xz` and `.sha256` artifacts in `dist/`.
2. If you are iterating on the custom pi-gen stage after a failed run, retry with `uv run wk image node --version v0.1.0-rc1 --continue`.
3. Flash the image with Raspberry Pi Imager and apply WiFi customization there. Add SSH public keys only if you want shell access.
4. Boot a Pi Zero 2 W and attach a USB UPS.
5. Verify there is no first-boot username or password prompt, then verify hostname rewrite, `/var/lib/wattkeeper` creation, mDNS advertisement, and remote `upsc` access.
6. Verify OverlayFS is active (`findmnt -n -o FSTYPE /` should report `overlay`) unless you intentionally set `wattkeeper-overlayfs-disable`.

## Security Notes

- no WiFi credentials are baked into source-controlled image artifacts
- no SSH authorized keys are baked into the image by default
- no NUT passwords or controller credentials are baked into the image
- SSH password authentication is disabled; use Raspberry Pi Imager to inject keys for the `wattkeeper` user if you need shell access
