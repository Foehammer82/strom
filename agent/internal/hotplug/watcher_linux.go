//go:build linux

package hotplug

import (
	"fmt"
	"os"
	"syscall"
)

type netlinkConn struct {
	fd int
}

func openSocketForPlatform() (messageConn, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("open netlink socket: %w", err)
	}

	address := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: 1,
		Pid:    uint32(os.Getpid()),
	}
	if err := syscall.Bind(fd, address); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind netlink socket: %w", err)
	}

	return &netlinkConn{fd: fd}, nil
}

func (c *netlinkConn) ReadMessage() ([]byte, error) {
	buffer := make([]byte, 4096)
	n, _, err := syscall.Recvfrom(c.fd, buffer, 0)
	if err != nil {
		return nil, err
	}
	return buffer[:n], nil
}

func (c *netlinkConn) Close() error {
	return syscall.Close(c.fd)
}