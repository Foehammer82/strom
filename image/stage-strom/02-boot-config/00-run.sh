#!/bin/bash -e

BOOT_CONFIG="${ROOTFS_DIR}/boot/firmware/config.txt"
if [ ! -f "$BOOT_CONFIG" ]; then
	BOOT_CONFIG="${BOOTFS_DIR}/config.txt"
fi

if ! grep -q '^# strom power tuning$' "$BOOT_CONFIG"; then
	cat >> "$BOOT_CONFIG" <<'EOF'

# strom power tuning
dtoverlay=disable-bt
hdmi_ignore_hotplug=1
EOF
fi
