//go:build !linux

package hotplug

import "errors"

type unsupportedConn struct{}

func openSocketForPlatform() (messageConn, error) {
	return &unsupportedConn{}, nil
}

func (unsupportedConn) ReadMessage() ([]byte, error) {
	return nil, errors.New("hotplug watcher is only supported on linux")
}

func (unsupportedConn) Close() error {
	return nil
}
