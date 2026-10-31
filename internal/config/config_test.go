package config

import "testing"

func TestConfigValidation(t *testing.T) {
	ok := Config{Cameras: []Camera{{ID: "garden", Name: "Garden", RTSPURL: "rtsp://127.0.0.1:8554/garden", ONVIFURL: "http://127.0.0.1:8891/onvif/ptz_service", PTZ: true}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Listen != "127.0.0.1:8099" || ok.Cameras[0].Step != 0.08 {
		t.Fatal("defaults not applied")
	}
	tests := []struct {
		name string
		cfg  Config
	}{
		{"duplicate", Config{Cameras: []Camera{{ID: "garden", Name: "a", RTSPURL: "rtsp://host/a"}, {ID: "garden", Name: "b", RTSPURL: "rtsp://host/b"}}}},
		{"invalid id", Config{Cameras: []Camera{{ID: "garden/a", Name: "a", RTSPURL: "rtsp://host/a"}}}},
		{"credentials blocked", Config{Cameras: []Camera{{ID: "garden", Name: "a", RTSPURL: "rtsp://user:secret@host/a"}}}},
		{"bad ptz", Config{Cameras: []Camera{{ID: "garden", Name: "a", RTSPURL: "rtsp://host/a", PTZ: true, ONVIFURL: "https://host/onvif/ptz_service"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoopback(t *testing.T) {
	for _, listen := range []string{"localhost:8099", "127.0.0.1:8099", "[::1]:8099"} {
		if !Loopback(listen) {
			t.Fatalf("expected loopback %s", listen)
		}
	}
	for _, listen := range []string{":8099", "0.0.0.0:8099", "192.168.1.20:8099"} {
		if Loopback(listen) {
			t.Fatalf("expected LAN %s", listen)
		}
	}
}
