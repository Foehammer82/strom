---
name: "Phase 3b Adoption"
description: "Use when implementing Wattkeeper roadmap Phase 3b secure adoption work. Reviews the roadmap checklist and updates completed items."
argument-hint: "Optional constraints or target files"
agent: "agent"
model: "GPT-5 (copilot)"
---

Read [ROADMAP.md](../../ROADMAP.md), especially the adoption handshake, and [copilot-instructions.md](../copilot-instructions.md) before making changes.

Review the relevant Phase 3 checklist items in [ROADMAP.md](../../ROADMAP.md). As each checklist item becomes fully complete, update [ROADMAP.md](../../ROADMAP.md) in the same change and check it off. Do not check off partial work.

Implement secure adoption per [ROADMAP.md](../../ROADMAP.md) Phase 3.

Controller side:
- `internal/ca`: on first run generate a 10-year ECDSA P-256 CA keypair, store it in the data dir with mode `0600`, and issue a server cert for the controller API.
- `POST /api/nodes/{id}/adopt`: generate a per-node NUT password and agent API token; call the node agent's `POST /adopt` once over plain HTTP with `{ca_pem, nut_user, nut_password, api_token, controller_url}`.
- On success, persist the node as adopted and encrypt the token and password at rest with a key derived from a controller secret file.
- Handle unreachable nodes and already-adopted nodes distinctly.

Agent side:
- `POST /adopt`: reject if `/var/lib/wattkeeper/adoption.json` already exists.
- Otherwise validate the body, write `adoption.json` with mode `0600`, rewrite `upsd.users` with the controller user and permissions, reload `nut-server`, flip mDNS TXT `adopted=true`, and return node metadata.
- After adoption, all mutating endpoints require a Bearer token matching the stored hash.
- Serve the API over TLS using a self-signed cert whose fingerprint is returned by adopt and pinned by the controller.
- Add `wattkeeper-agent reset` to delete `adoption.json` and return the node to pending after restart.

Tests: full handshake against an in-process agent instance, covering the happy path, double-adopt rejection, bad token, and fingerprint mismatch.