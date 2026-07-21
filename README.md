# Strom

Strom is a distributed UPS monitoring and management system built around a controller/adopt model.

Small Raspberry Pi nodes run NUT near the hardware, automatically detect USB UPS devices, expose them on the network, serve a per-node web dashboard and status API, and advertise themselves for discovery. A central controller discovers those nodes, adopts them, collects metrics, and eventually bridges the fleet into Home Assistant.

## Status

This repository now ships the Phase 1 node agent, the Phase 2 flashable image pipeline, and a substantial Phase 3 controller foundation.

Today that means:

- Raspberry Pi nodes auto-detect USB UPS hardware, generate NUT configuration, advertise themselves over mDNS, and expose a branded local dashboard with live telemetry and UPS control actions.
- The image pipeline builds a flashable Raspberry Pi OS Lite image for node deployment.
- The node image first-boot path now enables Raspberry Pi OverlayFS by default (read-mostly rootfs) to reduce SD card wear, with an opt-out marker file on the boot partition for operators who need writable-root behavior.
- Nodes now support operator-driven factory reset through `strom-agent reset` or a boot-partition `strom-factory-reset` marker file for offline recovery.
- The controller can discover nodes, persist them in SQLite, adopt pending nodes, forget stale node records, store controller-managed node display/location metadata, establish node-local trust material, poll adopted-node NUT variables into SQLite, expose recent UPS telemetry plus per-UPS detail/history APIs and trusted controller-side UPS commands, evaluate webhook alert rules, serve a branded React controller GUI, publish Home Assistant MQTT discovery/state payloads when configured with a broker, and re-serve adopted UPSes through an aggregate NUT listener on `:3493` that can be enabled or disabled from controller settings. Adopted nodes can be returned to pending state with `strom-agent reset` on the node.
- The controller UPS detail surface now derives a battery runtime-decay trend and replacement estimate from stored historical `battery.runtime` samples gathered during healthy, high-charge periods.

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
strom/
├── ROADMAP.md
├── .github/
│   ├── copilot-instructions.md # repo guidance for Copilot sessions
│   ├── prompts/                # slash-command prompts for roadmap phases
│   ├── skills/                 # project-specific Copilot skills
│   └── workflows/
├── strom                          # top-level developer CLI entrypoint
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
│   ├── stage-strom/       # custom pi-gen stage
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

This repository now produces versioned agent release artifacts, a flashable Raspberry Pi OS Lite image for Strom nodes, and a published multi-arch controller container image. Release tagging is automated for normal merges, while major/minor version choices and prerelease validation stay under maintainer control:

1. `.github/release/version.toml` is the single source of truth for the active release train (`major`/`minor`) and RC build behavior. Only maintainers change it, via the manual "Version Train Bump" GitHub Actions workflow (`workflow_dispatch`), which opens a reviewed pull request rather than committing directly.
2. Every push to `main` (other than docs-only changes, or a commit whose message contains `[skip release]`) runs `.github/workflows/auto-release.yml`, which computes the next patch tag for the configured train with `strom release next-version`, creates and pushes an immutable annotated tag such as `v0.1.1`, and calls the existing `.github/workflows/release.yml` to build and publish it. `latest` image tags are only emitted for stable tags, never for `-rcN` prereleases.
3. Pull requests targeting `main` automatically get lightweight release-candidate agent binaries as workflow artifacts via `.github/workflows/rc.yml`. Attach the `release-candidate` label (or run the workflow manually) to also validate a build of the controller and agent container images; those RC images are never pushed anywhere.
4. Maintainers can still cut a tag by hand (for example, an out-of-band hotfix) by pushing a SemVer-style tag such as `v0.1.0` or a prerelease like `v0.1.0-rc1`; `release.yml` still triggers directly on any `v*` tag push.
5. `release.yml` runs tests, builds the agent for `linux/arm64` and `linux/armv6`, packages each archive with the install assets from `deploy/`, builds the `strom-node-<version>.img.xz` image through pi-gen, publishes those artifacts to the GitHub Release for that tag, and builds/pushes the multi-arch controller and agent container images to `ghcr.io/<owner>/strom-controller` and `ghcr.io/<owner>/strom-agent`.

You can build the same release payload locally with:


```sh
uv run strom release agent --version v0.1.0
uv run strom image node --version v0.1.0
uv run strom image controller --version v0.1.0
```

For local controller iteration, use:

```sh
uv run strom build controller
uv run strom dev controller
```

Controller DB backup/restore:

```sh
go run ./controller/cmd/controller backup --data-dir /data --output /tmp/controller-backup.db
go run ./controller/cmd/controller restore --data-dir /data --input /tmp/controller-backup.db --force
```

Controller-signed agent OTA push to an adopted node:

```sh
go run ./controller/cmd/controller ota --data-dir /data --node-id <node-id> --binary ./dist/strom-agent-linux-arm64 --version v0.4.0
```

The controller signs the binary digest with its CA private key, then pushes the payload to the node's trusted TLS API. The node verifies the signature against the adopted controller CA certificate before replacing its local agent binary.

Image build prerequisites and the flash workflow are documented in [image/README.md](image/README.md).

The user-facing documentation set lives in [docs/](docs). If you are looking for setup steps, product capabilities, or operational reference material, start there.

Local docs and repo tooling are managed with `uv`. Use `uv run strom docs setup`, `uv run strom docs build`, or `uv run strom docs serve` from the repo root after installing `uv`, and use `uv run strom hooks install` or `uv run strom hooks run` for repo hygiene checks.

For the newer project CLI flow, run `strom` from an activated virtual environment, or use `uv run strom` without activating one. Current commands include:

- `strom docs`, `strom docs serve`, `strom docs build`
- `strom sim up|down|ps|logs`
- `strom sim smoke [--strict]`
- `strom sim scenario <name>` (`ci-smoke`, `on_battery`, `restore`, `node_loss`)
- `strom hooks install|run`
- `strom build ...`, `strom dev ...`, `strom check ...`, `strom image ...`, `strom release ...`

Examples:

```sh
uv run strom docs
uv run strom sim smoke --strict --replicas 2
uv run strom sim up --replicas 2
strom sim logs --service strom-controller --tail 200
strom hooks run
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
