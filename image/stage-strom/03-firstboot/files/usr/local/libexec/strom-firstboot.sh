#!/bin/sh
set -eu

state_dir=/var/lib/strom

if ! mountpoint -q "$state_dir"; then
	echo "strom state partition is not mounted at $state_dir" >&2
	exit 1
fi

install -d -m 0755 "$state_dir"

if [ ! -f /etc/strom/agent.yaml ]; then
	install -d -m 0700 /etc/strom
	nut_password=$(od -An -N 32 -tx1 /dev/urandom | tr -d ' \n')
	if [ -z "$nut_password" ]; then
		echo "failed to generate NUT bootstrap password" >&2
		exit 1
	fi
	{
		printf '%s\n' 'nut:'
		printf '%s\n' '  username: agent'
		printf '%s\n' "  password: $nut_password"
	} >/etc/strom/agent.yaml
	chmod 0600 /etc/strom/agent.yaml
fi

serial=''
if [ -r /sys/firmware/devicetree/base/serial-number ]; then
	serial=$(tr -d '\000' < /sys/firmware/devicetree/base/serial-number || true)
fi
if [ -z "$serial" ] && [ -r /proc/cpuinfo ]; then
	serial=$(awk '/^Serial/ { print $3; exit }' /proc/cpuinfo || true)
fi

serial=$(printf '%s' "$serial" | tr -cd '[:xdigit:]' | tr '[:upper:]' '[:lower:]')
suffix=$(printf '%s' "$serial" | sed 's/.*\(....\)$/\1/')
if [ -z "$suffix" ]; then
	suffix=0000
fi

hostname="strom-node-$suffix"

if id -u strom >/dev/null 2>&1; then
	passwd -l strom >/dev/null 2>&1 || true
fi

if command -v hostnamectl >/dev/null 2>&1; then
	hostnamectl set-hostname "$hostname"
else
	printf '%s\n' "$hostname" > /etc/hostname
	if command -v hostname >/dev/null 2>&1; then
		hostname "$hostname"
	fi
fi

if grep -q '^127\.0\.1\.1[[:space:]]' /etc/hosts; then
	sed -i "s/^127\.0\.1\.1[[:space:]].*/127.0.1.1\t$hostname/" /etc/hosts
else
	printf '127.0.1.1\t%s\n' "$hostname" >> /etc/hosts
fi

touch "$state_dir/.firstboot-complete"

# The persistent state partition is mounted before this service, so enabling
# Raspberry Pi's RAM-backed root overlay cannot discard node credentials.
if [ ! -f "$state_dir/.overlayfs-enabled" ] && command -v raspi-config >/dev/null 2>&1; then
	if raspi-config nonint do_overlayfs 0 >/dev/null 2>&1; then
		touch "$state_dir/.overlayfs-enabled"
		sync
		if systemctl --no-block reboot >/dev/null 2>&1; then
			exit 0
		fi
		if reboot >/dev/null 2>&1; then
			exit 0
		fi
	fi
fi

systemctl disable strom-firstboot.service >/dev/null 2>&1 || true
