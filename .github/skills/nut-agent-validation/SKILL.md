---
name: nut-agent-validation
description: 'Use for recurring Strom agent validation work: scanner fixtures, config rendering, name stability, service reload behavior, focused Go tests, and NUT config checks during Phase 1 development.'
argument-hint: 'Target package, file, or validation focus'
user-invocable: true
---

# NUT Agent Validation

Use this skill when working on the agent-side Phase 1 implementation, especially changes under `agent/internal/hotplug`, `agent/internal/nutconf`, `agent/internal/services`, or `agent/cmd/agent`.

## When to Use

- You changed scanner parsing, config rendering, or service reload behavior.
- You need a repeatable way to validate stable UPS naming and write-if-changed semantics.
- You are reviewing whether a roadmap Phase 1 checklist item is actually complete.

## Procedure

1. Read [ROADMAP.md](../../../ROADMAP.md) and confirm which Phase 1 checklist items the change is supposed to satisfy.
2. Inspect the affected package and its nearest tests first:
   - `agent/internal/nutconf/scanner_test.go`
   - `agent/internal/nutconf/render_test.go`
   - `agent/internal/services/services_test.go`
   - `agent/cmd/agent/main_test.go`
3. Run the narrowest useful test scope before broader validation:
   - `go test ./internal/nutconf`
   - `go test ./internal/services`
   - `go test ./cmd/agent`
4. If the change touches multiple pieces of the agent loop, run `go test ./...` inside `agent/`.
5. Confirm the behavior the roadmap actually requires:
   - scanner handles zero, single, multiple, and missing-serial cases
   - stable names persist across scans and collision cases
   - `ups.conf`, `nut.conf`, `upsd.conf`, and `upsd.users` render deterministically
   - writes are skipped when content hashes match
   - service reloads happen only when generated config changed
6. Only after those checks pass, update the relevant roadmap checklist items in [ROADMAP.md](../../../ROADMAP.md). Leave partial work unchecked.

## Notes

- Prefer focused tests over repo-wide validation first.
- Treat generated-text determinism as a first-class requirement, not a cleanup item.
- Do not mark deploy, mDNS, or health endpoint roadmap items complete from config-rendering work alone.
