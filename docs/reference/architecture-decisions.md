# Controller Architecture Decisions (6-12 Month Strategy)

## Purpose

This memo captures controller architecture recommendations for the next 6-12 months.
It is written for core maintainers to guide roadmap and implementation decisions.

## Decision Summary

1. Keep SQLite as the default controller datastore.
2. Keep mDNS as the default first-hop discovery mechanism, with seed fallback where multicast is unreliable.
3. Keep MQTT focused on integration and command bridging, not core node discovery.
4. Keep aggregate NUT listener optional by default, and enable it when operators need a single native NUT endpoint.
5. Add objective migration triggers so new infrastructure is introduced only when measurable pressure appears.

## Current Architecture Baseline

The current implementation already provides:

- Embedded SQLite persistence for controller state and telemetry.
- LAN discovery over mDNS, with optional discovery seed polling fallback.
- Trusted post-adoption HTTPS node control path with pinned trust material.
- Interval-based NUT polling and retention-pruned telemetry snapshots.
- MQTT publishing and command subscription for Home Assistant integration workflows.
- Optional aggregate NUT listener mode on the controller.

These choices align with Strom's low-ops, edge-first deployment model.

## Recommended Defaults For The Next 6-12 Months

## Datastore

Use SQLite as the default and primary recommendation.

Rationale:

- Zero external database dependency keeps setup and support burden low.
- Matches single-controller deployment assumptions used by current code and docs.
- Sufficient for expected early fleet sizes with sensible poll and retention settings.

### Discovery

Use mDNS as the primary discovery path.

Rationale:

- Preserves plug-and-discover onboarding on local networks.
- Matches current node advertisement and controller browse model.
- Seed fallback already provides a practical path for multicast-hostile environments.

### Messaging

Use MQTT as integration plumbing, not as discovery replacement.

Rationale:

- Keeps Home Assistant integration straightforward.
- Avoids adding broker bootstrap requirements to first adoption.
- Prevents coupling core node enrollment to external broker availability.

### Aggregate NUT

Keep optional by default.

Enable when:

- Operators need compatibility for tools expecting one native NUT endpoint.
- A single controller address simplifies monitoring integrations.

## Migration Trigger Matrix

The following are starter thresholds. They are decision triggers, not automatic mandates.

### Consider Postgres When

1. Poll-cycle deadlines are frequently missed due to sustained database write contention.
2. p95 or p99 write latency remains above acceptable SLOs after retention and indexing tuning.
3. You need controller high availability or scale-out read replicas.
4. Restore and operational workflows require database capabilities not practical with SQLite.

### Consider Redis When

1. Multiple controller instances need shared short-lived state.
2. You need distributed locking or queue coordination across processes.
3. In-process caches are no longer sufficient and latency-sensitive shared caching is required.

### Consider Internal Event Bus Or Broker-Centric Control Plane When

1. Controller internals are split into independently deployed services.
2. Durable asynchronous workflows and backpressure become mandatory.
3. Cross-component event replay and delivery guarantees are required.

### Keep mDNS Default Unless

1. Target environments are routinely multi-subnet or multicast-blocked.
2. Discovery reliability remains poor even with documented seed fallback.
3. A secure alternative enrollment model is implemented and validated end-to-end.

## Permanent Homelab Lane

SQLite plus mDNS remains a valid long-term architecture for:

- Single-controller homelab installs.
- Small single-site fleets.
- Operators prioritizing low maintenance over horizontal scale.

This is a first-class path, not a temporary stepping stone.

## Scenario Posture Matrix

### Scenario: Single-Site Homelab (Small Fleet)

- Datastore: SQLite
- Discovery: mDNS (seed fallback optional)
- Messaging: MQTT optional for Home Assistant
- Notes: optimize for simplicity and minimal moving parts

### Scenario: Small Multi-Site Or Segmented LAN Environments

- Datastore: SQLite by default
- Discovery: mDNS plus seeds, possibly site-local discovery relays
- Messaging: MQTT for integration, not enrollment
- Notes: invest in discovery observability before stack migration

### Scenario: Large Fleet With Tight Freshness SLOs

- Datastore: evaluate Postgres once contention or SLO triggers are sustained
- Discovery: keep mDNS where feasible; supplement with controlled enrollment options as needed
- Messaging: evaluate internal event bus if service decomposition starts
- Notes: make migrations based on measured pressure, not projected pressure

## Risks In Current Defaults And Practical Mitigations

### Risk: SQLite Write Contention At Higher Scale

Mitigations:

- Tune poll interval and retention windows first.
- Add and verify indexes for high-frequency query paths.
- Track poll-loop duration and DB write latency percentiles.

### Risk: mDNS Reliability Variance Across Networks

Mitigations:

- Keep and document discovery seeds for constrained networks.
- Add discovery success-rate metrics and troubleshooting guidance.
- Validate multicast behavior in common deployment topologies.

### Risk: Single-Controller Failure Domain

Mitigations:

- Document backup and restore procedure with regular test restores.
- Keep controller state and secrets in routine backup scope.
- Define acceptable downtime and recovery objectives.

## Anti-Patterns To Avoid

1. Adding Postgres only because fleet size might grow, without measured contention or SLO failure.
2. Adding Redis without a concrete shared-state or coordination requirement.
3. Replacing mDNS with broker-first discovery before secure bootstrap and enrollment constraints are fully solved.
4. Introducing additional infrastructure that outpaces the operational maturity of typical Strom deployments.

## Immediate Next Actions (No Stack Swap Required)

1. Define and publish operational SLOs for poll freshness, adoption latency, and offline detection delay.
2. Add controller metrics for DB write latency, poll cycle duration, missed cycles, and discovery health.
3. Keep storage and discovery boundaries clean so optional backends can be added with minimal churn.
4. Revisit this memo after real Home Assistant and broker validation from Phase 4 completion work.

## Scope And Non-Goals

This memo does not mandate immediate migration to Postgres, Redis, or a broker-centric control plane.
It defines recommended defaults and objective conditions for revisiting those decisions.
