# FAQ

## Is Wattkeeper ready for end users?

Not fully. The node agent and flashable image path exist today, but the controller, fleet UI, and Home Assistant bridge are still planned.

## Is the Raspberry Pi image an ISO?

No. Wattkeeper ships a Raspberry Pi disk image as a compressed `.img.xz` file.

## Do I need to extract the `.img.xz` before flashing?

No. Raspberry Pi Imager can write the `.img.xz` file directly.

## Which Raspberry Pi models are expected to work?

The current image is built for `arm64`. Pi Zero 2 W is the main target. Other 64-bit-capable Raspberry Pi boards are likely candidates, but they are not all hardware-validated yet.

## Does the image contain my WiFi password or SSH key by default?

No. WiFi and SSH customization are expected to be injected at flash time through Raspberry Pi Imager.

## Can I use the current release without a controller?

Yes. The current release is useful as a node image that discovers a UPS, configures NUT locally, and exposes it on the network.

## Does Wattkeeper support Home Assistant now?

Not yet. That integration is planned for a later phase.

## What is the current validation target?

The current practical validation path is: flash the node image, boot a Pi Zero 2 W, attach a USB UPS, confirm mDNS advertisement, and confirm remote `upsc` access.