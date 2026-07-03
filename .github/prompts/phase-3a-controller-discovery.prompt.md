---
name: "Phase 3a Controller Discovery"
description: "Use when implementing Wattkeeper roadmap Phase 3a controller skeleton and discovery work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 3 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Start the controller per [ROADMAP.md](../../ROADMAP.md) Phase 3.

Controller backend:
- `cmd/controller/main.go`: flags for `--data-dir`, `--listen :9000`, `--log-level`.
- `internal/registry`: SQLite using `modernc.org/sqlite` with tables for `nodes`, `ups`, and `samples`, plus embedded migrations.
- `internal/browse`: continuous mDNS browsing of `_wattkeeper._tcp`; maintain an in-memory live-node map keyed by TXT `id`, reconcile against the registry, and classify nodes as pending, adopted-online, or adopted-offline.
- HTTP JSON API: `GET /api/nodes` and `GET /api/nodes/{id}`. Structure it for a React frontend and add CORS for development.

Frontend:
- Vite + React + TypeScript scaffold.
- Single page with a table of nodes grouped by status and auto-refresh every 5s.
- Minimal styling, no component library required.
- Add `controller-dev` and `controller-build` Makefile targets, with the production frontend embedded into the Go binary via `embed.FS`.