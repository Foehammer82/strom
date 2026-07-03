---
name: "Phase 0 Scaffold"
description: "Use when implementing Wattkeeper roadmap Phase 0 scaffold work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md) and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the Phase 0 checklist in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Scaffold the wattkeeper monorepo per [ROADMAP.md](../../ROADMAP.md).

Create:
1. `go.work` with modules `agent/` and `controller/` (`controller` can be a stub `main.go` for now).
2. `agent/cmd/agent/main.go` that parses flags (`--config-dir` default `/etc/nut`, `--listen :8080`, `--log-level`) and starts an empty run loop with graceful shutdown on `SIGTERM` and `SIGINT`.
3. Top-level `Makefile` with targets:
   - `agent`: cross-compile for `linux/arm64` and `linux/arm` (`GOARM=6`), output to `dist/`
   - `test`: `go test ./...` in both modules
   - `lint`: `golangci-lint run`
   - `image`, `sim-up`, `sim-down`: placeholder targets that echo `not implemented`
4. `.golangci.yml` with a sane default linter set, `.editorconfig`, `.gitignore` (Go + node + `dist/` + `*.img*`).
5. GitHub Actions workflow: on push/PR run lint + test; on tag `v*` also run `make agent` and upload `dist/` binaries as artifacts.

Keep it minimal and avoid business logic. Verify `make test` and `make agent` succeed.