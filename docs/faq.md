# FAQ

## Is Wattkeeper ready for end users?

Not fully. The node agent, flashable image path, and controller GUI exist today, but the project still needs broader hardware validation and the Home Assistant bridge remains future work.

## Is the Raspberry Pi image an ISO?

No. Wattkeeper ships a Raspberry Pi disk image as a compressed `.img.xz` file.

## Do I need to extract the `.img.xz` before flashing?

No. Raspberry Pi Imager can write the `.img.xz` file directly.

## Which Raspberry Pi models are expected to work?

The current image is built for `arm64`. Pi Zero 2 W is the main target. Other 64-bit-capable Raspberry Pi boards are likely candidates, but they are not all hardware-validated yet.

## Does the image contain my WiFi password or SSH key by default?

No. WiFi and SSH customization are expected to be injected at flash time through Raspberry Pi Imager.

## Will the image ask me to create a username or password on first boot?

No. The image now ships with a pre-created `wattkeeper` account so the Pi boots directly into the Wattkeeper flow without first-user setup prompts.

## Can I log in over SSH with a password?

No. SSH password authentication is disabled. If you want shell access, inject a public key with Raspberry Pi Imager and connect as `wattkeeper`.

## Can I use the current release without a controller?

Yes. The current release is useful as a node image that discovers a UPS, configures NUT locally, and exposes it on the network.

## Does Wattkeeper support Home Assistant now?

Not yet. That integration is planned for a later phase.

## What is the current validation target?

The current practical validation path is: flash the node image, boot a Pi Zero 2 W, attach a USB UPS, confirm mDNS advertisement, and confirm remote `upsc` access.

## How do I factory-reset a node?

Two supported paths:

- Runtime reset: `sudo wattkeeper-agent reset` and restart the service.
- Boot-partition reset: create `wattkeeper-factory-reset` in `/boot/firmware/` (or `/boot/` on older layouts) and boot once.

Both paths clear adoption and controller TLS state. The boot-partition marker path also clears local web auth and persisted UPS naming state so the node returns to first-run bootstrap and pending adoption.
