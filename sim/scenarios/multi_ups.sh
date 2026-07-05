#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

agent_replicas="${AGENT_REPLICAS:-2}"
controller_url="http://127.0.0.1:9000"

wait_for_pending_nodes() {
    local max_attempts=120
    local attempt=0
    while (( attempt < max_attempts )); do
        if payload="$(curl -sf "$controller_url/api/nodes")"; then
            count="$(jq '[.nodes[] | select(.live == true)] | length' <<<"$payload")"
            if [[ "$count" -ge "$agent_replicas" ]]; then
                return 0
            fi
        fi
        attempt=$((attempt + 1))
        sleep 2
    done
    return 1
}

adopt_pending_nodes() {
    local max_rounds=6
    local round=1
    while (( round <= max_rounds )); do
        local node_ids
        node_ids="$(curl -sf "$controller_url/api/nodes" | jq -r '.nodes[] | select(.live == true and .adopted == false) | .id')"
        if [[ -z "$node_ids" ]]; then
            return 0
        fi

        while IFS= read -r node_id; do
            [[ -z "$node_id" ]] && continue
            curl -sS -o /dev/null -X POST "$controller_url/api/nodes/$node_id/adopt" || true
        done <<<"$node_ids"

        round=$((round + 1))
        sleep 2
    done

    return 0
}

wait_for_multi_ups_per_node() {
    local max_attempts=90
    local attempt=1
    while (( attempt <= max_attempts )); do
        adopt_pending_nodes
        if payload="$(curl -sf "$controller_url/api/nodes")"; then
            adopted_count="$(jq '[.nodes[] | select(.live == true and .adopted == true)] | length' <<<"$payload")"
            multi_count="$(jq '[.nodes[] | select(.live == true and .adopted == true and (.ups_summaries | length) >= 2)] | length' <<<"$payload")"
            if [[ "$adopted_count" -ge "$agent_replicas" && "$multi_count" -ge "$agent_replicas" ]]; then
                return 0
            fi
        fi
        attempt=$((attempt + 1))
        sleep 2
    done
    return 1
}

if ! wait_for_pending_nodes; then
    echo "timed out waiting for pending nodes"
    curl -sf "$controller_url/api/nodes" || true
    exit 1
fi

if ! wait_for_multi_ups_per_node; then
    echo "timed out waiting for multi-UPS summaries on every adopted node"
    curl -sf "$controller_url/api/nodes" | jq '.nodes | map({id, live, adopted, ups_count: (.ups_summaries | length)})' || true
    exit 1
fi

node_ids="$(curl -sf "$controller_url/api/nodes" | jq -r '.nodes[] | select(.live == true and .adopted == true) | .id')"
while IFS= read -r node_id; do
    [[ -z "$node_id" ]] && continue

    ups_payload="$(curl -sf "$controller_url/api/nodes/$node_id/ups")"
    ups_count="$(jq '((.upses // .ups) // []) | length' <<<"$ups_payload")"
    if [[ "$ups_count" -lt 2 ]]; then
        echo "node $node_id reported fewer than 2 UPS entries ($ups_count)"
        echo "$ups_payload"
        exit 1
    fi

    ups_names="$(jq -r '((.upses // .ups) // [])[]?.name' <<<"$ups_payload")"
    while IFS= read -r ups_name; do
        [[ -z "$ups_name" ]] && continue
        encoded_name="$(jq -rn --arg value "$ups_name" '$value | @uri')"
        curl -sf "$controller_url/api/nodes/$node_id/ups/$encoded_name" >/dev/null
    done <<<"$ups_names"
done <<<"$node_ids"

echo "multi-UPS scenario succeeded"
