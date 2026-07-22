#!/bin/sh
# strom-agent-launcher.sh execs whichever strom-agent binary is currently
# active. It is installed at /usr/local/bin/strom-agent and is the target of
# strom-agent.service's ExecStart=, so the systemd unit never needs to change
# when a signed update is staged and activated by the running agent.
#
# Activation is tracked by internal/updates.Store as an atomic symlink at
# $STROM_UPDATES_ROOT/current -> releases/<version>/strom-agent. If that
# symlink is missing, broken, or not executable (e.g. this node has never
# received an update, or the only activation attempt ever made failed and
# was rolled back before a previous release existed), fall back to the
# recovery binary shipped with the OS image.
set -eu

STROM_UPDATES_ROOT="${STROM_UPDATES_ROOT:-/var/lib/strom/agent}"
CURRENT="$STROM_UPDATES_ROOT/current"
RECOVERY="/usr/local/libexec/strom-agent-recovery"

if [ -x "$CURRENT" ]; then
	exec "$CURRENT" "$@"
fi

if [ -x "$RECOVERY" ]; then
	exec "$RECOVERY" "$@"
fi

echo "strom-agent-launcher: no executable agent binary found (checked $CURRENT and $RECOVERY)" >&2
exit 1
