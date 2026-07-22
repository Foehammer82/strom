#!/bin/bash -e

install -d "${ROOTFS_DIR}/usr/local/bin"
install -d "${ROOTFS_DIR}/usr/local/libexec"
install -d "${ROOTFS_DIR}/etc/systemd/system"
install -d "${ROOTFS_DIR}/etc/udev/rules.d"
install -d "${ROOTFS_DIR}/etc/ssh/sshd_config.d"
install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"
install -d "${ROOTFS_DIR}/etc/systemd/system/timers.target.wants"

install -m 0755 files/usr/local/libexec/strom-agent-recovery "${ROOTFS_DIR}/usr/local/libexec/strom-agent-recovery"
install -m 0755 files/usr/local/bin/strom-agent "${ROOTFS_DIR}/usr/local/bin/strom-agent"
install -m 0644 files/etc/systemd/system/strom-agent.service "${ROOTFS_DIR}/etc/systemd/system/strom-agent.service"
install -m 0644 files/etc/systemd/system/strom-ssh-access.service "${ROOTFS_DIR}/etc/systemd/system/strom-ssh-access.service"
install -m 0644 files/etc/systemd/system/strom-update-check.service "${ROOTFS_DIR}/etc/systemd/system/strom-update-check.service"
install -m 0644 files/etc/systemd/system/strom-update-check.timer "${ROOTFS_DIR}/etc/systemd/system/strom-update-check.timer"
install -m 0644 files/etc/udev/rules.d/99-strom-agent.rules "${ROOTFS_DIR}/etc/udev/rules.d/99-strom-agent.rules"
install -m 0644 files/etc/ssh/sshd_config.d/10-strom.conf "${ROOTFS_DIR}/etc/ssh/sshd_config.d/10-strom.conf"

ln -snf /etc/systemd/system/strom-agent.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/strom-agent.service"
ln -snf /etc/systemd/system/strom-ssh-access.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/strom-ssh-access.service"
ln -snf /etc/systemd/system/strom-update-check.timer "${ROOTFS_DIR}/etc/systemd/system/timers.target.wants/strom-update-check.timer"

rm -f "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/bluetooth.service"
rm -f "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/hciuart.service"
