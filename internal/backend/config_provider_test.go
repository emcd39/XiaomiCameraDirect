package backend

import (
	"context"
	"reflect"
	"testing"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
)

func TestConfigProviderMapsStreamsAndCapabilities(t *testing.T) {
	provider := NewConfigProvider([]config.Camera{
		{
			ID: "garden", Name: "Garden", Model: "ONVIF camera",
			RTSPURL: "rtsp://streamer:8554/garden", ONVIFURL: "http://camera:80/onvif/ptz_service",
			Profile: "profile-1", PTZ: true, Step: 0.08,
		},
		{ID: "porch", Name: "Porch", RTSPURL: "rtsp://streamer:8554/porch"},
	})

	cameras, err := provider.Cameras(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cameras) != 2 {
		t.Fatalf("camera count = %d, want 2", len(cameras))
	}
	if !cameras[0].Capabilities.Video || !cameras[0].Capabilities.PTZ || cameras[0].Control.State != model.ControlReady {
		t.Fatalf("garden capabilities = %#v, control = %#v", cameras[0].Capabilities, cameras[0].Control)
	}
	if cameras[1].Capabilities.PTZ || cameras[1].Control.State != model.ControlUnsupported {
		t.Fatalf("porch capabilities = %#v, control = %#v", cameras[1].Capabilities, cameras[1].Control)
	}
	streams, err := provider.Streams(context.Background(), "garden")
	if err != nil {
		t.Fatal(err)
	}
	want := []StreamEndpoint{{ID: "main", Protocol: "rtsp", URL: "rtsp://streamer:8554/garden"}}
	if !reflect.DeepEqual(streams, want) {
		t.Fatalf("streams = %#v, want %#v", streams, want)
	}
	unknown, err := provider.Streams(context.Background(), "missing")
	if err != nil || len(unknown) != 0 {
		t.Fatalf("unknown camera streams = %#v, error = %v", unknown, err)
	}
}

func TestStreamEndpointRejectsCredentialsAndQueryData(t *testing.T) {
	for _, source := range []string{
		"rtsp://user:secret@streamer:8554/camera",
		"rtsp://streamer:8554/camera?token=private",
		"http://streamer/camera",
	} {
		if err := (StreamEndpoint{ID: "main", Protocol: "rtsp", URL: source}).Validate(); err == nil {
			t.Errorf("Validate(%q) succeeded, want error", source)
		}
	}
}
