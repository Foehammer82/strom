#!/bin/bash -e

install -d "${ROOTFS_DIR}/usr/local/libexec"
install -d "${ROOTFS_DIR}/etc/systemd/system"
install -d "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants"

install -m 0755 files/usr/local/libexec/wattkeeper-firstboot.sh "${ROOTFS_DIR}/usr/local/libexec/wattkeeper-firstboot.sh"
install -m 0644 files/etc/systemd/system/wattkeeper-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/wattkeeper-firstboot.service"

ln -snf /etc/systemd/system/wattkeeper-firstboot.service "${ROOTFS_DIR}/etc/systemd/system/multi-user.target.wants/wattkeeper-firstboot.service"
