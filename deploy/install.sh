#!/bin/sh
set -eu

if [ "${1:-}" = "" ]; then
	echo "usage: $0 /path/to/wattkeeper-agent" >&2
	exit 1
fi

BIN_SOURCE=$1
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

if [ ! -f "$BIN_SOURCE" ]; then
	echo "binary not found: $BIN_SOURCE" >&2
	exit 1
fi

install -d /usr/local/bin /etc/systemd/system /etc/udev/rules.d
install -m 0755 "$BIN_SOURCE" /usr/local/bin/wattkeeper-agent
install -m 0644 "$SCRIPT_DIR/wattkeeper-agent.service" /etc/systemd/system/wattkeeper-agent.service
install -m 0644 "$SCRIPT_DIR/99-wattkeeper-agent.rules" /etc/udev/rules.d/99-wattkeeper-agent.rules

systemctl daemon-reload
udevadm control --reload
systemctl enable --now wattkeeper-agent.service