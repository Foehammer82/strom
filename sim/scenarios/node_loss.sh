#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

container_id="$(compose ps -q wattkeeper-agent | head -n 1)"
if [[ -z "$container_id" ]]; then
    echo "no running agent container found"
    exit 1
fi

docker stop "$container_id" >/dev/null

echo "node_loss scenario applied (stopped container $container_id)"
