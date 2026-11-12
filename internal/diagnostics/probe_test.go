package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDryRunMakesNoRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	report, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, IncludeInventory: true, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("dry-run sent %d network requests", requests.Load())
	}
	if report.Mode != "dry_run" || len(report.Operations) != 3 {
		t.Fatalf("dry-run report = %#v", report)
	}
}

func TestCollectUsesOnlyReadOnlyEndpointsAndRedactsCameraData(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v2/info":
			if r.Header.Get("Authorization") != "Bearer safe-test-token" {
				t.Errorf("authorization header missing")
			}
			_, _ = w.Write([]byte(`{"api_version":"2.0","features":["camera_selection","camera_settings_read"]}`))
		case "/v2/cameras":
			if r.Header.Get("Authorization") != "Bearer safe-test-token" {
				t.Errorf("authorization header missing")
			}
			_, _ = w.Write([]byte(`{"cameras":[{"id":"raw-account-camera-id","name":"Front door","model":"xiaomi.camera.c201","model_id":"xiaomi.camera.c201","firmware":"5.3.2_0016","selected":true,"capabilities":{"video":true,"snapshot":true,"audio":false,"ptz":false,"presets":false},"video":{"state":"idle"},"control":{"state":"unsupported"},"support":{"schema_version":1,"features":{"camera_settings_read":{"implementation":"implemented","validation":"offline","path":"cloud","requires_cloud":["account_auth","inventory","settings_read"],"evidence_ref":"test#safe"}}},"streams":[{"id":"main","protocol":"rtsp","url":"rtsp://camera-user:camera-secret@10.0.0.44:554/live"}]}]}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, Token: "safe-test-token", IncludeInventory: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 || report.Health != "ok" || report.API.State != "available" || len(report.Cameras) != 1 {
		t.Fatalf("unexpected report or request count: requests=%d report=%#v", requests.Load(), report)
	}
	camera := report.Cameras[0]
	if camera.Reference == "raw-account-camera-id" || camera.Model != "xiaomi.camera.c201" || len(camera.Streams) != 1 || camera.Streams[0].DeclaredEndpointClass != "private_ip" {
		t.Fatalf("camera summary did not retain safe diagnostics: %#v", camera)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"raw-account-camera-id", "Front door", "camera-user", "camera-secret", "10.0.0.44", "rtsp://", "safe-test-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("report leaked %q: %s", secret, encoded)
		}
	}
}

func TestInvalidOrPublicGatewayTargetStopsBeforeNetwork(t *testing.T) {
	for _, target := range []string{"http://203.0.113.4:8099", "http://user:secret@127.0.0.1:8099", "https://gateway.example.invalid"} {
		t.Run(target, func(t *testing.T) {
			_, err := NewRunner().Collect(context.Background(), Options{GatewayURL: target, Token: "test-token"})
			if !errors.Is(err, ErrUnsafeTarget) && !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
}

func TestCameraFilterRequiresExplicitInventory(t *testing.T) {
	_, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: "http://127.0.0.1:8099", Token: "test-token", CameraID: "camera-a",
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("camera filter without inventory = %v", err)
	}
}
