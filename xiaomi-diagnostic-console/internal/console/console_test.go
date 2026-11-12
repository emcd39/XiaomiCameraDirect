package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testCfg() Config {
	return Config{Listen: "127.0.0.1:18880", Mock: true, GatewayURL: "http://127.0.0.1:49320", RecordDir: "recordings"}
}

func TestServerFallsBackFromBusyLoopbackPortWithoutStoppingOwner(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	preferred := occupied.Addr().String()

	cfg := testCfg()
	cfg.Listen = preferred
	cfg.ListenFallback = true
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	runDone := false
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		if !runDone {
			select {
			case <-done:
			case <-ctx.Done():
			}
		}
	}()

	select {
	case <-s.listenReady:
	case err := <-done:
		runDone = true
		t.Fatalf("server exited before binding: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not bind a loopback address")
	}
	actual := s.cfg.Listen
	if actual == preferred {
		t.Fatalf("server reused occupied address %q", actual)
	}
	actualHost, _, err := net.SplitHostPort(actual)
	if err != nil || actualHost != "127.0.0.1" {
		t.Fatalf("fallback address is not IPv4 loopback: %q (%v)", actual, err)
	}

	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://"+actual+"/api/status", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mock":true`) {
		t.Fatalf("handler rejected the bound fallback address: HTTP %d: %s", response.Code, response.Body.String())
	}

	if probe, err := net.Listen("tcp", preferred); err == nil {
		_ = probe.Close()
		t.Fatal("original listener was stopped or disturbed")
	} else if !isAddressInUse(err) {
		t.Fatalf("could not verify original listener still owns its port: %v", err)
	}
}

func TestListenConfiguredKeepsFreePreferredLoopbackAddress(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	preferred := probe.Addr().String()
	_ = probe.Close()

	listener, err := listenConfigured(preferred, true)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.Addr().String() != preferred {
		t.Fatalf("free preferred address changed: got %q want %q", listener.Addr(), preferred)
	}
}

func req(s *Server, method, path, body string, action bool) *httptest.ResponseRecorder {
	h, _ := s.Handler()
	request := httptest.NewRequest(method, "http://127.0.0.1:18880"+path, strings.NewReader(body))
	if action {
		request.Header.Set("X-XLG-Action", "1")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	return w
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEmbeddedUIAssetsServedThroughHandler(t *testing.T) {
	s, err := NewServer(testCfg())
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, contentType, marker string
	}{
		{"/", "text/html", `<script src="/app.js" defer>`},
		{"/app.css", "text/css", "[hidden]{display:none!important}"},
		{"/app.js", "javascript", "async function request"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18880"+tc.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s returned HTTP %d: %s", tc.path, w.Code, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); !strings.Contains(got, tc.contentType) {
				t.Fatalf("GET %s content type = %q, want substring %q", tc.path, got, tc.contentType)
			}
			if !strings.Contains(w.Body.String(), tc.marker) {
				t.Fatalf("GET %s response missing expected marker %q", tc.path, tc.marker)
			}
		})
	}
}

