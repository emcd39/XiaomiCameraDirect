package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
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

func TestSessionDiagnosticsDryRunPlansNamedOperationWithoutRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	report, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, IncludeInventory: true, CameraID: "xiaomi_0123456789abcdef",
		IncludeSessionDiagnostics: true, SessionOperation: backend.DiagnosticTransportMetadata,
		SessionGeneration: "gen_0123456789abcdef0123456789abcdef", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || report.Mode != "dry_run" || len(report.Operations) != 4 ||
		!strings.HasSuffix(report.Operations[3], "/diagnostics/transport_metadata") {
		t.Fatalf("session dry-run made requests or omitted named operation: requests=%d report=%#v", requests.Load(), report)
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
			_, _ = w.Write([]byte(`{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Front door","model":"xiaomi.camera.c201","model_id":"xiaomi.camera.c201","firmware":"5.3.2_0016","selected":true,"capabilities":{"video":true,"snapshot":true,"audio":false,"ptz":false,"presets":false},"video":{"state":"idle"},"control":{"state":"unsupported"},"support":{"schema_version":1,"features":{"camera_settings_read":{"implementation":"implemented","validation":"offline","path":"cloud","availability":"unknown","reason":"unverified_firmware","requires_cloud":["account_auth","inventory","settings_read"],"evidence_ref":"test#safe"}}},"streams":[{"id":"main","protocol":"rtsp","url":"rtsp://camera-user:camera-secret@10.0.0.44:554/live"}]}]}`))
		case "/v2/cameras/xiaomi_0123456789abcdef/diagnostics/session_status":
			_, _ = w.Write([]byte(`{"operation":"session_status","correlation_id":"diag_0123456789abcdef0123456789abcdef","generation":"gen_0123456789abcdef0123456789abcdef","state":"streaming","events":[{"sequence":1,"name":"session_started","result":"ok","generation":"gen_0123456789abcdef0123456789abcdef"}]}`))
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
	if camera.Reference == "xiaomi_0123456789abcdef" || camera.Model != "xiaomi.camera.c201" || len(camera.Streams) != 1 || camera.Streams[0].DeclaredEndpointClass != "private_ip" {
		t.Fatalf("camera summary did not retain safe diagnostics: %#v", camera)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"xiaomi_0123456789abcdef", "Front door", "camera-user", "camera-secret", "10.0.0.44", "rtsp://", "safe-test-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("report leaked %q: %s", secret, encoded)
		}
	}
}

