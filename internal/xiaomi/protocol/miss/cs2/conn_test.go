package cs2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestWriteUntilAcceptsFourByteP2PReady(t *testing.T) {
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	go func() {
		buf := make([]byte, 64)
		_, remote, readErr := peer.ReadFromUDP(buf)
		if readErr != nil {
			return
		}
		// The malformed length must be counted and skipped before the valid,
		// four-byte CS2 control response is accepted.
		_, _ = peer.WriteToUDP([]byte{magic, msgP2PRdyUDP, 0, 1}, remote)
		_, _ = peer.WriteToUDP(cs2ControlPacket(msgP2PRdyTCP, 0), remote)
		_, _ = peer.WriteToUDP([]byte{magic, msgP2PRdyUDP, 0, 0}, remote)
	}()

	udp := &udpConn{UDPConn: client, addr: peer.LocalAddr().(*net.UDPAddr)}
	got, stats, err := udp.WriteUntil(
		[]byte{magic, msgPunchPkt, 0, 0}, nil,
		func(response []byte) bool { return response[1] == msgP2PRdyUDP },
	)
	if err != nil {
		t.Fatalf("WriteUntil rejected the ready packet: %v", err)
	}
	if !bytes.Equal(got, []byte{magic, msgP2PRdyUDP, 0, 0}) {
		t.Fatalf("ready response = %x", got)
	}
	if stats.malformed != 1 || stats.unmatched != 1 || stats.unmatchedTypes[msgP2PRdyTCP] != 1 {
		t.Fatalf("rejected response counts = malformed:%d unmatched:%d", stats.malformed, stats.unmatched)
	}
}

func TestHandshakeGetsFullTimeoutForEachPhase(t *testing.T) {
	oldTimeout := HandshakeTimeout
	HandshakeTimeout = 250 * time.Millisecond
	t.Cleanup(func() { HandshakeTimeout = oldTimeout })

	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1200)
		n, remote, readErr := peer.ReadFromUDP(buf)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		if n != 4 || buf[1] != msgLanSearch {
			serverErr <- errors.New("expected LAN-search packet")
			return
		}
		time.Sleep(175 * time.Millisecond)
		_, _ = peer.WriteToUDP(cs2ControlPacket(msgPunchPkt, 20), remote)

		n, remote, readErr = peer.ReadFromUDP(buf)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		if n < 2 || buf[1] != msgPunchPkt {
			serverErr <- errors.New("expected punch packet")
			return
		}
		time.Sleep(175 * time.Millisecond)
		_, writeErr := peer.WriteToUDP(cs2ControlPacket(msgP2PRdyUDP, 12), remote)
		serverErr <- writeErr
	}()

	started := time.Now()
	conn, err := handshakeOnPort("127.0.0.1", peer.LocalAddr().(*net.UDPAddr).Port, "udp")
	if err != nil {
		t.Fatalf("handshake failed although each reply arrived within its phase timeout: %v", err)
	}
	defer conn.Close()
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("handshake elapsed %s; expected both delayed phases", elapsed)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("local UDP peer: %v", err)
	}
}

func TestUDPReadRejectsForeignIPv4DatagramLargerThanEightBytes(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if err := receiver.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	attacker, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	conn := &udpConn{UDPConn: receiver, addr: peer.LocalAddr().(*net.UDPAddr)}
	if samePeerIP(attacker.LocalAddr().(*net.UDPAddr).IP, conn.addr.IP) {
		t.Fatal("test attacker unexpectedly has the expected peer IP")
	}

	sendErr := make(chan error, 1)
	go func() {
		target := receiver.LocalAddr().(*net.UDPAddr)
		if _, err := attacker.WriteToUDP([]byte("foreign-peer-datagram"), target); err != nil {
			sendErr <- err
			return
		}
		// Ensure the foreign packet reaches Read before the valid peer packet.
		time.Sleep(20 * time.Millisecond)
		_, err := peer.WriteToUDP([]byte("expected-peer-data"), target)
		sendErr <- err
	}()

	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if got, want := string(buf[:n]), "expected-peer-data"; got != want {
		t.Fatalf("Read() returned %q, want %q", got, want)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("send peer datagrams: %v", err)
	}
}

