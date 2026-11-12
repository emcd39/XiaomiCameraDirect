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

func cs2ControlPacket(kind byte, payloadBytes int) []byte {
	packet := make([]byte, 4+payloadBytes)
	packet[0], packet[1] = magic, kind
	binary.BigEndian.PutUint16(packet[2:4], uint16(payloadBytes))
	return packet
}
