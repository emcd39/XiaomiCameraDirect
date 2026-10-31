package httpapi

import (
	"context"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePTZ struct{ calls []string }

func (p *fakePTZ) Step(_ context.Context, _, _, dir string, _ float64) error {
	p.calls = append(p.calls, "step:"+dir)
	return nil
}
func (p *fakePTZ) Stop(_ context.Context, _, _ string) error {
	p.calls = append(p.calls, "stop")
	return nil
}
func (p *fakePTZ) Preset(_ context.Context, _, _, token string) error {
	p.calls = append(p.calls, "preset:"+token)
	return nil
}

func TestAuthPTZAndFrigate(t *testing.T) {
	cfg := config.Config{Cameras: []config.Camera{{ID: "garden", Name: "Garden", RTSPURL: "rtsp://camera:8554/garden", ONVIFURL: "http://camera:8891/onvif/ptz_service", PTZ: true}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	fake := &fakePTZ{}
	server := httptest.NewServer(New(cfg, "secret", fake))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/v1/cameras", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("unauth status %d", resp.StatusCode)
	}
	resp.Body.Close()
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("authorized status %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ = http.NewRequest("POST", server.URL+"/v1/cameras/garden/ptz/step", strings.NewReader(`{"direction":"left"}`))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("step status %d", resp.StatusCode)
	}
	resp.Body.Close()
	if len(fake.calls) != 1 || fake.calls[0] != "step:left" {
		t.Fatalf("unexpected calls %v", fake.calls)
	}
	req, _ = http.NewRequest("GET", server.URL+"/v1/frigate/config", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rtsp://127.0.0.1:8554/garden") {
		t.Fatal("bad frigate config")
	}
}
