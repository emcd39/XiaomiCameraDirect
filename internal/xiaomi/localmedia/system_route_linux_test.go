//go:build linux

package localmedia

import (
	"context"
	"net"
	"syscall"
	"testing"
)

// This uses only kernel route selection and local socket setup. It sends no
// datagrams to the destination or to a camera.
func TestSystemRouteAndInterfaceBindingOnLoopback(t *testing.T) {
	route, err := (SystemRouteResolver{}).Lookup(context.Background(), net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("route lookup for loopback: %v", err)
	}
	if route.Interface == "" {
		t.Fatal("route lookup returned an empty interface")
	}
	listen := net.ListenConfig{Control: func(network, _ string, raw syscall.RawConn) error {
		return BindSocketToInterface(network, route.Interface, raw)
	}}
	packet, err := listen.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("bind a local UDP socket to the selected interface: %v", err)
	}
	if err := packet.Close(); err != nil {
		t.Fatalf("close the local UDP socket: %v", err)
	}
}
