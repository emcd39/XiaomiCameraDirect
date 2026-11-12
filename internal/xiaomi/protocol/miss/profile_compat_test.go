package miss

import (
	"bytes"
	"testing"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/profiles"
)

func TestC201ProfileKeepsVideoStartWireCommand(t *testing.T) {
	profile := profiles.C201()
	body := mediaStartBody(profile.ModelID, profile.Channel, profile.Quality, "", profile.Audio)
	got := command(cmdVideoStart, body)
	want := append([]byte{0x00, 0x00, 0x01, 0x02}, []byte(`{"videoquality":0,"enableaudio":0}`)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("C201 video-start command = %x, want %x", got, want)
	}
}
