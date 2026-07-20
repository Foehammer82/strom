# Wattkeeper — Roadmap

Distributed UPS monitoring and management. UniFi-style controller/adopt model:
cheap Pi Zero 2 W nodes run NUT and auto-configure any USB UPS plugged into them,
a central controller discovers/adopts nodes, aggregates metrics, and bridges
everything to Home Assistant.

> Project name is `wattkeeper`. It appears in module paths, binary names,
> systemd unit names, and the mDNS service type.

## Repo layout (monorepo)

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
│       └── api/                # local HTTP API (adoption target, later)
├── controller/                 # Go backend + React UI (Phase 3+)
│   ├── cmd/controller/
│   ├── internal/
│   └── web/
├── image/                      # pi-gen based SD card image build
│   ├── stage-wattkeeper/         # custom pi-gen stage
│   └── config                  # pi-gen config
├── sim/                        # virtual UPS + node simulation (stretch)
│   ├── dummy-ups/              # NUT dummy-ups driver configs + .dev files
│   └── docker-compose.yml      # simulated node(s) in containers
└── deploy/                     # systemd units, udev rules, install scripts
```

## Phase 0 — Repo scaffold

- [x] Init repo, Go workspace (`go.work`) covering `agent/` and `controller/`
- [x] Top-level CLI workflow: `wk build agent`, `wk image node`, `wk sim up`, `wk check test`
- [x] CI stub (GitHub Actions): lint + test on push, cross-compile agent for
      `linux/arm64` (Zero 2 W is 64-bit capable) and `linux/arm` (fallback)
- [x] `.editorconfig`, `gofmt`/`golangci-lint` config

## Phase 1 — Node agent MVP

Goal: plug any USB UPS into a Pi running the agent and it appears as a working
NUT netserver with zero manual config, discoverable on the LAN.

- [x] **Hotplug watcher**: subscribe to udev events (netlink) for USB
      add/remove; debounce (UPSes enumerate noisily)
- [x] **Scanner**: shell out to `nut-scanner -U` on boot and on hotplug events;
      parse output into structs (driver, port, vendorid, productid, serial)
- [x] **Config generation**: render `/etc/nut/ups.conf` from scan results.
      Stable UPS naming: derive from serial number so names survive reboots
      and port changes. Template `nut.conf` (netserver), `upsd.conf`
      (LISTEN 0.0.0.0 3493), `upsd.users` (placeholder creds for Phase 1;
      controller-provisioned in Phase 3)
- [x] **Service management**: restart/reload `nut-server` and
      `nut-driver@` units only when generated config actually changed
      (hash compare)
- [x] **mDNS advertise**: `_wattkeeper._tcp.local` with TXT records:
      `id=<pi-serial>`, `adopted=false`, `ups_count=N`, `version=X`
- [x] **Agent node HTTP surface**: local dashboard on `/`, minimal public
      `GET /status`, and detailed `GET /healthz` on :80
- [x] Systemd unit + udev rule files in `deploy/`
- [x] Unit tests: scanner output parsing, config rendering, name stability

**Exit criteria**: fresh Raspberry Pi OS Lite + agent installed → plug in
BE1050G3 → `upsc <name>@<pi-ip>` works from another machine within ~15s, and
the node shows up in `avahi-browse _wattkeeper._tcp`.

## Phase 2 — Flashable image

Goal: one `.img` file. Flash, boot, done.

- [x] pi-gen custom stage: installs `nut`, agent binary, systemd units,
      udev rules; disables unneeded services; enables SSH with key-only auth
      (key injected at flash time via bootfs, same mechanism as Pi Imager)
- [x] First-boot script: expand filesystem, generate per-device NUT runtime
      dirs, hostname = `wkeeper-node-<last4 of serial>`
- [x] WiFi provisioning: support standard `custom.toml` / Pi Imager style
      config on the boot partition so users set WiFi creds at flash time
      without touching the rootfs
- [x] `wk image node` command that runs pi-gen in Docker and drops
      `wattkeeper-node-vX.Y.Z.img.xz` in `dist/`
- [x] CI job to build image on tag

**Exit criteria**: flash image with Pi Imager (WiFi + SSH key set in imager),
boot Pi, plug in UPS, node is discoverable and serving NUT with no SSH session
ever opened.

While iterating on Phase 2 work locally, validate with this sequence before pushing:

1. Run `uv run wk image node --version <dev-or-rc-tag>` and confirm it emits `dist/wattkeeper-node-<version>.img.xz` and the matching `.sha256` file.
2. If a pi-gen run fails mid-build and you are refining the custom stage, use `uv run wk image node --version <same-tag> --continue` to resume from the preserved `pigen_work` container instead of starting from scratch.
3. Flash the image with Raspberry Pi Imager using WiFi and SSH-key customization, then boot a real Pi Zero 2 W with a USB UPS attached.
4. Verify the Phase 2 exit behavior on hardware: first-boot hostname rewrite, `/var/lib/wattkeeper` creation, mDNS advertisement, and remote NUT access via `upsc`.

## Phase 3 — Controller: discovery + adoption

Goal: web UI that shows pending nodes, adopts them securely, and becomes the
single pane of glass.

- [x] Go backend: mDNS browser tracks live nodes; SQLite registry of adopted
      nodes (id, name, location label, adopted_at, last_seen, agent version)
      Completed sub-milestones:
      - [x] mDNS browser tracks live `_wattkeeper._tcp` nodes
      - [x] SQLite-backed node registry exists and persists discovered nodes
      - [x] Controller JSON APIs expose `GET /api/nodes` and `GET /api/nodes/{id}`
      - [x] Controller is packaged as a deployable Docker image
      Progress so far: controller now has an mDNS browser, SQLite-backed node
      registry, `GET /api/nodes`, `GET /api/nodes/{id}`, a grouped fleet web
      shell with pending-node adopt actions, controller-managed node
      display/location metadata editing, and a deployable Docker image.
      The controller backend now includes the adopted-node registry,
      metadata editing, forget-node support, UPS summaries/detail/history APIs,
      and alert-rule/event APIs needed by the controller GUI.
- [x] **Adoption handshake**:
      1. Controller has a self-signed CA (generated on first run)
      2. Adopt click → controller calls node agent API `POST /adopt` with its
         CA cert + a freshly generated per-node NUT password + agent API token
      3. Node stores them, rewrites `upsd.users`, pins controller CA, flips
         mDNS TXT to `adopted=true`, rejects future `/adopt` calls
      4. All subsequent controller→node API traffic is TLS with the pinned CA;
         re-adoption requires factory reset (documented file to delete on the
         node, or physical button/GPIO later)
            Completed sub-milestones:
            - [x] Controller generates and persists a local CA on first run
            - [x] Controller issues per-node NUT credentials and API tokens
            - [x] Controller UI/backend can trigger `POST /adopt` for pending nodes
            - [x] Node persists adoption state and rewrites `upsd.users`
            - [x] Node flips mDNS TXT `adopted=true` after adoption
            - [x] Node exposes a dedicated HTTPS controller API listener after adoption
            - [x] Controller performs an immediate pinned HTTPS follow-up check using the returned node certificate fingerprint
            - [x] Controller now persists encrypted node trust material and reuses the pinned HTTPS channel for follow-up node health reads
      Progress so far: controller now generates and persists a local CA,
      creates per-node NUT credentials plus API tokens, and calls the node
      `POST /adopt` endpoint. The node persists adoption state, rewrites
      `upsd.users`, reloads NUT, flips mDNS TXT `adopted=true`, and accepts
      post-adoption bearer tokens for mutating UPS control endpoints. The node
      now serves a dedicated HTTPS controller API listener after adoption and
      returns a certificate fingerprint that the controller verifies on an
      immediate pinned HTTPS follow-up call. The controller now persists
      encrypted node trust material and can reuse that pinned HTTPS channel for
      later node health reads. Nodes can now be returned to pending state with
      `wattkeeper-agent reset`, which clears the on-node adoption state and
      controller API TLS material before restart.
      The controller now uses the pinned HTTPS channel for follow-up health,
      trusted UPS detail reads, and trusted UPS instant-command passthrough.
- [x] NUT polling: controller maintains NUT client connections to every
      adopted node, snapshots all variables on an interval into SQLite
      (retention configurable)
      Progress so far: controller now has an internal NUT polling package that
      authenticates to adopted nodes with the provisioned controller NUT
      credentials, snapshots UPS variables into SQLite on an interval, and
      prunes stored samples with a configurable retention window. Repeated poll
      failures now update a poll-derived node comms state that is exposed in
      the fleet APIs/UI, and the controller node APIs now include recent UPS
      telemetry summaries plus per-UPS detail/history endpoints from those
      stored samples. The controller can also proxy trusted UPS detail reads
      and instant commands over the pinned HTTPS channel for adopted nodes.
      The controller now stores recent UPS telemetry, exposes filtered history
      for charts, and derives a poll-based communications state used by the UI
      and alerting.
- [x] React UI: pending adoptions, fleet grid (status/load/runtime/battery),
      per-UPS detail with history charts, per-node health (temp, disk,
      agent version), rename node, trigger instcmds (beeper, battery test)
      Completed sub-milestones:
      - [x] Controller has a branded fleet web shell with grouped node inventory
      - [x] Pending nodes can be adopted directly from the controller UI shell
      - [x] Node local UI already provides branded telemetry and UPS control workflows
      The node local UI already has a branded dark/light dashboard, live UPS
      telemetry, instant command execution, and writable NUT variable controls.
      The controller now ships a React + TypeScript GUI with a fleet grid,
      per-node detail, per-UPS detail/history charts, trusted instant-command
      passthrough, node metadata editing, and an alerts view while preserving
      the existing Wattkeeper theme language from the node UI.
- [x] Alert rules: on-battery, low-battery, node-offline, and comms-lost → webhook
      MQTT delivery is intentionally deferred to Phase 4 alongside the broader
      Home Assistant bridge.

**Implementation status**: Phase 3 software work is complete in this repo.
The remaining checklist risk is real-hardware validation against the exit
criteria below.

**Exit criteria**: two real nodes adopted through the UI, credentials never
hand-configured, pulling live metrics, instcmd round-trips work.

## Phase 4 — Home Assistant bridge

- [x] MQTT publisher with HA discovery: every UPS becomes a device with
      sensors (charge, load, runtime, voltage, status) + buttons for
      supported instcmds; controller/node health as diagnostic entities
      Progress so far: the controller now has an `internal/mqtt` package that
      generates Home Assistant discovery/state payloads for adopted UPSes and
      publishes retained discovery, state, and per-node availability messages
      from the existing poll cycle when an MQTT broker is configured. The
      bridge now subscribes to MQTT button command topics and routes those
      commands back to the trusted controller-side UPS command APIs. MQTT
      snapshots now enrich status/charge/load/runtime/input-voltage from
      trusted live UPS detail data when available, and controller availability
      heartbeat republishing is covered by publisher tests.
- [x] aggregate NUT server mode — controller re-serves all downstream UPSes on
      its own :3493 for anything that speaks native NUT
      Progress so far: the controller now includes an aggregate NUT manager
      with listener lifecycle control, persistent settings integration, and
      protocol support for auth (`USERNAME`/`PASSWORD`), `LIST UPS`,
      `LIST VAR`, `GET VAR`, `LIST CMD`, `GET CMDDESC`, `INSTCMD`, and
      `QUIT`/`LOGOUT`. The controller UI exposes a global settings toggle to
      enable or disable the aggregate listener and edit listen address.
      Coverage includes focused aggregate manager tests and end-to-end
      controller integration tests.
- [x] Docs: HA setup guide

**Implementation status**: Phase 4 software scope is complete in this repo.
Controller tests and simulation smoke now exercise MQTT bridge and aggregate
NUT paths. Continue collecting real-environment Home Assistant operator
feedback as normal post-implementation validation.

**Exit criteria**: fresh HA instance + MQTT integration → all UPSes appear
automatically with correct device grouping, controls work.

## Phase 5 — Hardening & lifecycle

- [x] Agent OTA updates pushed from controller (signed binaries)
- [x] Node factory-reset flow (GPIO jumper or boot-partition flag file)
- [x] Node HTTP auth bootstrap: first browser client to reach an uninitialized
      node must create the local admin username/password; after bootstrap, the
      node dashboard and detailed node APIs such as `GET /status/details`
      and `GET /healthz` require authentication via a node-local session flow
- [x] Node HTTP surface hardening: keep unauthenticated `GET /status` limited
      to basic aggregate state suitable for discovery and quick checks; avoid
      stable UPS identifiers and detailed node metrics there, and keep richer
      diagnostics behind the authenticated node UI or controller-trusted APIs
- [x] Insecure node UI bypass policy: keep `--http-auth=false` as an explicit
      local-development escape hatch only; do not enable it in image defaults,
      packaged service units, or controller-managed production policy
- [x] Session and form hardening for node-local auth: review CSRF protection,
      cookie security attributes under TLS, session expiration/rotation, and
      the exact contract for non-browser authenticated clients
- [x] Controller-managed node UI policy: let the controller drive the node's
      local UI enabled/disabled state through the same policy surface that
      backs the node settings page, with a documented local override/reset
- [x] Controller-managed node UI policy: after adoption, the controller can
      enable or disable each node's local web UI and related local-auth access
- [x] Battery health trending / replace-by estimates from runtime decay
- [x] Backup/restore of controller DB
- [x] Read-only rootfs or overlayfs on nodes to survive SD card abuse
- [x] Multi-UPS-per-node support verification (USB hub on a 3A+ etc.)

## Phase 5.5 - Release orchestration and auto-versioning

Goal: remove manual tag choreography for normal releases by letting merges to
`main` drive deterministic build/deploy/release automation, while still
keeping explicit maintainer control over major/minor version bumps.

- [x] Create a single source of truth for release train control (for example,
      `.github/release/version.toml`) that stores the active `major` and
      `minor` values plus RC behavior toggles used by workflows.
- [x] Add a validated `wk release next-version` path that computes the next
      patch from existing tags for the configured major/minor train
      (`vMAJOR.MINOR.PATCH`) and supports prerelease forms
      (`vMAJOR.MINOR.PATCH-rcN`).
- [x] Add automation so any merge commit or direct push to `main` runs a
      release orchestration workflow that:
      1. Computes `next_patch` for the configured major/minor train
      2. Creates an immutable annotated release tag
      3. Reuses existing release jobs to publish binaries, node image, and
         controller/agent container images
      4. Emits `latest` tags only for stable releases (never for `-rcN`)
- [x] Keep explicit manual control for major/minor changes via a dedicated
      maintainer-only workflow or PR that edits the central version file,
      with audit trail in git history.
- [x] Define RC generation policy for PRs targeting `main` and implement one
      of these modes:
      1. Always build RC artifacts for every PR to `main`
      2. Build RC artifacts only when a PR has a release-candidate label
      3. Build lightweight RC binaries by default, and gate expensive RC image
         builds behind label or manual workflow dispatch
- [x] Add branch protection and workflow permissions so release tags are only
      created by CI on `main` and cannot be overwritten or force-moved.
- [x] Document the new release model in `README.md` and `CONTRIBUTING.md`,
      including examples for major/minor bump operations, RC trigger behavior,
      and rollback/hotfix handling.

**Implementation status**: Phase 5.5 software scope is complete in this repo.
`.github/release/version.toml` is the release-train source of truth, `wk
release next-version`/`wk release set-train` are pytest-covered, and
`.github/workflows/auto-release.yml`, `rc.yml`, and `version-bump.yml` wire the
automation described above on top of the existing `release.yml` publish jobs
(now also invocable via `workflow_call`). The remaining branch-protection item
is enforced via a repository tag ruleset in GitHub settings (documented in
`CONTRIBUTING.md`), which is an administrative action outside this repo's
tracked files.

## Phase 5.6 - Node image real-hardware validation

Goal: close out the remaining real-hardware risk from Phase 2 and Phase 5 by
actually flashing and testing a current node image on real Raspberry Pi
hardware with a real USB UPS attached, before resuming controller-side work.
This phase is also the holding area for any fixes or follow-up changes
discovered while doing that validation.

- [ ] Flash a current node image (`dist/wattkeeper-node-<version>.img.xz`)
      with Raspberry Pi Imager using WiFi + SSH-key customization, per the
      Phase 2 validation sequence
- [ ] Boot a real Pi (Zero 2 W or other supported target) and verify
      first-boot behavior: hostname rewrite to `wkeeper-node-<last4 serial>`,
      `/var/lib/wattkeeper` creation, and the OverlayFS first-boot reboot
- [ ] Plug in a real USB UPS and verify zero-config detection: generated
      `ups.conf`/`upsd.conf`/`upsd.users`, stable UPS naming across reboots,
      and `nut-server`/`nut-driver@` reload behavior
- [ ] Verify mDNS discoverability (`avahi-browse _wattkeeper._tcp`) and
      remote NUT access (`upsc <name>@<pi-ip>`) from another machine
- [ ] Verify the node local HTTP surface on real hardware: auth bootstrap
      flow, `GET /status`, `GET /healthz`, and the dashboard UI
- [ ] Record and fix any bugs, doc gaps, or generated-config issues found
      during this pass, keeping fixes scoped to what real-hardware testing
      actually surfaces

**Exit criteria**: a freshly flashed Pi with the current node image meets the
Phase 1 and Phase 2 exit criteria on real hardware, with no manual
workarounds, and any issues found along the way are fixed or explicitly
tracked before moving back to controller-side roadmap work.

## Phase 6 - Alerting expansion

Goal: evolve beyond baseline alert events into a full alerting platform for
production operators and larger fleets.

- [ ] Robust alerting framework with first-class integrations for common
      notification targets such as Discord, Slack, Gotify, Notifiarr,
      Pushbullet, email, Telegram, Signal, and similar services
- [ ] Alert policies and routing: severity levels, deduplication, quiet
      hours, maintenance windows, repeat intervals, and
      per-site/per-node/per-event delivery targets

## Phase 7 - Shutdown orchestration

Goal: coordinate safe, policy-driven shutdown workflows for protected systems
based on battery/runtime signals.

- [ ] Shutdown orchestration for protected systems based on runtime
      thresholds, battery state, and power-restoration handling

## Phase 8 - Historical analytics

Goal: improve long-horizon insight for operators who need advanced reporting
and capacity planning.

- [ ] Historical analytics for outage frequency, battery/runtime trends, and
      capacity planning based on real-world load behavior

## Phase 9 - Observability export

Goal: provide optional external observability integration for operators who
run broader monitoring pipelines.

- [ ] Optional observability export via metrics endpoint or push integration
      for Prometheus and OpenTelemetry-compatible monitoring pipelines

## Phase 10 — Documentation & roadmap closeout

- [ ] Update `README.md` so it reflects the shipped architecture, setup flow,
      operational model, and current capabilities rather than planned work
- [ ] Add or refresh end-user and operator documentation for installation,
      adoption, upgrades, backup/restore, and recovery flows
- [ ] Remove or consolidate `.github/` prompts and skills that were only
      needed to complete roadmap implementation work, keeping only the
      customization assets still useful for ongoing maintenance
- [ ] Refresh `CONTRIBUTING.md` so it remains the contributor guide after
      roadmap-driven development ends, removing references that assume
      `ROADMAP.md` still exists
- [ ] Remove completed or superseded planning details from `ROADMAP.md` once
      the project has fully shipped and the roadmap is no longer the source of
      truth
- [ ] Delete `ROADMAP.md` after all roadmap phases are complete and its final
      documentation has been incorporated into `README.md` and any permanent
      docs

## Stretch — Virtual test rig (`sim/`)

Not required, keep it cheap:

- [x] Build and publish an agent container image (multi-arch like the
      controller image) so simulation and evaluation do not require a Pi image
- [x] Add agent simulation mode: `--simulate <dir>` bypasses nut-scanner and
      udev/netlink, treats each `*.dev` file as a detected UPS, and watches the
      directory with `fsnotify` so fixture drops/edits simulate hotplug
- [x] Add deterministic agent demo mode behavior for evaluation workflows,
      driven by scenario-script state changes rather than autonomous randomness
- [x] Add sample fixtures under `sim/dummy-ups/` modeled on APC Back-UPS
      BE1050G3 devices
- [x] Build `sim/docker-compose.yml` as a configurable sandbox with:
      - default two agent replicas
      - one controller
      - one Mosquitto broker
      - optional Home Assistant service via Compose profile
- [x] Document Docker/mDNS caveats (host networking or reflector sidecar may
      be required on some hosts)
- [x] Add scripted scenarios in `sim/scenarios/` for on-battery, restore, and
      node-loss simulation cases
- [x] Extend the `wk` CLI with `sim up`, `sim down`, and
      `scenario on_battery` plus replica override support
- [x] Add CI smoke coverage that boots the sandbox, asserts two pending nodes,
      adopts them, runs a scenario, and verifies metrics/MQTT flow

## Deliberately out of scope (for now)

- Cloud/remote access (Tailscale already solves this for you)
- Non-USB UPS transports (SNMP cards) — controller design shouldn't preclude
  it, but don't build it
- Windows/macOS nodes
- Fleet configuration profiles so groups of nodes can share alert policy,
      retention, update rings, and node-local UI settings
- Multi-user roles and audit logging for controller actions and node changes
- Power-event automation hooks for webhooks, scripts, and Home Assistant
      workflows beyond basic alert delivery
- Config drift detection and richer health diagnostics when live node state
      diverges from controller intent
- Support bundle export with sanitized logs, config summaries, and recent
      event history for troubleshooting
- Higher-availability controller deployment patterns beyond backup/restore
