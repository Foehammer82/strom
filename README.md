# Wattkeeper

Wattkeeper is a distributed UPS monitoring and management system built around a controller/adopt model.

Small Raspberry Pi nodes run NUT near the hardware, automatically detect USB UPS devices, expose them on the network, serve a per-node web dashboard and status API, and advertise themselves for discovery. A central controller discovers those nodes, adopts them, collects metrics, and eventually bridges the fleet into Home Assistant.

## Status

This repository now ships the Phase 1 node agent, the Phase 2 flashable image pipeline, and a substantial Phase 3 controller foundation.

Today that means:

- Raspberry Pi nodes auto-detect USB UPS hardware, generate NUT configuration, advertise themselves over mDNS, and expose a branded local dashboard with live telemetry and UPS control actions.
- The image pipeline builds a flashable Raspberry Pi OS Lite image for node deployment.
- The controller can discover nodes, persist them in SQLite, adopt pending nodes, forget stale node records, store controller-managed node display/location metadata, establish node-local trust material, poll adopted-node NUT variables into SQLite, expose recent UPS telemetry plus per-UPS detail/history APIs and trusted controller-side UPS commands, evaluate webhook alert rules, serve a branded React controller GUI, publish Home Assistant MQTT discovery/state payloads when configured with a broker, and re-serve adopted UPSes through an aggregate NUT listener on `:3493` that can be enabled or disabled from controller settings. Adopted nodes can be returned to pending state with `wattkeeper-agent reset` on the node.

What is still ahead:

- Phase 3 hardware validation against the real-node exit criteria
- Final Phase 4 validation against a real Home Assistant instance and broker
- Later lifecycle and hardening work from the roadmap

- [ROADMAP.md](ROADMAP.md) defines the architecture, phases, and exit criteria.
- [docs/](docs) contains the user-facing documentation set, including getting started, features, FAQ, and operational reference material.
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
├── wk                          # top-level developer CLI entrypoint
├── agent/                      # Go node agent (runs on the Pi)
│   ├── cmd/agent/
│   └── internal/
│       ├── hotplug/            # udev event watching
│       ├── nutconf/            # nut-scanner parsing + ups.conf generation
│       ├── discovery/          # mDNS advertisement
│       └── api/                # local HTTP API
├── controller/                 # Go backend + React controller GUI (Phase 3)
│   ├── cmd/controller/         # controller entrypoint + embedded frontend assets
│   └── internal/               # browse, CA, registry, secure store
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
- [x] Phase 1: ship the node agent MVP
- [x] Phase 2: build a flashable image
- [ ] Phase 3: add the controller, adoption flow, and fleet UI
- [x] Phase 4: add the Home Assistant bridge

Phase 3 implementation is now present in the repository: discovery, adoption,
controller packaging, polling/history, alerting, and the React controller GUI.
The remaining Phase 3 risk is hardware validation against the roadmap exit
criteria.

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

This repository now produces versioned agent release artifacts, a flashable Raspberry Pi OS Lite image for Wattkeeper nodes, and a published multi-arch controller container image:

1. Create and push a SemVer-style tag such as `v0.1.0` for a normal release or `v0.1.0-rc1` for a prerelease.
2. GitHub Actions runs `.github/workflows/release.yml`.
3. The workflow runs tests, builds the agent for `linux/arm64` and `linux/armv6`, packages each archive with the install assets from `deploy/`, builds the `wattkeeper-node-<version>.img.xz` image through pi-gen, and publishes those artifacts to the GitHub Release for that tag.
4. The same tag workflow also builds and pushes the multi-arch controller image to `ghcr.io/<owner>/wattkeeper-controller` for `linux/amd64` and `linux/arm64`.

You can build the same release payload locally with:

```sh
uv run wk release agent --version v0.1.0
uv run wk image node --version v0.1.0
uv run wk image controller --version v0.1.0
```

For local controller iteration, use:

```sh
uv run wk build controller
uv run wk dev controller
```

Image build prerequisites and the flash workflow are documented in [image/README.md](image/README.md).

The user-facing documentation set lives in [docs/](docs). If you are looking for setup steps, product capabilities, or operational reference material, start there.

Local docs and repo tooling are managed with `uv`. Use `uv run wk docs setup`, `uv run wk docs build`, or `uv run wk docs serve` from the repo root after installing `uv`, and use `uv run wk hooks install` or `uv run wk hooks run` for repo hygiene checks.

For the newer project CLI flow, run `wk` from an activated virtual environment, or use `uv run wk` without activating one. Current commands include:

- `wk docs`, `wk docs serve`, `wk docs build`
- `wk sim up|down|ps|logs`
- `wk sim smoke [--strict]`
- `wk sim scenario <name>` (`ci-smoke`, `on_battery`, `restore`, `node_loss`)
- `wk hooks install|run`
- `wk build ...`, `wk dev ...`, `wk check ...`, `wk image ...`, `wk release ...`

Examples:

```sh
uv run wk docs
uv run wk sim smoke --strict --replicas 2
uv run wk sim up --replicas 2
wk sim logs --service wattkeeper-controller --tail 200
wk hooks run
```

For the current user-facing path, start with:

- [docs/getting-started.md](docs/getting-started.md) for building, flashing, and validating a node image
- [docs/features.md](docs/features.md) for what ships today versus what is still planned
- [docs/faq.md](docs/faq.md) for common operator questions
- [docs/reference/image.md](docs/reference/image.md) for image artifact, checksum, compatibility, and first-boot reference details

## Contributing

Contributor workflow, release policy, RC handling, and GitHub Actions limit guidance live in [CONTRIBUTING.md](CONTRIBUTING.md).
Community expectations and reporting guidance live in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
Security vulnerability reporting guidance lives in [SECURITY.md](SECURITY.md).

## License

This project is licensed under the terms in [LICENSE](LICENSE).