func TestMockInventoryRefreshThroughHandler(t *testing.T) {
	s, err := NewServer(testCfg())
	if err != nil {
		t.Fatal(err)
	}
	before := req(s, http.MethodGet, "/api/status", "", false)
	if before.Code != http.StatusOK {
		t.Fatalf("initial status returned HTTP %d", before.Code)
	}
	var status struct {
		Gateway GatewayStatus `json:"gateway"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Gateway.Inventory.Loaded {
		t.Fatal("mock inventory loaded automatically at startup")
	}

	refresh := req(s, http.MethodPost, "/api/inventory/refresh", "", true)
	if refresh.Code != http.StatusOK {
		t.Fatalf("mock inventory refresh returned HTTP %d: %s", refresh.Code, refresh.Body.String())
	}
	var inventory InventorySummary
	if err := json.Unmarshal(refresh.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if !inventory.Loaded || inventory.CameraCount != 1 || inventory.C201Count != 1 {
		t.Fatalf("unexpected mock inventory: %+v", inventory)
	}
	after := req(s, http.MethodGet, "/api/status", "", false)
	if after.Code != http.StatusOK {
		t.Fatalf("final status returned HTTP %d", after.Code)
	}
	if err := json.Unmarshal(after.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Gateway.Inventory.Loaded || status.Gateway.Inventory.C201Count != 1 {
		t.Fatalf("status did not retain mock inventory: %+v", status.Gateway.Inventory)
	}
}

func TestConfigRejectsRemoteListenersAndGateway(t *testing.T) {
	cfg := testCfg()
	for _, listen := range []string{"0.0.0.0:18880", "192.168.2.1:18880", ":18880"} {
		cfg.Listen = listen
		if cfg.Validate() == nil {
			t.Fatalf("allowed nonloopback listen %q", listen)
		}
	}
	cfg = testCfg()
	cfg.GatewayURL = "http://192.168.2.124:49320"
	if cfg.Validate() == nil {
		t.Fatal("allowed nonloopback API gateway")
	}
	cfg = testCfg()
	cfg.GatewayURL = "http://127.0.0.1:49320/evil"
	if cfg.Validate() == nil {
		t.Fatal("allowed path on API gateway")
	}
}
func TestMockLeftPTZNeedsActionHeaderAndSendsNoGatewayRequest(t *testing.T) {
	s, _ := NewServer(testCfg())
	var gatewayCalls atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gatewayCalls.Add(1)
		return nil, errors.New("mock unexpectedly called gateway")
	})
	missing := req(s, "POST", "/api/ptz/left", `{}`, false)
	if missing.Code != 403 {
		t.Fatalf("expected action header 403, got %d", missing.Code)
	}
	good := req(s, "POST", "/api/ptz/left", `{}`, true)
	if good.Code != 200 {
		t.Fatalf("mock PTZ response %d: %s", good.Code, good.Body.String())
	}
	var r PTZResult
	if err := json.Unmarshal(good.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "simulated" || r.Motion != "not_measured" || r.HTTPWriteStatus != "simulated_only" {
		t.Fatalf("mock improperly implies movement: %+v", r)
	}
	if gatewayCalls.Load() != 0 {
		t.Fatalf("mock made %d gateway requests", gatewayCalls.Load())
	}
}
func TestOriginAndHostRejected(t *testing.T) {
	s, _ := NewServer(testCfg())
	h, _ := s.Handler()
	rq := httptest.NewRequest("POST", "http://127.0.0.1:18880/api/video/start", nil)
	rq.Header.Set("X-XLG-Action", "1")
	rq.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, rq)
	if w.Code != 403 {
		t.Fatalf("foreign origin %d", w.Code)
	}
	rq = httptest.NewRequest("GET", "http://malicious.example/api/status", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rq)
	if w.Code != 403 {
		t.Fatalf("host spoof %d", w.Code)
	}
}
func TestInventoryNotPolledAndExplicitRefreshOnce(t *testing.T) {
	var health, info, inventory atomic.Int32
	cfg := testCfg()
	cfg.Mock = false
	cfg.GatewayURL = "http://127.0.0.1:49320"
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("fake-test-token"), 0600)
	cfg.TokenFile = tok
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := ""
		switch r.URL.Path {
		case "/healthz":
			health.Add(1)
		case "/v2/info":
			info.Add(1)
		case "/v2/cameras":
			inventory.Add(1)
			body = `{"cameras":[{"model":"xiaomi.camera.c201","id":"xiaomi_0123456789abcdef","name":"Test C201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	w := req(s, "GET", "/api/status", "", false)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if inventory.Load() != 0 || health.Load() != 1 || info.Load() != 1 {
		t.Fatalf("status calls: inventory=%d health=%d info=%d response=%s", inventory.Load(), health.Load(), info.Load(), w.Body.String())
	}
	w = req(s, "POST", "/api/inventory/refresh", "", true)
	if w.Code != 200 {
		t.Fatalf("inventory %d %s", w.Code, w.Body.String())
	}
	if inventory.Load() != 1 {
		t.Fatalf("expected one inventory request; got %d", inventory.Load())
	}
	url, e := s.gateway.RTSPURL()
	if e != nil || !strings.HasPrefix(url, "rtsp://127.0.0.1:") {
		t.Fatalf("expected local RTSP; got err %v", e)
	}
}
func TestVideoMockOneWorker(t *testing.T) {
	s, _ := NewServer(testCfg())
	w := req(s, "POST", "/api/video/start", "", true)
	if w.Code != 200 {
		t.Fatalf("start %d", w.Code)
	}
	defer s.video.Stop()
	w = req(s, "POST", "/api/video/start", "", true)
	if w.Code != 409 {
		t.Fatalf("parallel preview started: %d", w.Code)
	}
	deadline := time.After(3 * time.Second)
	for {
		b, seq := s.video.Latest()
		if seq > 0 {
			if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
				t.Fatal("invalid mock jpeg")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("mock stream timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	w = req(s, "POST", "/api/video/stop", "", true)
	if w.Code != 200 {
		t.Fatalf("stop %d", w.Code)
	}
}
func TestJPEGFramingAndBound(t *testing.T) {
	input := []byte{0, 2, 0xff, 0xd8, 1, 2, 0xff, 0xd9, 0xff, 0xd8, 3, 0xff, 0xd9}
	r := bufio.NewReader(bytes.NewReader(input))
	one, e := readJPEGFrame(r, 100)
	if e != nil || len(one) != 6 {
		t.Fatalf("first frame %v: %v", one, e)
	}
	two, e := readJPEGFrame(r, 100)
	if e != nil || len(two) != 5 {
		t.Fatalf("second frame %v: %v", two, e)
	}
	_, e = readJPEGFrame(bufio.NewReader(bytes.NewReader([]byte{0xff, 0xd8, 1, 2, 3, 4, 5})), 5)
	if e == nil {
		t.Fatal("JPEG size limit not enforced")
	}
}
func TestRecordingPathSanitizer(t *testing.T) {
	for _, name := range []string{"../c201_20261111_120000.mkv", "other.mkv", "c201_20261111_120000.mkv/../x", "c201_abc.mkv"} {
		if validRecordingFilename(name) {
			t.Fatalf("unsafe recording accepted %q", name)
		}
	}
	if !validRecordingFilename("c201_20261111_120000.mkv") {
		t.Fatal("valid recording rejected")
	}
}
func TestPTZCannotRunWithoutSelectedCamera(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	p := &PTZ{}
	_, err := p.RunOnce(cfg, newGateway(cfg), "", "left")
	if err == nil {
		t.Fatal("live PTZ ran without a selected camera")
	}
	if p.Busy() {
		t.Fatal("PTZ busy after rejection")
	}
}

func TestPTZLeftSendsOneAuthenticatedGatewayStep(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	tokenPath := filepath.Join(t.TempDir(), "token")
	const token = "test-token-never-returned"
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenPath
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var inventoryCalls, writes atomic.Int32
	var gotAuthorization, gotPath, gotBody string
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		if r.URL.Path == "/v2/cameras" {
			inventoryCalls.Add(1)
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
		}
		if r.URL.Path == "/v2/cameras/xiaomi_0123456789abcdef/ptz/step" {
			writes.Add(1)
			gotAuthorization = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			gotBody = string(body)
			return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
		}
		return jsonResponse(r, http.StatusNotFound, `{}`), nil
	})
	if response := req(s, http.MethodPost, "/api/inventory/refresh", "", true); response.Code != http.StatusOK {
		t.Fatalf("inventory refresh HTTP %d: %s", response.Code, response.Body.String())
	}
	response := req(s, http.MethodPost, "/api/ptz/left", `{}`, true)
	if response.Code != http.StatusOK {
		t.Fatalf("left step HTTP %d: %s", response.Code, response.Body.String())
	}
	if inventoryCalls.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("gateway requests inventory=%d step=%d, want exactly 1 inventory and 1 step", inventoryCalls.Load(), writes.Load())
	}
	if gotAuthorization != "Bearer "+token || gotPath != "/v2/cameras/xiaomi_0123456789abcdef/ptz/step" || gotBody != `{"direction":"left"}` {
		t.Fatalf("gateway request auth/path/body = %q / %q / %q", gotAuthorization, gotPath, gotBody)
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("gateway token leaked into console response")
	}
	var result PTZResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.HTTPWriteStatus != "sent" || result.Reply113 != "not_measured" || result.Motion != "not_measured" {
		t.Fatalf("unexpected result claim: %+v", result)
	}
}

func TestPTZStepSendsOneGatewayPostForEachDirection(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("step-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var inventoryCalls atomic.Int32
	var gotDirections []string
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/cameras" {
			inventoryCalls.Add(1)
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v2/cameras/xiaomi_0123456789abcdef/ptz/step" {
			return jsonResponse(r, http.StatusNotFound, `{}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer step-test-token" {
			t.Errorf("missing gateway authentication")
		}
		var body struct {
			Direction string `json:"direction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode step body: %v", err)
		}
		gotDirections = append(gotDirections, body.Direction)
		return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
	})
	if response := req(s, http.MethodPost, "/api/inventory/refresh", "", true); response.Code != http.StatusOK {
		t.Fatalf("inventory refresh HTTP %d: %s", response.Code, response.Body.String())
	}
	wantDirections := []string{"left", "right", "up", "down"}
	for _, direction := range wantDirections {
		response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"xiaomi_0123456789abcdef","direction":"`+direction+`"}`, true)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"http_write_status":"sent"`) {
			t.Fatalf("%s step HTTP %d: %s", direction, response.Code, response.Body.String())
		}
	}
	if inventoryCalls.Load() != 1 || !reflect.DeepEqual(gotDirections, wantDirections) {
		t.Fatalf("gateway inventory calls/directions = %d/%v", inventoryCalls.Load(), gotDirections)
	}
}

