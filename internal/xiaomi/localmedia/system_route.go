package localmedia

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// SystemRouteResolver asks the kernel to select a route with a connected UDP
// socket, sends no datagram, then maps the kernel-selected source address to a
// single active interface. The actual media sockets are separately pinned by
// BindSocketToInterface; ambiguous or unobservable routes fail closed.
type SystemRouteResolver struct{}

func (SystemRouteResolver) Lookup(ctx context.Context, destination net.IP) (Route, error) {
	if destination == nil || ctx == nil {
		return Route{}, ErrLocalInterfaceUnverified
	}
	network := "udp6"
	if destination.To4() != nil {
		network = "udp4"
	}
	address := net.JoinHostPort(destination.String(), strconv.Itoa(32108))
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return Route{}, ErrLocalInterfaceUnverified
	}
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	closeErr := conn.Close()
	if closeErr != nil || !ok || local == nil || local.IP == nil || local.IP.IsUnspecified() {
		return Route{}, ErrLocalInterfaceUnverified
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return Route{}, ErrLocalInterfaceUnverified
	}
	matched := ""
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.Equal(local.IP) {
				if matched != "" && matched != iface.Name {
					return Route{}, ErrLocalInterfaceUnverified
				}
				matched = iface.Name
			}
		}
	}
	if strings.TrimSpace(matched) == "" {
		return Route{}, fmt.Errorf("%w", ErrLocalInterfaceUnverified)
	}
	return Route{Interface: matched}, nil
}

// ParseAllowedNetworks accepts semicolon-separated CIDR@interface pairs.
// Interface names containing '@' are deliberately unsupported to keep the
// setting unambiguous and straightforward to audit.
func ParseAllowedNetworks(raw string) ([]AllowedNetwork, error) {
	if strings.TrimSpace(raw) == "" || len(raw) > 2048 {
		return nil, ErrInvalidPolicy
	}
	parts := strings.Split(raw, ";")
	if len(parts) == 0 || len(parts) > 16 {
		return nil, ErrInvalidPolicy
	}
	allowed := make([]AllowedNetwork, 0, len(parts))
	for _, part := range parts {
		pair := strings.SplitN(strings.TrimSpace(part), "@", 2)
		if len(pair) != 2 || strings.TrimSpace(pair[0]) != pair[0] || strings.TrimSpace(pair[1]) != pair[1] || pair[1] == "" {
			return nil, ErrInvalidPolicy
		}
		if _, _, err := net.ParseCIDR(pair[0]); err != nil {
			return nil, ErrInvalidPolicy
		}
		allowed = append(allowed, AllowedNetwork{CIDR: pair[0], Interface: pair[1]})
	}
	return allowed, nil
}