func TestCollectReadsOnlyExplicitExistingSessionDiagnostics(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected request method %s", r.Method)
		}
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v2/info":
			_, _ = w.Write([]byte(`{"api_version":"2.0","features":["camera_session_diagnostics"]}`))
		case "/v2/cameras":
			_, _ = w.Write([]byte(`{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Private name","model":"xiaomi.camera.c201","selected":true,"capabilities":{"video":true,"snapshot":true,"audio":false,"ptz":false,"presets":false},"video":{"state":"idle"},"control":{"state":"unsupported"},"streams":[]}]}`))
		case "/v2/cameras/xiaomi_0123456789abcdef/diagnostics/session_status":
			_, _ = w.Write([]byte(`{"operation":"session_status","correlation_id":"diag_0123456789abcdef0123456789abcdef","generation":"gen_0123456789abcdef0123456789abcdef","state":"streaming","events":[{"sequence":1,"name":"session_started","result":"ok","generation":"gen_0123456789abcdef0123456789abcdef"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, Token: "test-token", IncludeInventory: true,
		CameraID: "xiaomi_0123456789abcdef", IncludeSessionDiagnostics: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/healthz", "/v2/info", "/v2/cameras", "/v2/cameras/xiaomi_0123456789abcdef/diagnostics/session_status"}
	if !reflect.DeepEqual(requested, want) {
		t.Fatalf("GET sequence = %#v, want %#v", requested, want)
	}
	if len(report.Cameras) != 1 || report.Cameras[0].SessionDiagnosticsState != "available" || report.Cameras[0].Session == nil {
		t.Fatalf("session diagnostics missing from report: %#v", report.Cameras)
	}
	if report.Cameras[0].Session.Operation != backend.DiagnosticSessionStatus || report.Cameras[0].Session.CorrelationID == "" || report.Cameras[0].Session.Generation == "" || report.Cameras[0].Session.State != backend.SessionStreaming || len(report.Cameras[0].Session.Events) != 1 {
		t.Fatalf("sanitized session snapshot = %#v", report.Cameras[0].Session)
	}
}

func TestCollectTransportMetadataRequiresAndUsesCurrentGeneration(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.RequestURI())
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v2/info":
			_, _ = w.Write([]byte(`{"api_version":"2.0","features":["camera_session_diagnostics"]}`))
		case "/v2/cameras":
			_, _ = w.Write([]byte(`{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Camera","model":"xiaomi.camera.c201","capabilities":{"video":true,"snapshot":true},"video":{"state":"idle"},"control":{"state":"unsupported"},"streams":[]}]}`))
		case "/v2/cameras/xiaomi_0123456789abcdef/diagnostics/transport_metadata":
			if got := r.URL.Query().Get("generation"); got != "gen_0123456789abcdef0123456789abcdef" {
				t.Errorf("transport operation generation=%q", got)
			}
			_, _ = w.Write([]byte(`{"operation":"transport_metadata","correlation_id":"diag_0123456789abcdef0123456789abcdef","generation":"gen_0123456789abcdef0123456789abcdef","state":"streaming","protocol":"cs2+tcp","peer_class":"private_ip","frames":17,"bytes":4096,"events":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, Token: "safe-token", IncludeInventory: true,
		CameraID: "xiaomi_0123456789abcdef", IncludeSessionDiagnostics: true,
		SessionOperation: backend.DiagnosticTransportMetadata,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("transport metadata without generation = %v", err)
	}
	requestGeneration := "gen_0123456789abcdef0123456789abcdef"
	report, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: server.URL, Token: "safe-token", IncludeInventory: true,
		CameraID: "xiaomi_0123456789abcdef", IncludeSessionDiagnostics: true,
		SessionOperation: backend.DiagnosticTransportMetadata, SessionGeneration: requestGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "/v2/cameras/xiaomi_0123456789abcdef/diagnostics/transport_metadata?generation=" + requestGeneration
	if len(requested) != 4 || requested[3] != want {
		t.Fatalf("request URIs = %#v, expected final URI %q", requested, want)
	}
	if report.Cameras[0].Session == nil || report.Cameras[0].Session.Operation != backend.DiagnosticTransportMetadata || report.Cameras[0].Session.Protocol != "cs2+tcp" || report.Cameras[0].Session.Frames != 17 {
		t.Fatalf("transport metadata report = %#v", report.Cameras[0].Session)
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

func TestPrivateGatewayBypassesEnvironmentProxy(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyRequests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	var gatewayRequests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayRequests.Add(1)
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v2/info":
			_, _ = w.Write([]byte(`{"api_version":"2.0","features":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	directAddress := gateway.Listener.Addr().String()
	target := "http://10.17.0.9:" + strconv.Itoa(gateway.Listener.Addr().(*net.TCPAddr).Port)
	runner := newRunnerWithDialContext(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, directAddress)
	})
	transport, ok := runner.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("camera probe must use an explicit transport with proxy disabled")
	}
	if _, err := runner.Collect(context.Background(), Options{GatewayURL: target, Token: "test-token"}); err != nil {
		t.Fatalf("private gateway request failed: %v", err)
	}
	if proxyRequests.Load() != 0 || gatewayRequests.Load() != 2 {
		t.Fatalf("request counts proxy=%d gateway=%d, want 0 and 2", proxyRequests.Load(), gatewayRequests.Load())
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

func TestSessionDiagnosticsRequireExplicitCameraInventoryAndIdentity(t *testing.T) {
	_, err := NewRunner().Collect(context.Background(), Options{
		GatewayURL: "http://127.0.0.1:8099", Token: "test-token", IncludeSessionDiagnostics: true,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("session diagnostics without inventory/id = %v", err)
	}
}