func TestPTZStepRejectsCameraSelectionChangeDuringPreparation(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("selection-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraA = "xiaomi_1111111111111111"
	const cameraB = "xiaomi_2222222222222222"
	inventoryJSON := `{"cameras":[` +
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	var inventory managedInventory
	initialInventoryJSON := strings.ReplaceAll(inventoryJSON, `"state":"streaming"`, `"state":"starting"`)
	if err := json.Unmarshal([]byte(initialInventoryJSON), &inventory); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(inventory)
	if err := s.gateway.SelectCamera(cameraA); err != nil {
		t.Fatal(err)
	}

	var changeSelection atomic.Bool
	changeSelection.Store(true)
	var gatewayPosts atomic.Int32
	var postedPath string
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/cameras" {
			if changeSelection.CompareAndSwap(true, false) {
				if err := s.gateway.SelectCamera(cameraB); err != nil {
					t.Errorf("select concurrent camera B: %v", err)
				}
			}
			return jsonResponse(r, http.StatusOK, inventoryJSON), nil
		}
		if strings.HasSuffix(r.URL.Path, "/ptz/step") {
			gatewayPosts.Add(1)
			postedPath = r.URL.Path
			return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
		}
		return jsonResponse(r, http.StatusNotFound, `{}`), nil
	})

	response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraA+`","direction":"up"}`, true)
	if response.Code != http.StatusConflict {
		t.Fatalf("selection race HTTP %d, want 409: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "camera selection changed") || !strings.Contains(response.Body.String(), "no request was sent") {
		t.Fatalf("selection race did not return a clear no-write error: %s", response.Body.String())
	}
	if s.gateway.SelectedCameraID() != cameraB {
		t.Fatalf("selection after simulated concurrent change = %q, want camera B", s.gateway.SelectedCameraID())
	}
	if gatewayPosts.Load() != 0 || postedPath != "" {
		t.Fatalf("selection race made %d PTZ writes to %q; want no write to either camera", gatewayPosts.Load(), postedPath)
	}
}

