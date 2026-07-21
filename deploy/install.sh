#!/bin/sh
set -eu

if [ "${1:-}" = "" ]; then
	echo "usage: $0 /path/to/strom-agent" >&2
	exit 1
fi

BIN_SOURCE=$1
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

if [ ! -f "$BIN_SOURCE" ]; then
	echo "binary not found: $BIN_SOURCE" >&2
	exit 1
fi

install -d /usr/local/bin /etc/systemd/system /etc/udev/rules.d
install -m 0755 "$BIN_SOURCE" /usr/local/bin/strom-agent
install -m 0644 "$SCRIPT_DIR/strom-agent.service" /etc/systemd/system/strom-agent.service
install -m 0644 "$SCRIPT_DIR/strom-ssh-access.service" /etc/systemd/system/strom-ssh-access.service
install -m 0644 "$SCRIPT_DIR/99-strom-agent.rules" /etc/udev/rules.d/99-strom-agent.rules

systemctl daemon-reload
udevadm control --reload
systemctl enable strom-ssh-access.service
systemctl enable --now strom-agent.service
