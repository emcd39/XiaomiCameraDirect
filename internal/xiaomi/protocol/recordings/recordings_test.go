package recordings

import (
	"encoding/binary"
	"testing"
)

func TestFileRequestEncodesExactTimestampAndNonzeroChannel(t *testing.T) {
	const timestamp uint32 = 1_794_468_000
	const channel uint32 = 10

	payload := FileRequest(timestamp, channel)
	if len(payload) <= 19 {
		t.Fatalf("file request payload length = %d, want > 19 so firmware reads channel", len(payload))
	}
	if got := binary.LittleEndian.Uint32(payload[0:4]); got != timestamp {
		t.Fatalf("file request timestamp = %d, want exact index start %d", got, timestamp)
	}
	if got := binary.LittleEndian.Uint32(payload[8:12]); got != channel {
		t.Fatalf("file request channel = %d, want %d", got, channel)
	}
	for i, b := range payload[4:8] {
		if b != 0 {
			t.Fatalf("reserved timestamp bytes [%d] = %d, want zero", i+4, b)
		}
	}
	for i, b := range payload[12:] {
		if b != 0 {
			t.Fatalf("reserved payload bytes [%d] = %d, want zero", i+12, b)
		}
	}
}