func TestPTZGatewayFailureIsNotRetried(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ := NewServer(cfg)
	var writes atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/cameras" {
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/ptz/step") {
			writes.Add(1)
			return nil, errors.New("simulated transport failure")
		}
		return jsonResponse(r, http.StatusNotFound, `{}`), nil
	})
	if response := req(s, http.MethodPost, "/api/inventory/refresh", "", true); response.Code != http.StatusOK {
		t.Fatalf("inventory refresh HTTP %d: %s", response.Code, response.Body.String())
	}
	response := req(s, http.MethodPost, "/api/ptz/left", `{}`, true)
	if response.Code == http.StatusOK || writes.Load() != 1 {
		t.Fatalf("failed step response=%d gateway writes=%d", response.Code, writes.Load())
	}
	if strings.Contains(response.Body.String(), "private-token") {
		t.Fatal("gateway token leaked after failure")
	}
}

func jsonResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestGatewayRequiresExplicitChoiceForMultipleC201AndKeepsTheirStreamsPaired(t *testing.T) {
	cfg := testCfg()
	cfg.RTSPURLFile = filepath.Join(t.TempDir(), "legacy-rtsp-url.txt")
	if err := os.WriteFile(cfg.RTSPURLFile, []byte("rtsp://127.0.0.1:8554/camera-a"), 0600); err != nil {
		t.Fatal(err)
	}
	g := newGateway(cfg)
	var document managedInventory
	body := `{"cameras":[` +
		`{"id":"xiaomi_1111111111111111","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"xiaomi_2222222222222222","name":"C201 B","model_id":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	inv := g.applyInventory(document)
	if inv.C201Count != 2 || inv.SelectedCameraID != "" {
		t.Fatalf("multiple C201 inventory was not left unselected: %+v", inv)
	}
	if err := g.SelectCamera("xiaomi_2222222222222222"); err != nil {
		t.Fatal(err)
	}
	streamURL, err := g.RTSPURL()
	if err != nil || streamURL != "rtsp://127.0.0.1:8554/camera-b" {
		t.Fatalf("selected camera stream = %q, %v; want C201 B stream", streamURL, err)
	}
	// A refresh that removes an unambiguous ready selection must not reuse
	// either the previous camera's cached stream or the legacy static file.
	inv = g.applyInventory(document)
	if inv.SelectedCameraID != "" || inv.SessionReady {
		t.Fatalf("ambiguous refresh unexpectedly selected a camera: %+v", inv)
	}
	if streamURL, err := g.RTSPURL(); err == nil {
		t.Fatalf("RTSP URL remained available after the selection was cleared: %q", streamURL)
	}
	// A unique but unprepared camera is also not a source for a new preview.
	unprepared := document
	unprepared.Cameras = append([]managedInventoryCamera(nil), document.Cameras[:1]...)
	unprepared.Cameras[0].Video.State = "starting"
	inv = g.applyInventory(unprepared)
	if inv.SelectedCameraID == "" || inv.SessionReady {
		t.Fatalf("single unprepared C201 state is wrong: %+v", inv)
	}
	if streamURL, err := g.RTSPURL(); err == nil {
		t.Fatalf("unprepared camera reused an RTSP URL: %q", streamURL)
	}
}

func TestSelectionAndInventoryRefreshStopPreview(t *testing.T) {
	s, err := NewServer(testCfg())
	if err != nil {
		t.Fatal(err)
	}
	const cameraA = "xiaomi_1111111111111111"
	const cameraB = "xiaomi_2222222222222222"
	var document managedInventory
	body := `{"cameras":[` +
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(document)
	if err := s.gateway.SelectCamera(cameraA); err != nil {
		t.Fatal(err)
	}
	var gatewayCalls atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gatewayCalls.Add(1)
		return nil, errors.New("mock preview must not call gateway")
	})
	if response := req(s, http.MethodPost, "/api/video/start", "", true); response.Code != http.StatusOK {
		t.Fatalf("start preview HTTP %d: %s", response.Code, response.Body.String())
	}
	waitForPreviewFrame(t, s)
	if response := req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"`+cameraB+`"}`, true); response.Code != http.StatusOK {
		t.Fatalf("select camera B HTTP %d: %s", response.Code, response.Body.String())
	}
	if s.video.Status().Running {
		t.Fatal("preview for camera A remained running after selecting camera B")
	}
	if frame, _ := s.video.Latest(); len(frame) != 0 {
		t.Fatal("old camera A frame remained available after selecting camera B")
	}
	if streamURL, err := s.gateway.RTSPURL(); err != nil || streamURL != "rtsp://127.0.0.1:8554/camera-b" {
		t.Fatalf("selected camera B stream = %q, %v", streamURL, err)
	}
	if response := req(s, http.MethodPost, "/api/video/start", "", true); response.Code != http.StatusOK {
		t.Fatalf("restart preview for camera B HTTP %d: %s", response.Code, response.Body.String())
	}
	waitForPreviewFrame(t, s)
	if response := req(s, http.MethodPost, "/api/inventory/refresh", "", true); response.Code != http.StatusOK {
		t.Fatalf("refresh inventory HTTP %d: %s", response.Code, response.Body.String())
	}
	if s.video.Status().Running {
		t.Fatal("preview remained running after inventory refresh")
	}
	if frame, _ := s.video.Latest(); len(frame) != 0 {
		t.Fatal("previous preview frame remained available after inventory refresh")
	}
	if gatewayCalls.Load() != 0 {
		t.Fatalf("mock flow sent %d gateway requests", gatewayCalls.Load())
	}
}

func waitForPreviewFrame(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame, _ := s.video.Latest()
		if len(frame) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("preview worker did not produce a frame")
}

func TestPTZStepForCameraBWorksWithPreviewOff(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	const token = "camera-b-step-token"
	if err := os.WriteFile(cfg.TokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraA = "xiaomi_1111111111111111"
	const cameraB = "xiaomi_2222222222222222"
	body := `{"cameras":[` +
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	var document managedInventory
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(document)
	if err := s.gateway.SelectCamera(cameraB); err != nil {
		t.Fatal(err)
	}
	if s.video.Status().Running {
		t.Fatal("preview unexpectedly running before PTZ click")
	}
	var writes atomic.Int32
	var postedPath, postedAuthorization string
	var selectedDuringWrite string
	postEntered := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	defer func() {
		select {
		case <-releaseWrite:
		default:
			close(releaseWrite)
		}
	}()
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v2/cameras/"+cameraB+"/ptz/step" {
			writes.Add(1)
			postedPath = r.URL.Path
			postedAuthorization = r.Header.Get("Authorization")
			selectedDuringWrite = s.gateway.SelectedCameraID()
			postEntered <- struct{}{}
			<-releaseWrite
			return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
		}
		return jsonResponse(r, http.StatusNotFound, `{}`), nil
	})
	responseCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responseCh <- req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraB+`","direction":"down"}`, true)
	}()
	select {
	case <-postEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("camera B PTZ write did not start")
	}
	if s.gateway.selectionMu.TryLock() {
		s.gateway.selectionMu.Unlock()
		t.Fatal("selection lock was not held while the PTZ POST was in flight")
	}
	selectionDone := make(chan error, 1)
	go func() { selectionDone <- s.gateway.SelectCamera(cameraA) }()
	if selected := s.gateway.SelectedCameraID(); selected != cameraB {
		t.Fatalf("selection changed during camera B write: %q", selected)
	}
	close(releaseWrite)
	var response *httptest.ResponseRecorder
	select {
	case response = <-responseCh:
	case <-time.After(2 * time.Second):
		t.Fatal("camera B PTZ response did not complete")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("camera B PTZ step HTTP %d: %s", response.Code, response.Body.String())
	}
	if err := <-selectionDone; err != nil {
		t.Fatalf("select camera A after camera B write: %v", err)
	}
	if writes.Load() != 1 || postedPath != "/v2/cameras/"+cameraB+"/ptz/step" || postedAuthorization != "Bearer "+token || selectedDuringWrite != cameraB {
		t.Fatalf("preview-off PTZ writes=%d path=%q authorization=%q selected-during-write=%q", writes.Load(), postedPath, postedAuthorization, selectedDuringWrite)
	}
	if s.video.Status().Running {
		t.Fatal("PTZ step started the preview worker")
	}
}

