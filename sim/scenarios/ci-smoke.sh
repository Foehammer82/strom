#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

agent_replicas="${AGENT_REPLICAS:-2}"
controller_url="http://127.0.0.1:9000"
strict_full_adoption="${REQUIRE_FULL_ADOPTION:-${CI:-}}"

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

if ! wait_for_pending_nodes; then
    echo "timed out waiting for pending nodes"
    curl -sf "$controller_url/api/nodes" || true
    exit 1
fi

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
            local adopt_body adopt_status
            [[ -z "$node_id" ]] && continue
            adopt_body="$(mktemp)"
            adopt_status="$(curl -sS -o "$adopt_body" -w "%{http_code}" -X POST "$controller_url/api/nodes/$node_id/adopt" || true)"
            adopt_response="$(tr -d '\n' <"$adopt_body")"
            if [[ "$adopt_status" == "409" && "$adopt_response" == *"already adopted"* ]]; then
                rm -f "$adopt_body"
                continue
            fi
            if [[ "$adopt_status" -lt 200 || "$adopt_status" -gt 299 ]]; then
                echo "warning: adoption failed for node $node_id (status=$adopt_status, body=$adopt_response)"
            fi
            rm -f "$adopt_body"
        done <<<"$node_ids"

        round=$((round + 1))
        sleep 2
    done

    return 0
}

wait_for_adoption_convergence() {
    local target="$1"
    local max_attempts=25
    local attempt=1
    while (( attempt <= max_attempts )); do
        adopt_pending_nodes
        if payload="$(curl -sf "$controller_url/api/nodes")"; then
            adopted_count="$(jq '[.nodes[] | select(.live == true and .adopted == true)] | length' <<<"$payload")"
            if [[ "$adopted_count" -ge "$target" ]]; then
                return 0
            fi
        fi
        attempt=$((attempt + 1))
        sleep 2
    done
    return 1
}

adopt_pending_nodes

if [[ "$strict_full_adoption" != "" ]]; then
    if ! wait_for_adoption_convergence "$agent_replicas"; then
        echo "expected $agent_replicas live adopted nodes before scenario"
        curl -sf "$controller_url/api/nodes" || true
        exit 1
    fi
fi

bash "$ROOT_DIR/sim/scenarios/on_battery.sh"
sleep 8

adopted_count="$(curl -sf "$controller_url/api/nodes" | jq '[.nodes[] | select(.live == true and .adopted == true)] | length')"
if [[ "$adopted_count" -lt 1 ]]; then
    echo "expected at least one live adopted node, got $adopted_count"
    curl -sf "$controller_url/api/nodes" || true
    exit 1
fi
if [[ "$adopted_count" -lt "$agent_replicas" ]]; then
    if [[ "$strict_full_adoption" != "" ]]; then
        echo "expected $agent_replicas live adopted nodes, got $adopted_count"
        curl -sf "$controller_url/api/nodes" || true
        exit 1
    fi
    echo "warning: expected $agent_replicas live adopted nodes, got $adopted_count"
fi

metrics_ready_count="$(curl -sf "$controller_url/api/nodes" | jq '[.nodes[] | select(.live == true and .adopted == true and (.ups_summaries | length) > 0)] | length')"
if [[ "$metrics_ready_count" -lt 1 ]]; then
    echo "expected metrics for at least one live adopted node"
    curl -sf "$controller_url/api/nodes" || true
    exit 1
fi

if ! docker compose -f "$COMPOSE_FILE" logs --no-color --since=2m wattkeeper-controller \
    | grep -q "mqtt publish topic=wattkeeper/nodes/.*/ups/.*/state"; then
    echo "warning: no MQTT UPS state publish observed in controller logs"
fi

echo "simulation smoke succeeded"
