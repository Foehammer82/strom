# Wattkeeper

Wattkeeper is a distributed UPS monitoring and management system built around a controller/adopt model.

Small Raspberry Pi nodes run NUT near the hardware, automatically detect USB UPS devices, expose them on the network, and advertise themselves for discovery. A central controller discovers those nodes, adopts them, collects metrics, and eventually bridges the fleet into Home Assistant.

## Status

This repository is currently in the planning and scaffolding stage.

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

## Planned Architecture

The repo is intended to become a Go monorepo with these main areas:

- `agent/`: Raspberry Pi node agent that detects UPS devices, manages NUT config, advertises via mDNS, and exposes a local API
- `controller/`: Central backend and web UI for discovery, adoption, metrics, and fleet management
- `deploy/`: Systemd units, install scripts, and related deployment assets
- `image/`: Pi image build pipeline based on pi-gen
- `sim/`: Optional simulation rig for end-to-end testing without hardware

The authoritative planned layout and behavior live in [ROADMAP.md](ROADMAP.md).

## Development Approach

Work is intended to follow the roadmap phase by phase rather than building the full system up front.

- Phase 0: scaffold the monorepo and CI
- Phase 1: ship the node agent MVP
- Phase 2: build a flashable image
- Phase 3: add the controller, adoption flow, and fleet UI
- Phase 4: add the Home Assistant bridge

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

## License

This project is licensed under the terms in [LICENSE](LICENSE).