func TestStalePTZRequestLeavesSelectedCameraAndPreviewUntouched(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("stale-selection-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraA = "xiaomi_1111111111111111"
	const cameraB = "xiaomi_2222222222222222"
	var document managedInventory
	body := `{"cameras":[` +
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(document)
	if err := s.gateway.SelectCamera(cameraB); err != nil {
		t.Fatal(err)
	}
	if err := s.video.Start(Config{Mock: true}, s.gateway); err != nil {
		t.Fatal(err)
	}
	defer s.video.Stop()
	waitForPreviewFrame(t, s)
	var gatewayCalls atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gatewayCalls.Add(1)
		return jsonResponse(r, http.StatusInternalServerError, `{}`), nil
	})
	response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraA+`","direction":"left"}`, true)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale camera A request HTTP %d, want 409: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "camera selection changed") || !strings.Contains(response.Body.String(), "no request was sent") {
		t.Fatalf("stale camera A request did not explain the conflict: %s", response.Body.String())
	}
	if s.gateway.SelectedCameraID() != cameraB {
		t.Fatalf("stale request changed selection to %q, want camera B", s.gateway.SelectedCameraID())
	}
	if !s.video.Status().Running {
		t.Fatal("stale request stopped the selected camera B preview")
	}
	if frame, _ := s.video.Latest(); len(frame) == 0 {
		t.Fatal("selected camera B preview lost its current frame")
	}
	if gatewayCalls.Load() != 0 {
		t.Fatalf("stale camera A request made %d gateway requests", gatewayCalls.Load())
	}
}

