#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/sim/docker-compose.yml"

compose() {
    docker compose -f "$COMPOSE_FILE" "$@"
}

set_dev_value() {
    local file="$1"
    local key="$2"
    local value="$3"
    if grep -q "^${key}:" "$file"; then
        sed -i "s|^${key}:.*|${key}: ${value}|" "$file"
    else
        printf '%s: %s\n' "$key" "$value" >>"$file"
    fi
}

fixture_files() {
    printf '%s\n' "$ROOT_DIR/sim/dummy-ups/be1050g3-a.dev" "$ROOT_DIR/sim/dummy-ups/be1050g3-b.dev"
}
