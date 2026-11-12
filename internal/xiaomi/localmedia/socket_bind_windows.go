//go:build windows

package localmedia

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ipUnicastIf   = 31
	ipv6UnicastIf = 31
)

func BindSocketToInterface(network, interfaceName string, raw syscall.RawConn) error {
	if raw == nil || strings.TrimSpace(interfaceName) == "" {
		return ErrLocalInterfaceUnverified
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil || iface.Index <= 0 {
		return ErrLocalInterfaceUnverified
	}
	v4 := strings.HasSuffix(network, "4")
	v6 := strings.HasSuffix(network, "6")
	if !v4 && !v6 {
		return ErrLocalInterfaceUnverified
	}
	var bindErr error
	if err := raw.Control(func(fd uintptr) {
		if v4 {
			// Windows requires IP_UNICAST_IF's DWORD in network byte order.
			var value [4]byte
			binary.BigEndian.PutUint32(value[:], uint32(iface.Index))
			bindErr = windows.Setsockopt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, &value[0], int32(len(value)))
			return
		}
		// IPV6_UNICAST_IF takes its interface index in host byte order.
		value := uint32(iface.Index)
		bindErr = windows.Setsockopt(windows.Handle(fd), windows.IPPROTO_IPV6, ipv6UnicastIf, (*byte)(unsafe.Pointer(&value)), int32(unsafe.Sizeof(value)))
	}); err != nil {
		return ErrLocalInterfaceUnverified
	}
	if bindErr != nil {
		return errors.Join(ErrLocalInterfaceUnverified, bindErr)
	}
	return nil
}