func TestPTZStepSerializesWithInventoryRefresh(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("refresh-lock-token"), 0600); err != nil {
		t.Fatal(err)
	}
	g := newGateway(cfg)
	const cameraA = "xiaomi_1111111111111111"
	const cameraB = "xiaomi_2222222222222222"
	var document managedInventory
	body := `{"cameras":[` +
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(document)
	if err := g.SelectCamera(cameraB); err != nil {
		t.Fatal(err)
	}
	postEntered := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	defer func() {
		select {
		case <-releaseWrite:
		default:
			close(releaseWrite)
		}
	}()
	var writes atomic.Int32
	g.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/cameras/"+cameraB+"/ptz/step" || r.Header.Get("Authorization") != "Bearer refresh-lock-token" {
			return jsonResponse(r, http.StatusNotFound, `{}`), nil
		}
		writes.Add(1)
		postEntered <- struct{}{}
		<-releaseWrite
		return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
	})
	stepDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := g.Step(ctx, cameraB, "left")
		stepDone <- err
	}()
	select {
	case <-postEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("camera B PTZ write did not start")
	}
	if g.selectionMu.TryLock() {
		g.selectionMu.Unlock()
		t.Fatal("selection lock was not held during the gateway PTZ POST")
	}
	refreshStarted := make(chan struct{})
	refreshDone := make(chan struct{})
	go func() {
		close(refreshStarted)
		g.applyInventory(document)
		close(refreshDone)
	}()
	<-refreshStarted
	select {
	case <-refreshDone:
		t.Fatal("inventory refresh changed selection before the PTZ POST completed")
	case <-time.After(50 * time.Millisecond):
	}
	if g.SelectedCameraID() != cameraB {
		t.Fatalf("inventory changed selection during the camera B write: %q", g.SelectedCameraID())
	}
	close(releaseWrite)
	if err := <-stepDone; err != nil {
		t.Fatalf("camera B PTZ step: %v", err)
	}
	select {
	case <-refreshDone:
	case <-time.After(2 * time.Second):
		t.Fatal("inventory refresh did not complete after the PTZ write")
	}
	if writes.Load() != 1 {
		t.Fatalf("inventory race sent %d PTZ writes, want one", writes.Load())
	}
	if g.SelectedCameraID() != "" {
		t.Fatalf("completed refresh left unexpected selection %q", g.SelectedCameraID())
	}
}

