---
name: "Phase 2 Image"
description: "Use when implementing Wattkeeper roadmap Phase 2 flashable image work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the Phase 2 checklist in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Build the flashable image pipeline in `image/` per [ROADMAP.md](../../ROADMAP.md) Phase 2.

1. Use pi-gen (`github.com/RPi-Distro/pi-gen`) via its Docker build. Add `image/config`: `IMG_NAME=wattkeeper-node`, `arm64`, only stages 0-2 plus the custom stage, `ENABLE_SSH=1`.
2. Create `image/stage-wattkeeper/` with substages that:
   - install `nut` and `avahi-daemon` via apt
   - copy the cross-compiled agent binary from `dist/` into `/usr/local/bin/wattkeeper-agent`, plus systemd units and udev rules from `deploy/`, then enable the agent service
   - disable bluetooth and HDMI via `config.txt` tweaks for power savings while leaving WiFi power management at defaults
   - run a first-boot oneshot that sets hostname to `wkeeper-node-<last4 serial>`, creates `/var/lib/wattkeeper`, then disables itself
3. Rely on Raspberry Pi Imager's standard customization for WiFi and SSH keys, and verify the image does not clobber `firstrun.sh` handling. Document the flash procedure in `image/README.md`.
4. Add a `make image` target that depends on `make agent`, runs the pi-gen Docker build, and outputs `dist/wattkeeper-node-<version>.img.xz`. Document the x86_64 Linux host requirement with Docker and `binfmt/qemu-user-static`.
5. Extend GitHub Actions so tags build the image and attach it to the release.

Do not bake credentials, WiFi config, or NUT passwords into the image.