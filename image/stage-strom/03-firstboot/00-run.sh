#!/bin/bash -e

install -d "${ROOTFS_DIR}/usr/local/libexec"
install -d "${ROOTFS_DIR}/etc/systemd/system"
install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"

install -m 0755 files/usr/local/libexec/strom-firstboot.sh "${ROOTFS_DIR}/usr/local/libexec/strom-firstboot.sh"
install -m 0644 files/etc/systemd/system/strom-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/strom-firstboot.service"

ln -snf /etc/systemd/system/strom-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/strom-firstboot.service"