func TestSamePeerIPHandlesIPv4AndIPv6Representations(t *testing.T) {
	tests := []struct {
		name     string
		got      net.IP
		expected net.IP
		want     bool
	}{
		{
			name: "ipv4 four-byte equals parsed 16-byte form",
			got:  net.IP{192, 0, 2, 10}, expected: net.ParseIP("192.0.2.10"), want: true,
		},
		{
			name: "ipv4-mapped ipv6 equals ipv4",
			got:  net.ParseIP("::ffff:192.0.2.10"), expected: net.IP{192, 0, 2, 10}, want: true,
		},
		{
			name: "compressed and expanded ipv6 are equal",
			got:  net.ParseIP("2001:db8::10"), expected: net.ParseIP("2001:0db8:0000:0000:0000:0000:0000:0010"), want: true,
		},
		{
			name: "different ipv4 rejected",
			got:  net.IP{192, 0, 2, 11}, expected: net.ParseIP("192.0.2.10"), want: false,
		},
		{
			name: "different ipv6 rejected",
			got:  net.ParseIP("2001:db8::11"), expected: net.ParseIP("2001:db8::10"), want: false,
		},
		{
			name: "different address families rejected",
			got:  net.ParseIP("2001:db8::10"), expected: net.ParseIP("192.0.2.10"), want: false,
		},
		{
			name: "nil rejected",
			got:  nil, expected: net.ParseIP("192.0.2.10"), want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := samePeerIP(test.got, test.expected); got != test.want {
				t.Fatalf("samePeerIP(%v, %v) = %t, want %t", test.got, test.expected, got, test.want)
			}
		})
	}
	if samePeerIP(net.ParseIP("2001:db8::10"), net.ParseIP("::ffff:192.0.2.10")) {
		t.Fatal("IPv6 address was confused with an IPv4-mapped address")
	}
	if !bytes.Equal(net.ParseIP("::ffff:192.0.2.10").To4(), []byte{192, 0, 2, 10}) {
		t.Fatal("test runtime did not parse the IPv4-mapped address as IPv4")
	}
}

func TestNewUDPConnUsesExplicitPeerPortAndAddressFamily(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		port   int
		family string
		wantIP net.IP
	}{
		{name: "ipv4", host: "127.0.0.1", port: 32108, family: "ipv4", wantIP: net.IPv4(127, 0, 0, 1)},
		{name: "ipv6", host: "::1", port: 32109, family: "ipv6", wantIP: net.ParseIP("::1")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			netConn, err := newUDPConn(test.host, test.port)
			if err != nil {
				t.Fatal(err)
			}
			defer netConn.Close()

			got := netConn.RemoteAddr().(*net.UDPAddr)
			if got.Port != test.port || !samePeerIP(got.IP, test.wantIP) {
				t.Fatalf("RemoteAddr() = %v, want %s:%d", got, test.wantIP, test.port)
			}
			local := netConn.LocalAddr().(*net.UDPAddr)
			switch test.family {
			case "ipv4":
				if local.IP.To4() == nil {
					t.Fatalf("IPv4 target used non-IPv4 socket %v", local)
				}
			case "ipv6":
				if local.IP.To4() != nil {
					t.Fatalf("IPv6 target used IPv4 socket %v", local)
				}
			}
		})
	}
}

func TestHandshakeAcceptsLegitimateCameraPortChange(t *testing.T) {
	oldTimeout := HandshakeTimeout
	HandshakeTimeout = time.Second
	t.Cleanup(func() { HandshakeTimeout = oldTimeout })

	control, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ephemeral, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ephemeral.Close()
	if control.LocalAddr().(*net.UDPAddr).Port == ephemeral.LocalAddr().(*net.UDPAddr).Port {
		t.Fatal("test camera sockets unexpectedly share a UDP port")
	}
	for _, conn := range []*net.UDPConn{control, ephemeral} {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 1200)
		n, client, err := control.ReadFromUDP(buf)
		if err != nil {
			serverErr <- err
			return
		}
		if n < 2 || buf[1] != msgLanSearch {
			serverErr <- errors.New("expected LAN-search packet")
			return
		}
		if _, err := ephemeral.WriteToUDP(cs2ControlPacket(msgPunchPkt, 20), client); err != nil {
			serverErr <- err
			return
		}

		n, client, err = ephemeral.ReadFromUDP(buf)
		if err != nil {
			serverErr <- err
			return
		}
		if n < 2 || buf[1] != msgPunchPkt {
			serverErr <- errors.New("expected punch on the negotiated ephemeral port")
			return
		}
		_, err = ephemeral.WriteToUDP(cs2ControlPacket(msgP2PRdyUDP, 12), client)
		serverErr <- err
	}()

	conn, err := handshakeOnPort("127.0.0.1", control.LocalAddr().(*net.UDPAddr).Port, "udp")
	if err != nil {
		t.Fatalf("handshake with camera port change failed: %v", err)
	}
	defer conn.Close()

	remote, ok := conn.RemoteAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("RemoteAddr() = %T, want *net.UDPAddr", conn.RemoteAddr())
	}
	if remote.Port != ephemeral.LocalAddr().(*net.UDPAddr).Port || !samePeerIP(remote.IP, net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("negotiated remote = %s, want camera IP at ephemeral port %d", remote, ephemeral.LocalAddr().(*net.UDPAddr).Port)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("fake camera handshake: %v", err)
	}
}

func cs2ControlPacket(kind byte, payloadBytes int) []byte {
	packet := make([]byte, 4+payloadBytes)
	packet[0], packet[1] = magic, kind
	binary.BigEndian.PutUint16(packet[2:4], uint16(payloadBytes))
	return packet
}
