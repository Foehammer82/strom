# Wattkeeper Docs

Wattkeeper is a distributed UPS monitoring system built around small Raspberry Pi nodes that auto-detect USB UPS devices, configure NUT locally, and advertise themselves for discovery.

This documentation set is the user-facing entry point for the project. It is written in Markdown and organized so operators and evaluators can find the current path quickly.

## Start Here

- Read [Getting Started](getting-started.md) if you want to build or flash a node image today.
- Read [Features](features.md) if you want a high-level view of what exists now versus what is still planned.
- Read [FAQ](faq.md) for common questions about hardware support, image usage, and current project scope.
- Read [Reference](reference/index.md) for the current node-agent behavior, image artifacts, and flashing details.

## Current State

Today the repository ships:

- a Phase 1 node agent that discovers USB UPS devices, renders NUT configuration, manages related services, and exposes a local health endpoint
- a Phase 2 Raspberry Pi image pipeline that produces a flashable `.img.xz` artifact for node deployment

The controller, adoption workflow, fleet UI, and Home Assistant bridge are still planned work.

## Documentation Scope

This docs site is intended for user-facing and operator-facing material such as:

- getting started guides
- flash and setup instructions
- feature and compatibility notes
- operational reference pages
- FAQ and troubleshooting guidance

Contributor workflow and implementation-planning details remain in the repo root documents such as `README.md`, `CONTRIBUTING.md`, and `ROADMAP.md`.