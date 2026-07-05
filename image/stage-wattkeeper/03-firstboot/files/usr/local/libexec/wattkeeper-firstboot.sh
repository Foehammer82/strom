#!/bin/sh
set -eu

overlay_opt_out=''
for marker in /boot/firmware/wattkeeper-overlayfs-disable /boot/wattkeeper-overlayfs-disable; do
	if [ -f "$marker" ]; then
		overlay_opt_out="$marker"
		break
	fi
done

install -d -m 0755 /var/lib/wattkeeper

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

hostname="wkeeper-node-$suffix"

if id -u wattkeeper >/dev/null 2>&1; then
	passwd -l wattkeeper >/dev/null 2>&1 || true
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

# Enable Raspberry Pi OverlayFS by default to reduce SD card wear from steady writes.
# Place /boot/firmware/wattkeeper-overlayfs-disable before first boot to opt out.
if [ ! -f /var/lib/wattkeeper/.overlayfs-enabled ] && [ -z "$overlay_opt_out" ] && command -v raspi-config >/dev/null 2>&1; then
	if raspi-config nonint do_overlayfs 0 >/dev/null 2>&1; then
		touch /var/lib/wattkeeper/.overlayfs-enabled
		sync
		if systemctl --no-block reboot >/dev/null 2>&1; then
			exit 0
		fi
		if reboot >/dev/null 2>&1; then
			exit 0
		fi
	fi
fi

touch /var/lib/wattkeeper/.firstboot-complete

systemctl disable wattkeeper-firstboot.service >/dev/null 2>&1 || true
