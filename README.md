# Wattkeeper

Wattkeeper is a distributed UPS monitoring and management system built around a controller/adopt model.

Small Raspberry Pi nodes run NUT near the hardware, automatically detect USB UPS devices, expose them on the network, and advertise themselves for discovery. A central controller discovers those nodes, adopts them, collects metrics, and eventually bridges the fleet into Home Assistant.

## Status

This repository now ships the Phase 1 node agent and the Phase 2 flashable image pipeline. The controller and Home Assistant bridge phases are still ahead.

- [ROADMAP.md](ROADMAP.md) defines the architecture, phases, and exit criteria.
- [.github/copilot-instructions.md](.github/copilot-instructions.md) captures project-specific coding guidance for Copilot sessions in this repo.
- [.github/prompts/](.github/prompts) contains workspace slash-command prompts for each roadmap phase.
- [.github/skills/](.github/skills) contains project-specific Copilot skills for agent validation and Pi-node debugging workflows.

## Goals

- Zero-configuration USB UPS discovery on Raspberry Pi nodes
- NUT-based network exposure with generated configuration
- Centralized discovery, adoption, monitoring, and control
- Home Assistant integration through a controller-side bridge
- A flashable Pi image for simple deployment

## Repo Layout

> [!NOTE]
> Entries tagged `planned` are still future work and are not implemented in the workspace yet.

```text
wattkeeper/
├── ROADMAP.md
├── .github/
│   ├── copilot-instructions.md # repo guidance for Copilot sessions
│   ├── prompts/                # slash-command prompts for roadmap phases
│   ├── skills/                 # project-specific Copilot skills
│   └── workflows/
├── Makefile                    # top-level build/test/image targets
├── agent/                      # Go node agent (runs on the Pi)
│   ├── cmd/agent/
│   └── internal/
│       ├── hotplug/            # udev event watching
│       ├── nutconf/            # nut-scanner parsing + ups.conf generation
│       ├── discovery/          # mDNS advertisement
│       └── api/                # local HTTP API
├── controller/                 # Go backend (Phase 3+)
│   ├── cmd/controller/
│   ├── internal/
│   └── web/                    # planned: React UI
├── image/                      # pi-gen based SD card image build
│   ├── stage-wattkeeper/       # custom pi-gen stage
│   └── config                  # pi-gen config
├── sim/                        # planned: virtual UPS + node simulation
│   ├── dummy-ups/              # planned: NUT dummy-ups fixtures
│   └── docker-compose.yml      # planned: simulated node topology
└── deploy/                     # systemd units, udev rules, install scripts
```

## Development Approach

Work is intended to follow the roadmap phase by phase rather than building the full system up front.

- [x] Phase 0: scaffold the monorepo and CI
- [ ] Phase 1: ship the node agent MVP
- [ ] Phase 2: build a flashable image
- [ ] Phase 3: add the controller, adoption flow, and fleet UI
- [ ] Phase 4: add the Home Assistant bridge

When implementing code in this repository:

- Prefer Go standard library solutions unless a dependency is clearly justified
- Target Go 1.26+ and Raspberry Pi OS Lite (Debian bookworm) for the agent
- Keep generated configs and service artifacts deterministic and testable
- Write table-driven tests for anything that parses or renders text

## How To Use This Repo Today

If you are starting work from scratch:

1. Read [ROADMAP.md](ROADMAP.md) for the intended architecture and constraints.
2. Use the matching slash-command prompt from [.github/prompts/](.github/prompts) when the task lines up with a roadmap phase.
3. Implement only the requested phase unless a small prerequisite is needed to keep the repo buildable.
4. Update [ROADMAP.md](ROADMAP.md) in the same change when roadmap checklist items become fully complete.
5. Use the skills in [.github/skills/](.github/skills) for recurring validation or hardware-debug workflows instead of rewriting those procedures in every session.

## Releases Today

This repository now produces both versioned agent release artifacts and a flashable Raspberry Pi OS Lite image for Wattkeeper nodes:

1. Create and push a SemVer-style tag such as `v0.1.0` for a normal release or `v0.1.0-rc1` for a prerelease.
2. GitHub Actions runs `.github/workflows/release.yml`.
3. The workflow runs tests, builds the agent for `linux/arm64` and `linux/armv6`, packages each archive with the install assets from `deploy/`, builds the `wattkeeper-node-<version>.img.xz` image through pi-gen, and publishes all artifacts to the GitHub Release for that tag.

You can build the same release payload locally with:

```sh
make release-agent VERSION=v0.1.0
make image VERSION=v0.1.0
```

Image build prerequisites and the flash workflow are documented in [image/README.md](image/README.md).

## Likely Compatible Pi Models

The current node image is built as a 64-bit Raspberry Pi OS Lite image for `arm64`, so it is most likely to work on 64-bit-capable Raspberry Pi boards including:

- Pi Zero 2 W
- Pi 3 Model B and B+
- Pi 4 Model B
- Pi 5
- Raspberry Pi 400
- Compute Module 4

The Pi Zero 2 W is the primary validated target today. Other `arm64` Raspberry Pi models should be good candidates, but they are not yet called out as explicitly hardware-validated in this repo.

Older 32-bit-only boards such as the original Pi Zero W are not expected to work with the current image as built.

## Local Validation

When working on the image pipeline or the Pi provisioning flow, validate locally before pushing:

1. Run `make image VERSION=v0.1.0-rc1` from the repo root and wait for `dist/wattkeeper-node-<version>.img.xz` plus its `.sha256` file.
2. If a pi-gen run fails after creating the `pigen_work` container and you want to resume the same build state while iterating on the custom stage, rerun with `CONTINUE=1 make image VERSION=v0.1.0-rc1`.
3. Flash the resulting image with Raspberry Pi Imager, set WiFi and SSH keys in the Imager customization dialog, then boot a Pi Zero 2 W.
4. Plug in a USB UPS and verify the first-boot path end to end: hostname becomes `wkeeper-node-<last4 serial>`, `/var/lib/wattkeeper` exists, the node advertises `_wattkeeper._tcp`, and `upsc <name>@<pi-ip>` works from another machine.
5. Treat CI as the release gate, but use the local path for day-to-day iteration so image and first-boot regressions are caught before tagging or pushing.

## Contributing

Contributor workflow, release policy, RC handling, and GitHub Actions limit guidance live in [CONTRIBUTING.md](CONTRIBUTING.md).
Community expectations and reporting guidance live in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

This project is licensed under the terms in [LICENSE](LICENSE).
