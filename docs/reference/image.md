# Image And Flashing Reference

## Artifact Names

The image build produces artifacts like:

- `wattkeeper-node-v0.1.0.img.xz`
- `wattkeeper-node-v0.1.0.img.xz.sha256`

## Build Command

From the repo root:

```sh
make image VERSION=v0.1.0
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
- configure WiFi and SSH before writing

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

- sets the hostname to `wkeeper-node-<last4 serial>`
- creates `/var/lib/wattkeeper`
- marks itself complete and disables itself

## Local Validation

When working on the image pipeline or Pi provisioning flow, the current validation sequence is:

1. Run `make image VERSION=v0.1.0-rc1` and wait for the `.img.xz` and `.sha256` artifacts in `dist/`.
2. If you are iterating on the custom pi-gen stage after a failed run, retry with `CONTINUE=1 make image VERSION=v0.1.0-rc1`.
3. Flash the image with Raspberry Pi Imager and apply WiFi and SSH customization there.
4. Boot a Pi Zero 2 W and attach a USB UPS.
5. Verify hostname rewrite, `/var/lib/wattkeeper` creation, mDNS advertisement, and remote `upsc` access.

## Security Notes

- no WiFi credentials are baked into source-controlled image artifacts
- no SSH authorized keys are baked into the image by default
- no NUT passwords or controller credentials are baked into the image
