//go:build windows

package lan

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

var (
	iphlpapi    = syscall.NewLazyDLL("iphlpapi.dll")
	procSendARP = iphlpapi.NewProc("SendARP")
)

// hardwareAddr resolves a neighbour already learned from a discovery reply.
func hardwareAddr(ip net.IP) (net.HardwareAddr, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("lan: %s is not IPv4", ip)
	}
	dest := binary.LittleEndian.Uint32(ip4)
	var mac [8]byte
	length := uint32(len(mac))
	r, _, _ := procSendARP.Call(
		uintptr(dest), 0,
		uintptr(unsafe.Pointer(&mac[0])),
		uintptr(unsafe.Pointer(&length)))
	if r != 0 {
		return nil, fmt.Errorf("lan: SendARP failed with code %d", r)
	}
	if length == 0 || length > uint32(len(mac)) {
		return nil, fmt.Errorf("lan: SendARP returned %d bytes", length)
	}
	return net.HardwareAddr(mac[:length]), nil
}
