//go:build linux

package localmedia

import (
	"errors"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func BindSocketToInterface(network, interfaceName string, raw syscall.RawConn) error {
	if raw == nil || strings.TrimSpace(interfaceName) == "" || !validSocketNetwork(network) {
		return ErrLocalInterfaceUnverified
	}
	var bindErr error
	if err := raw.Control(func(fd uintptr) {
		bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
	}); err != nil {
		return ErrLocalInterfaceUnverified
	}
	if bindErr != nil {
		return errors.Join(ErrLocalInterfaceUnverified, bindErr)
	}
	return nil
}

func validSocketNetwork(network string) bool {
	return strings.HasSuffix(network, "4") || strings.HasSuffix(network, "6")
}
