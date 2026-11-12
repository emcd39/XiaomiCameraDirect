//go:build !linux && !windows

package lan

import (
	"errors"
	"net"
)

func hardwareAddr(net.IP) (net.HardwareAddr, error) {
	return nil, errors.New("lan: neighbour lookup is not supported on this platform")
}
