//go:build linux

package lan

import (
	"bufio"
	"errors"
	"net"
	"os"
	"strings"
)

// hardwareAddr reads a neighbour entry populated by the discovery datagram;
// it does not generate an additional network packet.
func hardwareAddr(ip net.IP) (net.HardwareAddr, error) {
	want := ip.To4()
	if want == nil {
		return nil, errors.New("lan: address is not IPv4")
	}
	file, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() { // header
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 6 || fields[0] != want.String() || fields[2] == "0x0" {
				continue
			}
			mac, err := net.ParseMAC(fields[3])
			if err == nil {
				return mac, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("lan: neighbour entry not found")
}
