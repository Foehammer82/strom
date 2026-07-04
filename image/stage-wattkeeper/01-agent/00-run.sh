#!/bin/bash -e

install -d "${ROOTFS_DIR}/usr/local/bin"
install -d "${ROOTFS_DIR}/etc/systemd/system"
install -d "${ROOTFS_DIR}/etc/udev/rules.d"
install -d "${ROOTFS_DIR}/etc/ssh/sshd_config.d"
install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"

install -m 0755 files/usr/local/bin/wattkeeper-agent "${ROOTFS_DIR}/usr/local/bin/wattkeeper-agent"
install -m 0644 files/etc/systemd/system/wattkeeper-agent.service "${ROOTFS_DIR}/etc/systemd/system/wattkeeper-agent.service"
install -m 0644 files/etc/udev/rules.d/99-wattkeeper-agent.rules "${ROOTFS_DIR}/etc/udev/rules.d/99-wattkeeper-agent.rules"
install -m 0644 files/etc/ssh/sshd_config.d/10-wattkeeper.conf "${ROOTFS_DIR}/etc/ssh/sshd_config.d/10-wattkeeper.conf"

ln -snf /etc/systemd/system/wattkeeper-agent.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/wattkeeper-agent.service"

rm -f "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/bluetooth.service"
rm -f "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/hciuart.service"
