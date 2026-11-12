package streamrtsp

import (
	"bytes"
	"testing"
)

func TestSplitAnnexBHandlesThreeAndFourByteStartCodes(t *testing.T) {
	input := []byte{0, 0, 0, 1, 0x40, 1, 0xaa, 0, 0, 1, 0x42, 1, 0xbb}
	got := splitAnnexB(input)
	want := [][]byte{{0x40, 1, 0xaa}, {0x42, 1, 0xbb}}
	if len(got) != len(want) {
		t.Fatalf("NALU count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("NALU %d = %x, want %x", i, got[i], want[i])
		}
	}
}

func TestPathBecomesReadyOnlyAfterParametersAndKeyFrame(t *testing.T) {
	relay, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	path, err := relay.NewPath()
	if err != nil {
		t.Fatal(err)
	}
	defer path.Close()

	if err := path.WriteAccessUnit([]byte{0, 0, 1, 0x26, 1, 0xaa}, 1, false); err != nil {
		t.Fatal(err)
	}
	if path.Ready() {
		t.Fatal("path became ready without parameter sets")
	}
	accessUnit := []byte{
		0, 0, 0, 1, 0x40, 1, 0xaa, // VPS
		0, 0, 1, 0x42, 1, 0xbb, // SPS
		0, 0, 0, 1, 0x44, 1, 0xcc, // PPS
		0, 0, 1, 0x26, 1, 0xdd, // IDR
	}
	if err := path.WriteAccessUnit(accessUnit, 2, true); err != nil {
		t.Fatal(err)
	}
	if !path.Ready() {
		t.Fatal("path did not become ready after H.265 parameters and a key frame")
	}
	if _, err := path.URL("gateway", 8554); err != nil {
		t.Fatalf("build RTSP URL: %v", err)
	}
}
