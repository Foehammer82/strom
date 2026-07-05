#!/bin/sh
set -eu

SIM_DIR="${AGENT_SIM_DIR:-/sim/dummy-ups}"
NODE_ID="${AGENT_NODE_ID:-${HOSTNAME:-sim-node}}"
HTTP_AUTH="${AGENT_HTTP_AUTH:-true}"
DEMO_MODE="${AGENT_DEMO_MODE:-true}"
NUT_USER="${AGENT_NUT_USERNAME:-agent}"
NUT_PASS="${AGENT_NUT_PASSWORD:-agent-secret}"

mkdir -p /etc/wattkeeper /var/lib/wattkeeper /etc/nut "$SIM_DIR"

if [ ! -f /etc/wattkeeper/agent.yaml ]; then
    cat >/etc/wattkeeper/agent.yaml <<EOF
nut:
  username: ${NUT_USER}
  password: ${NUT_PASS}
EOF
fi

set -- /app/wattkeeper-agent --config-dir /etc/nut --listen :80 --tls-listen :8443 --simulate "$SIM_DIR" --node-id "$NODE_ID"

if [ "$DEMO_MODE" = "true" ]; then
    set -- "$@" --demo-mode
fi
if [ "$HTTP_AUTH" = "true" ]; then
    set -- "$@" --http-auth=true
else
    set -- "$@" --http-auth=false
fi

exec "$@"