func TestRTSPHostAllowlist(t *testing.T) {
	c := testCfg()
	g := newGateway(c)
	if g.allowRTSP("rtsp://192.168.2.124:554/stream") {
		t.Fatal("camera direct stream mistakenly allowed")
	}
	if g.allowRTSP("rtsp://user:secret@127.0.0.1:8554/live") {
		t.Fatal("embedded RTSP credentials allowed")
	}
	if !g.allowRTSP("rtsp://127.0.0.1:8554/live") {
		t.Fatal("localhost RTSP disallowed")
	}
}

func TestMJPEGUsesExistingWorker(t *testing.T) {
	s, _ := NewServer(testCfg())
	if err := s.video.Start(s.cfg, s.gateway); err != nil {
		t.Fatal(err)
	}
	defer s.video.Stop()
	h, _ := s.Handler()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("GET", "http://127.0.0.1:18880/api/video/mjpeg", nil).WithContext(ctx)
	result := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(result, request); close(done) }()
	<-done
	if result.Code != 200 || !strings.Contains(result.Header().Get("Content-Type"), "multipart/x-mixed-replace") {
		t.Fatalf("MJPEG headers bad: code=%d contenttype=%s", result.Code, result.Header().Get("Content-Type"))
	}
	if !bytes.Contains(result.Body.Bytes(), []byte("Content-Type: image/jpeg")) {
		t.Fatal("MJPEG stream did not forward frames from singleton worker")
	}
	// A preview client must not have launched a second worker.
	if s.video.Status().Frames == 0 {
		t.Fatal("preview worker received no frames")
	}
}

func TestRecordingDownloadRejectsSymlink(t *testing.T) {
	cfg := testCfg()
	cfg.RecordDir = t.TempDir()
	name := "c201_20261111_120000.mkv"
	outside := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cfg.RecordDir, name)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	s, _ := NewServer(cfg)
	w := req(s, "GET", "/api/record/download/"+name, "", false)
	if w.Code != 404 {
		t.Fatalf("followed symlink, HTTP %d", w.Code)
	}
}
