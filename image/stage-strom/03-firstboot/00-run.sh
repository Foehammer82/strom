#!/bin/bash -e

install -d "${ROOTFS_DIR}/usr/local/libexec"
install -d "${ROOTFS_DIR}/etc/systemd/system"
install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"
install -d -m 0755 "${ROOTFS_DIR}/var/lib/strom"

install -m 0755 files/usr/local/libexec/strom-firstboot.sh "${ROOTFS_DIR}/usr/local/libexec/strom-firstboot.sh"
install -m 0644 files/etc/systemd/system/strom-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/strom-firstboot.service"

if ! grep -q 'LABEL=strom-state[[:space:]]\+/var/lib/strom' "${ROOTFS_DIR}/etc/fstab"; then
	printf '%s\n' 'LABEL=strom-state /var/lib/strom ext4 defaults,nofail,x-systemd.device-timeout=10 0 2' >> "${ROOTFS_DIR}/etc/fstab"
fi

ln -snf /etc/systemd/system/strom-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/strom-firstboot.service"
