package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
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
	if !inventory.Loaded || inventory.CameraCount != 4 || inventory.C201Count != 2 || len(inventory.Cameras) != 4 {
		t.Fatalf("unexpected mock inventory: %+v", inventory)
	}
	if inventory.SelectedCameraID != "xiaomi_0000000000000001" || !inventory.Cameras[0].PTZ || inventory.Cameras[2].PTZ || !inventory.Cameras[2].VideoReady || inventory.Cameras[3].Model != "vendor.camera.future-ptz" || !inventory.Cameras[3].PTZ {
		t.Fatalf("mock inventory did not expose the camera chooser capabilities: %+v", inventory.Cameras)
	}
	selected := req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"xiaomi_0000000000000003"}`, true)
	if selected.Code != http.StatusOK || !strings.Contains(selected.Body.String(), `"selected_camera_id":"xiaomi_0000000000000003"`) {
		t.Fatalf("mock selection did not switch to the video-only camera: HTTP %d: %s", selected.Code, selected.Body.String())
	}
	ptz := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"xiaomi_0000000000000003","direction":"left"}`, true)
	if ptz.Code != http.StatusForbidden {
		t.Fatalf("mock PTZ was accepted for a video-only camera: HTTP %d: %s", ptz.Code, ptz.Body.String())
	}
	selectedUnknown := req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"lab_0000000000000004"}`, true)
	if selectedUnknown.Code != http.StatusOK || !strings.Contains(selectedUnknown.Body.String(), `"selected_camera_id":"lab_0000000000000004"`) {
		t.Fatalf("mock chooser could not select the unknown PTZ model: HTTP %d: %s", selectedUnknown.Code, selectedUnknown.Body.String())
	}
	unknownPTZ := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"lab_0000000000000004","direction":"left"}`, true)
	if unknownPTZ.Code != http.StatusOK || !strings.Contains(unknownPTZ.Body.String(), `"camera_id":"lab_0000000000000004"`) || !strings.Contains(unknownPTZ.Body.String(), `"outcome":"simulated"`) {
		t.Fatalf("mock PTZ did not route the declared unknown-model capability: HTTP %d: %s", unknownPTZ.Code, unknownPTZ.Body.String())
	}
	after := req(s, http.MethodGet, "/api/status", "", false)
	if after.Code != http.StatusOK {
		t.Fatalf("final status returned HTTP %d", after.Code)
	}
	if err := json.Unmarshal(after.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Gateway.Inventory.Loaded || status.Gateway.Inventory.C201Count != 2 || status.Gateway.Inventory.SelectedCameraID != "lab_0000000000000004" {
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
	refresh := req(s, http.MethodPost, "/api/inventory/refresh", "", true)
	if refresh.Code != http.StatusOK {
		t.Fatalf("mock inventory refresh HTTP %d: %s", refresh.Code, refresh.Body.String())
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
	var health, info, inventory, streamStarts atomic.Int32
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
			if r.URL.Query().Get("include_streams") == "false" {
				inventory.Add(1)
				body = `{"cameras":[{"model":"xiaomi.camera.c201","id":"xiaomi_0123456789abcdef","name":"Test C201","capabilities":{"video":true,"ptz":true},"video":{"state":"idle","session_active":false}},{"model":"mxiang.camera.mod13","id":"xiaomi_1123456789abcdef","name":"Outdoor","capabilities":{"video":false,"ptz":false},"video":{"state":"unsupported"}}]}`
			} else if r.URL.Query().Get("streams_for") == "xiaomi_0123456789abcdef" {
				streamStarts.Add(1)
				body = `{"cameras":[{"model":"xiaomi.camera.c201","id":"xiaomi_0123456789abcdef","name":"Test C201","capabilities":{"video":true,"ptz":true},"video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`
			} else {
				status = http.StatusBadRequest
			}
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
	var listed InventorySummary
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || listed.CameraCount != 2 ||
		listed.SelectedCameraID != "" || len(listed.Cameras) != 2 || listed.Cameras[1].Model != "mxiang.camera.mod13" {
		t.Fatalf("inventory did not keep both cameras without auto-selecting: %s", w.Body.String())
	}
	if inventory.Load() != 1 || streamStarts.Load() != 0 {
		t.Fatalf("refresh must only request metadata: inventory=%d streams_for=%d", inventory.Load(), streamStarts.Load())
	}
	w = req(s, "POST", "/api/inventory/select", `{"camera_id":"xiaomi_1123456789abcdef"}`, true)
	if w.Code != 200 || streamStarts.Load() != 0 {
		t.Fatalf("unsupported camera selection unexpectedly opened a stream: status=%d starts=%d", w.Code, streamStarts.Load())
	}
	w = req(s, "POST", "/api/inventory/select", `{"camera_id":"xiaomi_0123456789abcdef"}`, true)
	if w.Code != 200 || streamStarts.Load() != 1 {
		t.Fatalf("explicit supported selection must start one source: status=%d starts=%d", w.Code, streamStarts.Load())
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
	if !validRecordingFilename("c201_20261111_120000_123456789_000.mkv") {
		t.Fatal("valid run-scoped segment rejected")
	}
	if !validRecordingFilename("c201_20261111_120000_123456789_reconnect01_000.mkv") {
		t.Fatal("valid reconnect segment rejected")
	}
}

func TestRecordingListsAndDownloadsOnlyClosedManifestSegments(t *testing.T) {
	dir := t.TempDir()
	runID := "c201_20261114_120000_123456789"
	complete := runID + "_000.mkv"
	partial := runID + "_001.mkv"
	empty := runID + "_003.mkv"
	legacy := "c201_20261113_120000.mkv"
	unverifiedLegacy := "c201_20261113_120001.mkv"
	zeroLegacy := "c201_20261113_120002.mkv"
	for name, content := range map[string]string{complete: "closed segment", partial: "still writing", empty: "", legacy: "verified legacy Matroska", unverifiedLegacy: "old unverified file", zeroLegacy: ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := runID + ".csv"
	if err := os.WriteFile(filepath.Join(dir, manifest), []byte(complete+",0.000000,5.000000\n../"+partial+",5.000000,10.000000\n"+runID+"_002.mkv,not-a-time,12\n"+empty+",5.000000,6.000000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verifyLegacy := func(path string) bool {
		content, err := os.ReadFile(path)
		return err == nil && string(content) == "verified legacy Matroska"
	}
	files, err := listRecordingsWithLegacyVerifier(dir, verifyLegacy)
	if err != nil {
		t.Fatal(err)
	}
	listed := make(map[string]bool)
	for _, file := range files {
		listed[file.Name] = true
	}
	if len(files) != 2 || !listed[legacy] || !listed[complete] || listed[unverifiedLegacy] || listed[zeroLegacy] {
		t.Fatalf("listed files = %+v; the closed segment and verified legacy clip should be visible", files)
	}

	cfg := testCfg()
	cfg.RecordDir = dir
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.legacyRecordingVerifier = verifyLegacy
	listResponse := req(s, "GET", "/api/record/files", "", false)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), legacy) || strings.Contains(listResponse.Body.String(), unverifiedLegacy) {
		t.Fatalf("legacy list response HTTP %d body=%q", listResponse.Code, listResponse.Body.String())
	}
	if response := req(s, "GET", "/api/record/download/"+partial, "", false); response.Code != http.StatusNotFound {
		t.Fatalf("download exposed an unlisted partial file: HTTP %d", response.Code)
	}
	if response := req(s, "GET", "/api/record/download/"+empty, "", false); response.Code != http.StatusNotFound {
		t.Fatalf("download exposed a zero-byte segment with a manifest row: HTTP %d", response.Code)
	}
	if response := req(s, "GET", "/api/record/download/"+unverifiedLegacy, "", false); response.Code != http.StatusNotFound {
		t.Fatalf("download exposed an unverified legacy file: HTTP %d", response.Code)
	}
	if response := req(s, "GET", "/api/record/download/"+zeroLegacy, "", false); response.Code != http.StatusNotFound {
		t.Fatalf("download exposed a zero-byte legacy file: HTTP %d", response.Code)
	}
	response := req(s, "GET", "/api/record/download/"+complete, "", false)
	if response.Code != http.StatusOK || response.Body.String() != "closed segment" {
		t.Fatalf("completed download: HTTP %d body=%q", response.Code, response.Body.String())
	}
	response = req(s, "GET", "/api/record/download/"+legacy, "", false)
	if response.Code != http.StatusOK || response.Body.String() != "verified legacy Matroska" {
		t.Fatalf("verified legacy download: HTTP %d body=%q", response.Code, response.Body.String())
	}
}

func TestParseFFprobeLegacySummaryRequiresVideoPacketsAndDuration(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "valid video", json: `{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`, want: true},
		{name: "numeric packet count", json: `{"streams":[{"codec_type":"video","nb_read_packets":42}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`, want: true},
		{name: "audio only", json: `{"streams":[{"codec_type":"audio","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`},
		{name: "zero packets", json: `{"streams":[{"codec_type":"video","nb_read_packets":"0"}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`},
		{name: "zero duration", json: `{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm","duration":"0"}}`},
		{name: "missing duration", json: `{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm"}}`},
		{name: "wrong container", json: `{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"avi","duration":"3.5"}}`},
		{name: "malformed", json: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseFFprobeLegacySummary([]byte(test.json)); got != test.want {
				t.Fatalf("parseFFprobeLegacySummary() = %v, want %v", got, test.want)
			}
		})
	}
	validJSON := []byte(`{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`)
	if !parseFFprobeLegacyOutput(validJSON, nil) {
		t.Fatal("valid ffprobe output was rejected")
	}
	if parseFFprobeLegacyOutput(validJSON, []byte("[matroska,webm @ 0x1] File ended prematurely")) {
		t.Fatal("ffprobe output with an early-end diagnostic was accepted")
	}
}

func TestLegacyFFprobeProcessDiagnosticsControlListAndDownload(t *testing.T) {
	root := t.TempDir()
	ffmpegPath := filepath.Join(root, "ffmpeg.exe")
	ffprobePath := filepath.Join(root, "ffprobe.exe")
	for _, path := range []string{ffmpegPath, ffprobePath} {
		if err := os.WriteFile(path, []byte("test shim placeholder"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	clipName := "c201_20261113_120000.mkv"
	clipContent := []byte("legacy clip fixture")
	probeJSON := `{"streams":[{"codec_type":"video","nb_read_packets":"42"}],"format":{"format_name":"matroska,webm","duration":"3.5"}}`
	tests := []struct {
		name        string
		diagnostics string
		wantListed  bool
	}{
		{name: "clean probe", wantListed: true},
		{name: "probe reports premature end with zero exit", diagnostics: "[matroska,webm @ 0x1] File ended prematurely", wantListed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, clipName), clipContent, 0600); err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			commandFactory := func(ctx context.Context, executable string, args ...string) *exec.Cmd {
				if executable != ffprobePath {
					t.Errorf("ffprobe executable = %q, want %q", executable, ffprobePath)
				}
				calls = append(calls, append([]string(nil), args...))
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFFprobeShimHelper$")
				cmd.Env = append(os.Environ(), "XLG_FFPROBE_SHIM=1", "XLG_FFPROBE_JSON="+probeJSON, "XLG_FFPROBE_DIAGNOSTICS="+test.diagnostics)
				return cmd
			}
			cfg := testCfg()
			cfg.RecordDir = dir
			cfg.FFmpegPath = ffmpegPath
			s, err := NewServer(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s.legacyRecordingVerifier = newLegacyRecordingVerifierWithCommand(ffmpegPath, commandFactory)
			if s.legacyRecordingVerifier == nil {
				t.Fatal("test ffprobe sibling was not resolved")
			}
			listResponse := req(s, "GET", "/api/record/files", "", false)
			if listResponse.Code != http.StatusOK {
				t.Fatalf("record list returned HTTP %d: %s", listResponse.Code, listResponse.Body.String())
			}
			var files []RecordingFile
			if err := json.Unmarshal(listResponse.Body.Bytes(), &files); err != nil {
				t.Fatal(err)
			}
			listed := len(files) == 1 && files[0].Name == clipName
			if listed != test.wantListed {
				t.Fatalf("legacy listing = %+v, wantListed=%v", files, test.wantListed)
			}
			download := req(s, "GET", "/api/record/download/"+clipName, "", false)
			if test.wantListed {
				if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), clipContent) {
					t.Fatalf("verified legacy download: HTTP %d body=%q", download.Code, download.Body.String())
				}
			} else if download.Code != http.StatusNotFound {
				t.Fatalf("diagnostic legacy download returned HTTP %d, want 404", download.Code)
			}
			if len(calls) != 2 {
				t.Fatalf("production verifier process calls = %d, want list and download probes", len(calls))
			}
			wantArgs := []string{"-v", "error", "-count_packets", "-show_entries", "stream=codec_type,nb_read_packets:format=duration,format_name", "-of", "json", filepath.Join(dir, clipName)}
			for i, args := range calls {
				if !reflect.DeepEqual(args, wantArgs) {
					t.Errorf("probe call %d args = %#v, want %#v", i, args, wantArgs)
				}
			}
		})
	}
}

func TestFFprobeShimHelper(t *testing.T) {
	if os.Getenv("XLG_FFPROBE_SHIM") != "1" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, os.Getenv("XLG_FFPROBE_JSON"))
	if diagnostic := os.Getenv("XLG_FFPROBE_DIAGNOSTICS"); diagnostic != "" {
		_, _ = fmt.Fprint(os.Stderr, diagnostic)
	}
	os.Exit(0)
}

func TestRecordingStopFinalizesSegmentBeforeItBecomesVisible(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	g := newGateway(cfg)
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{}
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		manifest, output := "", ""
		segmentTimeOK, csvListOK, stdinEnabled := false, false, false
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-segment_list" {
				manifest = args[i+1]
			}
			if args[i] == "-segment_time" && args[i+1] == "300" {
				segmentTimeOK = true
			}
			if args[i] == "-segment_list_type" && args[i+1] == "csv" {
				csvListOK = true
			}
			if args[i] == "-stdin" {
				stdinEnabled = true
			}
		}
		if !segmentTimeOK || !csvListOK || !stdinEnabled {
			t.Errorf("FFmpeg args missing 5-minute CSV/graceful-stop options: %v", args)
		}
		if len(args) > 0 {
			output = strings.Replace(args[len(args)-1], "%03d", "000", 1)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		cmd.Env = append(os.Environ(), "XLG_RECORDING_HELPER=1", "XLG_RECORDING_FILE="+output, "XLG_RECORDING_MANIFEST="+manifest)
		return cmd
	}
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	files, err := listRecordings(cfg.RecordDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("active segment should not be visible; files=%+v err=%v", files, err)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("graceful stop failed: %v", err)
	}
	files, err = listRecordings(cfg.RecordDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("finalized segment not listed: files=%+v err=%v", files, err)
	}
	if status := r.Status(cfg); status.Running || status.Error != "" {
		t.Fatalf("recording status after graceful stop: %+v", status)
	}
}

func TestRecordingReconnectCreatesMarkedSegmentAndHidesFailedAttempt(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	g := newGateway(cfg)
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{}
	r.retryBaseDelay = 20 * time.Millisecond
	var starts atomic.Int32
	startedFiles := make(chan string, 3)
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		manifest := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-segment_list" {
				manifest = args[i+1]
			}
		}
		output := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		startedFiles <- output
		startNumber := starts.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		env := []string{"XLG_RECORDING_HELPER=1", "XLG_RECORDING_FILE=" + output, "XLG_RECORDING_MANIFEST=" + manifest}
		if startNumber == 1 {
			env = append(env, "XLG_RECORDING_FAIL=1")
		}
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	firstFile := <-startedFiles
	deadline := time.Now().Add(3 * time.Second)
	for (starts.Load() < 2 || r.Status(cfg).Reconnecting) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := r.Status(cfg)
	if !status.Running || status.Reconnecting || status.ReconnectAttempt != 1 {
		t.Fatalf("reconnect did not return to recording: %+v", status)
	}
	secondFile := <-startedFiles
	if !strings.Contains(secondFile, "_reconnect01_000.mkv") {
		t.Fatalf("reconnected output is not visibly marked: %q", secondFile)
	}
	if firstFile == secondFile {
		t.Fatal("reconnect reused the failed output filename")
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("stop after reconnect: %v", err)
	}
	files, err := listRecordings(cfg.RecordDir)
	if err != nil || len(files) != 1 || files[0].Name != secondFile {
		t.Fatalf("expected only the finalized reconnect segment, files=%+v err=%v", files, err)
	}
}

func TestRecordingStopCancelsReconnectBackoff(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	g := newGateway(cfg)
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{retryBaseDelay: 5 * time.Second}
	var starts atomic.Int32
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		starts.Add(1)
		output := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		cmd.Env = append(os.Environ(), "XLG_RECORDING_HELPER=1", "XLG_RECORDING_FAIL=1", "XLG_RECORDING_FILE="+output)
		return cmd
	}
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !r.Status(cfg).Reconnecting && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.Status(cfg).Reconnecting {
		t.Fatal("failed process did not enter reconnect backoff")
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("stop during reconnect backoff: %v", err)
	}
	if status := r.Status(cfg); status.Running || status.Reconnecting || starts.Load() != 1 {
		t.Fatalf("stop did not cancel reconnect: status=%+v starts=%d", status, starts.Load())
	}
}

func TestRecordingStopTimeoutDoesNotKillNewRun(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	g := newGateway(cfg)
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{stopTimeout: 75 * time.Millisecond}
	stopTimeoutReached := make(chan struct{})
	releaseOldStop := make(chan struct{})
	released := false
	release := func() {
		if !released {
			close(releaseOldStop)
			released = true
		}
	}
	var stopTimeoutHooks atomic.Int32
	r.beforeStopTimeout = func() {
		if stopTimeoutHooks.Add(1) == 1 {
			close(stopTimeoutReached)
			<-releaseOldStop
		}
	}
	var starts atomic.Int32
	ready := filepath.Join(cfg.RecordDir, "first-run-ready")
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		manifest := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-segment_list" {
				manifest = args[i+1]
			}
		}
		output := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		start := starts.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		env := []string{"XLG_RECORDING_HELPER=1", "XLG_RECORDING_FILE=" + output, "XLG_RECORDING_MANIFEST=" + manifest}
		if start == 1 {
			env = append(env, "XLG_RECORDING_HANG=1", "XLG_RECORDING_READY="+ready)
		}
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	defer func() {
		release()
		if r.Status(cfg).Running {
			_ = r.Stop()
		}
	}()
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first recording helper did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	oldRun := r.run
	r.mu.Unlock()
	oldStopResult := make(chan error, 1)
	go func() { oldStopResult <- r.Stop() }()
	select {
	case <-stopTimeoutReached:
	case <-time.After(3 * time.Second):
		t.Fatal("old Stop did not reach the timeout window")
	}
	r.mu.Lock()
	oldRun.forcedStop = true
	killErr := oldRun.cmd.Process.Kill()
	r.mu.Unlock()
	if killErr != nil {
		t.Fatalf("kill the first helper during the controlled timeout window: %v", killErr)
	}
	select {
	case <-oldRun.done:
	case <-time.After(3 * time.Second):
		t.Fatal("first recording run did not finish")
	}
	if err := r.Start(cfg, g); err != nil {
		t.Fatalf("start a new recording while old Stop is delayed: %v", err)
	}
	r.mu.Lock()
	newRun := r.run
	r.mu.Unlock()
	if newRun == nil || newRun == oldRun {
		t.Fatal("new Start did not create a distinct recording run")
	}
	release()
	select {
	case err := <-oldStopResult:
		if err == nil || err.Error() != oldRun.result.Error() {
			t.Fatalf("old Stop returned %v, want original run result %v", err, oldRun.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("old Stop did not return")
	}
	if status := r.Status(cfg); !status.Running {
		t.Fatalf("old Stop affected the newer recording run: %+v", status)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("stop the newer run cleanly: %v", err)
	}
}

func TestRecordingStopTimeoutDuringReconnectStartKillsLateProcess(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	g := newGateway(cfg)
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{stopTimeout: 80 * time.Millisecond, retryBaseDelay: 10 * time.Millisecond}
	reconnectStartEntered := make(chan struct{})
	releaseReconnectStart := make(chan struct{})
	reconnectStarted := make(chan struct{})
	releaseReconnectPublish := make(chan struct{})
	var startReleased atomic.Bool
	var publishReleased atomic.Bool
	var reconnectStartHooks atomic.Int32
	var afterStartHooks atomic.Int32
	var timeoutHooks atomic.Int32
	r.beforeReconnectStart = func() {
		if reconnectStartHooks.Add(1) == 1 {
			close(reconnectStartEntered)
			<-releaseReconnectStart
		}
	}
	r.afterReconnectStart = func() {
		if afterStartHooks.Add(1) == 1 {
			close(reconnectStarted)
			<-releaseReconnectPublish
		}
	}
	stopTimeoutReached := make(chan struct{})
	r.beforeStopTimeout = func() {
		if timeoutHooks.Add(1) == 1 {
			close(stopTimeoutReached)
		}
	}
	ready := filepath.Join(cfg.RecordDir, "late-reconnect-ready")
	var starts atomic.Int32
	startedFiles := make(chan string, 3)
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		manifest := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-segment_list" {
				manifest = args[i+1]
			}
		}
		output := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		startedFiles <- output
		start := starts.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		env := []string{"XLG_RECORDING_HELPER=1", "XLG_RECORDING_FILE=" + output, "XLG_RECORDING_MANIFEST=" + manifest}
		if start == 1 {
			env = append(env, "XLG_RECORDING_FAIL=1")
		} else if start == 2 {
			env = append(env, "XLG_RECORDING_HANG=1", "XLG_RECORDING_READY="+ready)
		}
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	defer func() {
		if startReleased.CompareAndSwap(false, true) {
			close(releaseReconnectStart)
		}
		if publishReleased.CompareAndSwap(false, true) {
			close(releaseReconnectPublish)
		}
		if r.Status(cfg).Running {
			_ = r.Stop()
		}
	}()
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	select {
	case <-startedFiles:
	case <-time.After(3 * time.Second):
		t.Fatal("initial recording process did not start")
	}
	select {
	case <-reconnectStartEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not enter its delayed start")
	}
	stopResult := make(chan error, 1)
	go func() { stopResult <- r.Stop() }()
	select {
	case <-stopTimeoutReached:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not reach its timeout while reconnect Start was delayed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.mu.Lock()
		forced := r.run != nil && r.run.forcedStop
		r.mu.Unlock()
		if forced {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Stop timeout was not carried to the delayed reconnect")
		}
		time.Sleep(time.Millisecond)
	}
	if startReleased.CompareAndSwap(false, true) {
		close(releaseReconnectStart)
	}
	select {
	case <-reconnectStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("late reconnect process did not start")
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late reconnect helper did not enter its q-ignoring state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if publishReleased.CompareAndSwap(false, true) {
		close(releaseReconnectPublish)
	}
	select {
	case err := <-stopResult:
		if err == nil || !strings.Contains(err.Error(), "did not finalize cleanly") {
			t.Fatalf("Stop result = %v, want non-clean finalization error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after killing the late reconnect process")
	}
	if status := r.Status(cfg); status.Running || status.Reconnecting {
		t.Fatalf("late reconnect process was not stopped: %+v", status)
	}
	files, err := listRecordings(cfg.RecordDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("interrupted reconnect segment became visible: files=%+v err=%v", files, err)
	}
	if err := r.Start(cfg, g); err != nil {
		t.Fatalf("new recording could not start after forced reconnect stop: %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("new recording after reconnect stop did not finalize: %v", err)
	}
	if starts.Load() != 3 {
		t.Fatalf("process starts = %d, want initial, delayed reconnect, and new recording", starts.Load())
	}
}

func TestRecordingProcessHelper(t *testing.T) {
	if os.Getenv("XLG_RECORDING_HELPER") != "1" {
		return
	}
	name := os.Getenv("XLG_RECORDING_FILE")
	manifest := os.Getenv("XLG_RECORDING_MANIFEST")
	if os.Getenv("XLG_RECORDING_DISK_FULL") == "1" {
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintln(os.Stderr, "[matroska] No space left on device; source rtsp://user:secret@camera.local/token")
		os.Exit(72)
	}
	if err := os.WriteFile(name, []byte("partial Matroska data"), 0600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("XLG_RECORDING_FAIL") == "1" {
		t.Fatal("simulated FFmpeg write failure")
	}
	if os.Getenv("XLG_RECORDING_HANG") == "1" {
		if ready := os.Getenv("XLG_RECORDING_READY"); ready != "" {
			if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for {
			time.Sleep(time.Second)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() != "q" {
			continue
		}
		if err := os.WriteFile(name, []byte("finalized Matroska fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest, []byte(strings.TrimSuffix(name, "_000.mkv")+"_000.mkv,0.000000,1.000000\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("FFmpeg helper stdin closed before graceful quit")
}

func testGatewayWithCamera(t *testing.T, s *Server) {
	t.Helper()
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(inventory)
}

func TestRecordingDiskFullFailureHidesPartialFile(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	testGatewayWithCamera(t, s)
	s.recording.retryBaseDelay = 10 * time.Second
	var partialPath string
	s.recording.commandFactory = func(_ string, args ...string) *exec.Cmd {
		partialPath = filepath.Join(cfg.RecordDir, strings.Replace(args[len(args)-1], "%03d", "000", 1))
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		cmd.Env = append(os.Environ(), "XLG_RECORDING_HELPER=1", "XLG_RECORDING_DISK_FULL=1", "XLG_RECORDING_FILE="+partialPath)
		return cmd
	}
	start := req(s, http.MethodPost, "/api/record/start", "", true)
	if start.Code != http.StatusOK {
		t.Fatalf("record start returned HTTP %d: %s", start.Code, start.Body.String())
	}
	defer s.recording.Stop()
	deadline := time.Now().Add(3 * time.Second)
	var status RecordStatus
	for time.Now().Before(deadline) {
		status = s.recording.Status(cfg)
		if status.Reconnecting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !status.Running || !status.Reconnecting || status.Error != "recording storage is full; incomplete segment is hidden; reconnect will be retried" {
		t.Fatalf("disk-full recorder status = %+v", status)
	}
	if strings.Contains(status.Error, "secret") || strings.Contains(status.Error, "camera.local") {
		t.Fatalf("raw FFmpeg diagnostic leaked in status: %q", status.Error)
	}
	info, err := os.Stat(partialPath)
	if err != nil || info.Size() != 0 {
		t.Fatalf("expected simulated empty partial segment; stat=%v err=%v", info, err)
	}
	list := req(s, http.MethodGet, "/api/record/files", "", false)
	var files []RecordingFile
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &files) != nil || len(files) != 0 {
		t.Fatalf("partial disk-full segment appeared in list: HTTP %d body=%q", list.Code, list.Body.String())
	}
	if strings.TrimSpace(list.Body.String()) != "[]" {
		t.Fatalf("empty recording list is not a JSON array: %q", list.Body.String())
	}
	name := filepath.Base(partialPath)
	download := req(s, http.MethodGet, "/api/record/download/"+name, "", false)
	if download.Code != http.StatusNotFound {
		t.Fatalf("partial disk-full segment download returned HTTP %d, want 404", download.Code)
	}
	// Exercise the status JSON shape in mock mode so this request cannot touch
	// the gateway; the recorder itself is the same process started above.
	s.cfg.Mock = true
	statusResponse := req(s, http.MethodGet, "/api/status", "", false)
	var statusBody struct {
		Recording RecordStatus `json:"recording"`
	}
	if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &statusBody) != nil || statusBody.Recording.Error != status.Error {
		t.Fatalf("recording status API did not expose the classified error: HTTP %d body=%q", statusResponse.Code, statusResponse.Body.String())
	}
	t.Logf("SIMULATED ENOSPC: recorder=%+v status_api=%d list=%d %s download=%d; no real volume was filled", status, statusResponse.Code, list.Code, strings.TrimSpace(list.Body.String()), download.Code)
}

func TestRecordingReconnectPreservesStorageErrorAndRecoversWhenDirectoryReturns(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var inventory managedInventory
	if err := json.Unmarshal([]byte(`{"cameras":[{"id":"xiaomi_1111111111111111","name":"C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera"}]}]}`), &inventory); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(inventory)
	r := &s.recording
	r.retryBaseDelay = 200 * time.Millisecond
	var processStarts atomic.Int32
	var reconnectStarts atomic.Int32
	startedFiles := make(chan string, 4)
	startErrors := make(chan error, 4)
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		processNumber := processStarts.Add(1)
		outputName := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		manifestName := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-segment_list" {
				manifestName = args[i+1]
			}
		}
		startedFiles <- outputName
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		env := []string{
			"XLG_RECORDING_HELPER=1",
			"XLG_RECORDING_FILE=" + outputName,
			"XLG_RECORDING_MANIFEST=" + manifestName,
		}
		if processNumber == 1 {
			env = append(env, "XLG_RECORDING_FAIL=1")
		}
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	r.onReconnectStartFail = func(err error) {
		select {
		case startErrors <- err:
		default:
		}
	}
	r.beforeReconnectStart = func() {
		if reconnectStarts.Add(1) == 1 {
			if err := os.RemoveAll(cfg.RecordDir); err != nil {
				t.Errorf("simulate unavailable recording directory: %v", err)
			}
		}
	}
	if err := r.Start(cfg, s.gateway); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	firstName := <-startedFiles
	deadline := time.Now().Add(5 * time.Second)
	var status RecordStatus
	for time.Now().Before(deadline) {
		status = r.Status(cfg)
		if status.ReconnectAttempt >= 2 && strings.Contains(status.Error, "recording storage is unavailable or not writable") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !status.Running || !status.Reconnecting || status.ReconnectAttempt < 2 || status.Error != "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried" {
		var startErr error
		select {
		case startErr = <-startErrors:
		default:
		}
		t.Fatalf("storage error was lost after reconnect Start failed: status=%+v startErr=%v", status, startErr)
	}
	wasMock := s.cfg.Mock
	s.cfg.Mock = true // make this status request local-only; do not query the gateway
	statusResponse := req(s, http.MethodGet, "/api/status", "", false)
	s.cfg.Mock = wasMock
	var statusBody struct {
		Recording RecordStatus `json:"recording"`
	}
	if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &statusBody) != nil || statusBody.Recording.Error != status.Error {
		t.Fatalf("status API did not retain reconnect storage error: HTTP %d body=%q", statusResponse.Code, statusResponse.Body.String())
	}
	select {
	case err := <-startErrors:
		t.Logf("expected reconnect process start failure while record directory was absent: %v", err)
	default:
		t.Fatal("reconnect did not attempt to start FFmpeg while the directory was absent")
	}
	if _, err := os.Stat(cfg.RecordDir); !os.IsNotExist(err) {
		t.Fatalf("test directory should still be unavailable before recovery: stat err=%v", err)
	}
	if err := os.MkdirAll(cfg.RecordDir, 0700); err != nil {
		t.Fatalf("restore recording directory: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status = r.Status(cfg)
		if processStarts.Load() >= 3 && status.Running && !status.Reconnecting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !status.Running || status.Reconnecting || status.Error != "" || processStarts.Load() != 3 {
		t.Fatalf("recorder did not recover after the directory returned: starts=%d status=%+v", processStarts.Load(), status)
	}
	wasMock = s.cfg.Mock
	s.cfg.Mock = true // local status only; do not query the gateway
	recoveredStatusResponse := req(s, http.MethodGet, "/api/status", "", false)
	s.cfg.Mock = wasMock
	var recoveredStatusBody struct {
		Recording RecordStatus `json:"recording"`
	}
	if recoveredStatusResponse.Code != http.StatusOK || json.Unmarshal(recoveredStatusResponse.Body.Bytes(), &recoveredStatusBody) != nil || recoveredStatusBody.Recording.Error != "" || !recoveredStatusBody.Recording.Running || recoveredStatusBody.Recording.Reconnecting {
		t.Fatalf("status API did not clear the storage error after recovery: HTTP %d body=%q", recoveredStatusResponse.Code, recoveredStatusResponse.Body.String())
	}
	failedReconnect := <-startedFiles
	recoveredReconnect := <-startedFiles
	if !strings.Contains(failedReconnect, "_reconnect01_000.mkv") || !strings.Contains(recoveredReconnect, "_reconnect02_000.mkv") {
		t.Fatalf("unexpected reconnect segment names: failed=%q recovered=%q", failedReconnect, recoveredReconnect)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("stop recovered recording: %v", err)
	}
	files, err := listRecordings(cfg.RecordDir)
	if err != nil || len(files) != 1 || files[0].Name != recoveredReconnect {
		t.Fatalf("only recovered segment should be downloadable: files=%+v err=%v", files, err)
	}
	listed := req(s, http.MethodGet, "/api/record/files", "", false)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), recoveredReconnect) || strings.Contains(listed.Body.String(), failedReconnect) {
		t.Fatalf("record list after recovery did not expose only the closed segment: HTTP %d body=%q", listed.Code, listed.Body.String())
	}
	failedDownload := req(s, http.MethodGet, "/api/record/download/"+failedReconnect, "", false)
	if failedDownload.Code != http.StatusNotFound {
		t.Fatalf("failed reconnect segment download returned HTTP %d, want 404", failedDownload.Code)
	}
	recoveredDownload := req(s, http.MethodGet, "/api/record/download/"+recoveredReconnect, "", false)
	if recoveredDownload.Code != http.StatusOK || recoveredDownload.Body.Len() == 0 {
		t.Fatalf("recovered segment download returned HTTP %d, want non-empty 200", recoveredDownload.Code)
	}
	if !strings.Contains(firstName, "_000.mkv") {
		t.Fatalf("unexpected initial segment name: %q", firstName)
	}
	t.Logf("record directory disappeared during reconnect: status=%+v; recovered after restore with closed segment %s; failed _reconnect01 stayed hidden", status, recoveredReconnect)
}

func TestRecordingReconnectMissingFFmpegDoesNotReportStorageFailure(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = t.TempDir()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	testGatewayWithCamera(t, s)
	r := &s.recording
	r.retryBaseDelay = 100 * time.Millisecond
	var processStarts atomic.Int32
	startErrors := make(chan error, 2)
	r.commandFactory = func(_ string, args ...string) *exec.Cmd {
		processNumber := processStarts.Add(1)
		if processNumber > 1 {
			return exec.Command(filepath.Join(cfg.RecordDir, "missing-ffmpeg.exe"), args...)
		}
		outputName := strings.Replace(args[len(args)-1], "%03d", "000", 1)
		manifestName := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-segment_list" {
				manifestName = args[i+1]
			}
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
		cmd.Env = append(os.Environ(), "XLG_RECORDING_HELPER=1", "XLG_RECORDING_FAIL=1", "XLG_RECORDING_FILE="+outputName, "XLG_RECORDING_MANIFEST="+manifestName)
		return cmd
	}
	r.onReconnectStartFail = func(err error) {
		select {
		case startErrors <- err:
		default:
		}
	}
	if err := r.Start(cfg, s.gateway); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	deadline := time.Now().Add(5 * time.Second)
	var status RecordStatus
	for time.Now().Before(deadline) {
		status = r.Status(cfg)
		if status.ReconnectAttempt >= 2 && status.Error == "RTSP reconnect failed; retrying" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !status.Running || !status.Reconnecting || status.ReconnectAttempt < 2 || status.Error != "RTSP reconnect failed; retrying" || processStarts.Load() != 2 {
		t.Fatalf("missing FFmpeg was misclassified or not retried: starts=%d status=%+v", processStarts.Load(), status)
	}
	if info, err := os.Stat(cfg.RecordDir); err != nil || !info.IsDir() {
		t.Fatalf("record directory should remain available: info=%v err=%v", info, err)
	}
	select {
	case startErr := <-startErrors:
		var pathErr *os.PathError
		if errors.As(startErr, &pathErr) && pathErr.Op == "chdir" {
			t.Fatalf("missing executable unexpectedly failed during chdir: %v", startErr)
		}
	case <-time.After(time.Second):
		t.Fatal("missing FFmpeg process start error was not observed")
	}
	wasMock := s.cfg.Mock
	s.cfg.Mock = true // local status only; do not query the gateway
	statusResponse := req(s, http.MethodGet, "/api/status", "", false)
	s.cfg.Mock = wasMock
	var statusBody struct {
		Recording RecordStatus `json:"recording"`
	}
	if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &statusBody) != nil || statusBody.Recording.Error != "RTSP reconnect failed; retrying" {
		t.Fatalf("missing FFmpeg status API exposed the wrong category: HTTP %d body=%q", statusResponse.Code, statusResponse.Body.String())
	}
	if strings.Contains(statusBody.Recording.Error, cfg.RecordDir) || strings.Contains(statusBody.Recording.Error, "missing-ffmpeg") {
		t.Fatalf("raw executable or directory path leaked into status: %q", statusBody.Recording.Error)
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("stop retry after missing executable: %v", err)
	}
	t.Logf("missing FFmpeg with available RecordDir remained a generic reconnect error: %s", statusBody.Recording.Error)
}

func TestClassifyRecordingStartFailureUsesTypedErrorsAndDirectoryState(t *testing.T) {
	dir := t.TempDir()
	if got := classifyRecordingStartFailure(&exec.Error{Name: "missing-ffmpeg", Err: exec.ErrNotFound}, dir); got != "RTSP reconnect failed; retrying" {
		t.Fatalf("missing executable classification = %q", got)
	}
	deniedDir := &os.PathError{Op: "chdir", Path: dir, Err: os.ErrPermission}
	if got := classifyRecordingStartFailure(deniedDir, dir); got != "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried" {
		t.Fatalf("typed denied-directory classification = %q", got)
	}
	if got := classifyRecordingStartFailure(errors.New("ambiguous process start failure"), filepath.Join(dir, "gone")); got != "recording storage is unavailable or not writable; incomplete segment is hidden; reconnect will be retried" {
		t.Fatalf("missing directory in ambiguous-error fallback = %q", got)
	}
	if got := classifyRecordingStartFailure(errors.New("ambiguous process start failure"), dir); got != "RTSP reconnect failed; retrying" {
		t.Fatalf("available directory in ambiguous-error fallback = %q", got)
	}
}

func TestRecordingDelayedDirectoryCheckDoesNotBlockStatusOrStop(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = true // status remains local-only; no gateway request is made
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := &s.recording
	r.stopTimeout = 40 * time.Millisecond
	statEntered := make(chan struct{})
	allowStatReturn := make(chan struct{})
	r.statRecordDir = func(string) (os.FileInfo, error) {
		close(statEntered)
		<-allowStatReturn
		return nil, os.ErrNotExist
	}
	run := &recordingRun{done: make(chan struct{}), stopSignal: make(chan struct{})}
	r.mu.Lock()
	r.running = true
	r.reconnecting = true
	r.errText = "previous reconnect state"
	r.run = run
	r.mu.Unlock()

	handlerDone := make(chan bool, 1)
	go func() {
		handlerDone <- r.handleReconnectStartFailure(cfg, run, errors.New("ambiguous start error"))
	}()
	select {
	case <-statEntered:
	case <-time.After(time.Second):
		t.Fatal("directory check did not start")
	}

	statusStarted := time.Now()
	statusResponse := req(s, http.MethodGet, "/api/status", "", false)
	if elapsed := time.Since(statusStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("status blocked behind directory check for %s", elapsed)
	}
	var statusBody struct {
		Recording RecordStatus `json:"recording"`
	}
	if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &statusBody) != nil || statusBody.Recording.Error != "previous reconnect state" {
		t.Fatalf("status changed while delayed directory result was pending: HTTP %d body=%q", statusResponse.Code, statusResponse.Body.String())
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- r.Stop() }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		forced := run.forcedStop
		r.mu.Unlock()
		if forced {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Lock()
	stopRequested := run.stopRequested
	forcedStop := run.forcedStop
	r.mu.Unlock()
	if !stopRequested || !forcedStop {
		t.Fatalf("Stop did not record and advance its request while directory check was blocked: requested=%v forced=%v", stopRequested, forcedStop)
	}

	close(allowStatReturn)
	select {
	case handled := <-handlerDone:
		if !handled {
			t.Fatal("delayed reconnect failure did not observe the stop request")
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect failure handler did not finish after releasing directory check")
	}
	select {
	case stopErr := <-stopDone:
		if stopErr == nil || !strings.Contains(stopErr.Error(), "did not finalize cleanly") {
			t.Fatalf("Stop result = %v, want the stopped run's incomplete-finalization error", stopErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after delayed directory check was released")
	}
	status := r.Status(cfg)
	if status.Running || status.Error != "recording stopped before the active segment was finalized; incomplete files are hidden" {
		t.Fatalf("late storage classification overwrote the stopped run state: %+v", status)
	}
	t.Logf("delayed RecordDir check ran without Recording.mu: status returned promptly, Stop recorded its request, and stale classification did not overwrite final state")
}

func TestRecordingUnavailableLoopbackSMBTarget(t *testing.T) {
	if os.Getenv("XLG_TEST_UNAVAILABLE_SMB") != "1" {
		t.Skip("set XLG_TEST_UNAVAILABLE_SMB=1 to probe the loopback-only missing SMB share")
	}
	if runtime.GOOS != "windows" {
		t.Skip("UNC storage failure probe requires Windows")
	}
	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = os.Args[0]
	cfg.RecordDir = `\\127.0.0.1\XLG_F01_MISSING_SHARE\recordings`
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	testGatewayWithCamera(t, s)
	var processStarts int
	s.recording.commandFactory = func(_ string, _ ...string) *exec.Cmd {
		processStarts++
		return exec.Command(os.Args[0], "-test.run=^TestRecordingProcessHelper$")
	}
	start := req(s, http.MethodPost, "/api/record/start", "", true)
	if start.Code != http.StatusConflict {
		t.Fatalf("unavailable SMB start returned HTTP %d: %s", start.Code, start.Body.String())
	}
	status := s.recording.Status(cfg)
	if status.Running || !strings.Contains(status.Error, "recording storage is unavailable") {
		t.Fatalf("unavailable SMB recorder status = %+v", status)
	}
	list := req(s, http.MethodGet, "/api/record/files", "", false)
	if list.Code != http.StatusInternalServerError {
		t.Fatalf("unavailable SMB list returned HTTP %d, want 500", list.Code)
	}
	download := req(s, http.MethodGet, "/api/record/download/c201_20261114_120000.mkv", "", false)
	if download.Code != http.StatusNotFound {
		t.Fatalf("unavailable SMB download returned HTTP %d, want 404", download.Code)
	}
	if processStarts != 0 {
		t.Fatalf("FFmpeg processes started against unavailable SMB path: %d", processStarts)
	}
	t.Logf("LOOPBACK SMB failure: start=%d %s recorder=%+v list=%d download=%d ffmpeg_starts=%d", start.Code, strings.TrimSpace(start.Body.String()), status, list.Code, download.Code, processStarts)
}

func TestRecordingFFmpegSyntheticRTSP(t *testing.T) {
	ffmpegPath := os.Getenv("XLG_FFMPEG_PATH")
	rtspMockPath := os.Getenv("XLG_RTSP_MOCK_PATH")
	if ffmpegPath == "" || rtspMockPath == "" {
		t.Skip("set XLG_FFMPEG_PATH and XLG_RTSP_MOCK_PATH to run the real FFmpeg/RTSP integration")
	}
	if _, err := os.Stat(ffmpegPath); err != nil {
		t.Fatalf("FFmpeg executable unavailable: %v", err)
	}
	if _, err := os.Stat(rtspMockPath); err != nil {
		t.Fatalf("synthetic RTSP server executable unavailable: %v", err)
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "synthetic.h264")
	generate := exec.Command(ffmpegPath, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=5", "-frames:v", "1", "-an", "-c:v", "libx264", "-profile:v", "baseline", "-preset", "ultrafast", "-tune", "zerolatency", "-x264-params", "repeat-headers=1:keyint=1", "-f", "h264", "-y", fixture)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate local H.264 fixture: %v: %s", err, output)
	}

	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback RTSP port: %v", err)
	}
	address := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	rtspLogPath := filepath.Join(dir, "rtsp-mock.log")
	rtspLog, err := os.Create(rtspLogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rtspLog.Close()
	rtspLogText := func() string {
		_ = rtspLog.Sync()
		data, _ := os.ReadFile(rtspLogPath)
		return strings.TrimSpace(string(data))
	}
	startRTSP := func() (*exec.Cmd, error) {
		cmd := exec.Command(rtspMockPath, "-file", fixture, "-address", address)
		cmd.Stdout = rtspLog
		cmd.Stderr = rtspLog
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(5 * time.Second)
		var lastDialErr error
		for time.Now().Before(deadline) {
			conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return cmd, nil
			}
			lastDialErr = dialErr
			time.Sleep(25 * time.Millisecond)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("loopback RTSP mock did not accept TCP at %s (last dial: %v): %s", address, lastDialErr, rtspLogText())
	}
	stopRTSP := func(cmd *exec.Cmd) {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	var source *exec.Cmd
	source, err = startRTSP()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopRTSP(source) }()

	cfg := testCfg()
	cfg.Mock = false
	cfg.FFmpegPath = ffmpegPath
	cfg.RecordDir = filepath.Join(dir, "recordings")
	if err := os.MkdirAll(cfg.RecordDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.AllowedRTSPHosts = []string{"127.0.0.1"}
	rtspURL := "rtsp://" + address + "/synthetic"
	g := newGateway(cfg)
	var inventory managedInventory
	inventoryJSON := `{"cameras":[{"id":"xiaomi_1111111111111111","name":"Synthetic C201","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"` + rtspURL + `"}]}]}`
	if err := json.Unmarshal([]byte(inventoryJSON), &inventory); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(inventory)
	r := &Recording{retryBaseDelay: 100 * time.Millisecond, stopTimeout: 4 * time.Second}
	ffmpegLogPath := filepath.Join(dir, "ffmpeg.stderr.log")
	ffmpegLog, err := os.Create(ffmpegLogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ffmpegLog.Close()
	type ffmpegStart struct {
		file      string
		logOffset int64
	}
	startedFiles := make(chan ffmpegStart, 64)
	frameProgress := regexp.MustCompile(`frame=\s*[1-9][0-9]*`)
	var starts atomic.Int32
	var segmentTime300 atomic.Bool
	r.commandFactory = func(path string, args ...string) *exec.Cmd {
		starts.Add(1)
		// Keep production arguments except for the test process log level, so
		// a real FFmpeg/RTSP incompatibility is visible when the integration fails.
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-loglevel" {
				args[i+1] = "info"
				break
			}
		}
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-segment_time" && args[i+1] == "300" {
				segmentTime300.Store(true)
			}
		}
		logInfo, _ := ffmpegLog.Stat()
		var logOffset int64
		if logInfo != nil {
			logOffset = logInfo.Size()
		}
		startedFiles <- ffmpegStart{file: strings.Replace(args[len(args)-1], "%03d", "000", 1), logOffset: logOffset}
		cmd := exec.Command(path, args...)
		cmd.Stderr = ffmpegLog
		return cmd
	}
	defer func() {
		stopRTSP(source)
		source = nil
		if r.Status(cfg).Running {
			_ = r.Stop()
		}
	}()
	if err := r.Start(cfg, g); err != nil {
		t.Fatal(err)
	}
	var initialRun ffmpegStart
	select {
	case initialRun = <-startedFiles:
	case <-time.After(5 * time.Second):
		t.Fatal("initial FFmpeg command did not start")
	}
	waitForFrames := func(offset int64, timeout time.Duration) bool {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			_ = ffmpegLog.Sync()
			logData, _ := os.ReadFile(ffmpegLogPath)
			if offset < int64(len(logData)) && frameProgress.Match(logData[offset:]) {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	if !waitForFrames(initialRun.logOffset, 8*time.Second) {
		_ = ffmpegLog.Sync()
		stderr, _ := os.ReadFile(ffmpegLogPath)
		entries, _ := os.ReadDir(cfg.RecordDir)
		var recordFiles []string
		for _, entry := range entries {
			info, _ := entry.Info()
			recordFiles = append(recordFiles, fmt.Sprintf("%s(%d)", entry.Name(), info.Size()))
		}
		t.Fatalf("real FFmpeg did not receive frames from the loopback source; initial=%s starts=%d status=%+v files=%v; FFmpeg log: %s; RTSP log: %s", initialRun.file, starts.Load(), r.Status(cfg), recordFiles, strings.TrimSpace(string(stderr)), rtspLogText())
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	activeList := req(s, "GET", "/api/record/files", "", false)
	if activeList.Code != http.StatusOK {
		t.Fatalf("active recording list returned HTTP %d", activeList.Code)
	}
	var activeFiles []RecordingFile
	if err := json.Unmarshal(activeList.Body.Bytes(), &activeFiles); err != nil {
		t.Fatal(err)
	}
	if len(activeFiles) != 0 {
		t.Fatalf("active unclosed Matroska segment appeared in list: %+v", activeFiles)
	}
	ffprobePath := os.Getenv("XLG_FFPROBE_PATH")
	if ffprobePath == "" {
		candidate := filepath.Join(filepath.Dir(ffmpegPath), "ffprobe.exe")
		if _, statErr := os.Stat(candidate); statErr == nil {
			ffprobePath = candidate
		}
	}
	verifyDownloaded := func(label string, body []byte) {
		downloaded := filepath.Join(dir, label+".mkv")
		if err := os.WriteFile(downloaded, body, 0600); err != nil {
			t.Fatal(err)
		}
		if ffprobePath != "" {
			probe := exec.Command(ffprobePath, "-v", "error", "-count_packets", "-show_entries", "stream=codec_type,nb_read_packets:format=duration,format_name", "-of", "json", downloaded)
			output, probeErr := probe.CombinedOutput()
			if probeErr != nil || !parseFFprobeLegacySummary(output) {
				t.Fatalf("ffprobe did not verify downloaded Matroska video %s: %v: %s", label, probeErr, output)
			}
			t.Logf("ffprobe verified downloaded Matroska video %s", label)
			return
		}
		decode := exec.Command(ffmpegPath, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "matroska", "-i", downloaded, "-map", "0:v:0", "-f", "null", os.DevNull)
		output, decodeErr := decode.CombinedOutput()
		if verifyErr := validateFFmpegDecodeResult(decodeErr, output); verifyErr != nil {
			t.Fatalf("ffmpeg could not cleanly decode downloaded Matroska video %s: %v", label, verifyErr)
		}
		t.Logf("ffprobe.exe unavailable; real FFmpeg decoded the downloaded Matroska video %s as a fallback", label)
	}
	if os.Getenv("XLG_FFMPEG_WAIT_FOR_ROTATION") == "1" {
		wantClosedName := filepath.Base(initialRun.file)
		wantActiveName := strings.TrimSuffix(wantClosedName, "_000.mkv") + "_001.mkv"
		rotationDeadline := time.Now().Add(330 * time.Second)
		rotatedWhileRunning := false
		for time.Now().Before(rotationDeadline) {
			list := req(s, "GET", "/api/record/files", "", false)
			if list.Code != http.StatusOK {
				t.Fatalf("recording list during 300-second rotation returned HTTP %d: %s", list.Code, list.Body.String())
			}
			var snapshot []RecordingFile
			if err := json.Unmarshal(list.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			closedListed := false
			for _, file := range snapshot {
				if file.Name == wantClosedName {
					closedListed = true
				}
			}
			activeInfo, activeStatErr := os.Stat(filepath.Join(cfg.RecordDir, wantActiveName))
			_ = ffmpegLog.Sync()
			ffmpegLogText, _ := os.ReadFile(ffmpegLogPath)
			activeOpened := strings.Contains(string(ffmpegLogText), "Opening '"+wantActiveName+"' for writing")
			if closedListed && activeStatErr == nil && activeInfo.Mode().IsRegular() && activeOpened {
				rotatedWhileRunning = true
				break
			}
			if !r.Status(cfg).Running {
				t.Fatalf("recording stopped before the first 300-second segment rotation: %+v", r.Status(cfg))
			}
			time.Sleep(time.Second)
		}
		if !rotatedWhileRunning {
			t.Fatalf("the first closed segment %s was not listed within the 330-second rotation window", wantClosedName)
		}
		if !r.Status(cfg).Running {
			t.Fatal("recording stopped before download of the rotated segment")
		}
		freshList := req(s, "GET", "/api/record/files", "", false)
		if freshList.Code != http.StatusOK {
			t.Fatalf("fresh recording list after opening %s returned HTTP %d: %s", wantActiveName, freshList.Code, freshList.Body.String())
		}
		var freshSnapshot []RecordingFile
		if err := json.Unmarshal(freshList.Body.Bytes(), &freshSnapshot); err != nil {
			t.Fatal(err)
		}
		closedListed := false
		activeListed := false
		for _, file := range freshSnapshot {
			if file.Name == wantClosedName {
				closedListed = true
			}
			if file.Name == wantActiveName {
				activeListed = true
			}
		}
		if !closedListed {
			t.Fatalf("fresh recording list omitted closed segment %s: %+v", wantClosedName, freshSnapshot)
		}
		if activeListed {
			t.Fatalf("fresh recording list exposed currently written segment %s: %+v", wantActiveName, freshSnapshot)
		}
		activeDownload := req(s, "GET", "/api/record/download/"+wantActiveName, "", false)
		if activeDownload.Code != http.StatusNotFound {
			t.Fatalf("currently written segment download returned HTTP %d, want 404 (%d bytes)", activeDownload.Code, activeDownload.Body.Len())
		}
		rotatedDownload := req(s, "GET", "/api/record/download/"+wantClosedName, "", false)
		if rotatedDownload.Code != http.StatusOK || rotatedDownload.Body.Len() == 0 {
			t.Fatalf("rotated segment download while recording returned HTTP %d (%d bytes)", rotatedDownload.Code, rotatedDownload.Body.Len())
		}
		verifyDownloaded("rotated-while-running", rotatedDownload.Body.Bytes())
		t.Logf("after rotation, closed segment %s was listed/downloaded while active segment %s was hidden and returned HTTP 404", wantClosedName, wantActiveName)
	}
	stopRTSP(source)
	source = nil
	var reconnectRun ffmpegStart
	select {
	case reconnectRun = <-startedFiles:
	case <-time.After(8 * time.Second):
		t.Fatalf("source interruption did not start a reconnect process; starts=%d status=%+v", starts.Load(), r.Status(cfg))
	}
	time.Sleep(300 * time.Millisecond)
	source, err = startRTSP()
	if err != nil {
		t.Fatalf("restart synthetic loopback RTSP source: %v", err)
	}
	if !waitForFrames(reconnectRun.logOffset, 12*time.Second) {
		_ = ffmpegLog.Sync()
		stderr, _ := os.ReadFile(ffmpegLogPath)
		t.Fatalf("reconnected FFmpeg did not receive video frames; run=%s starts=%d status=%+v; FFmpeg log: %s; RTSP log: %s", reconnectRun.file, starts.Load(), r.Status(cfg), strings.TrimSpace(string(stderr)), rtspLogText())
	}
	if !segmentTime300.Load() {
		t.Fatal("real FFmpeg command did not contain a 300-second segment target")
	}
	if err := r.Stop(); err != nil {
		t.Fatalf("gracefully close real FFmpeg segment: %v", err)
	}
	stopRTSP(source)
	source = nil

	listResponse := req(s, "GET", "/api/record/files", "", false)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("recording list returned HTTP %d: %s", listResponse.Code, listResponse.Body.String())
	}
	var files []RecordingFile
	if err := json.Unmarshal(listResponse.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	var reconnectName string
	initialListed := false
	for _, file := range files {
		if file.Name == filepath.Base(initialRun.file) {
			initialListed = true
		}
		if strings.Contains(file.Name, "_reconnect") {
			reconnectName = file.Name
		}
	}
	if !initialListed {
		t.Fatalf("closed initial segment %s is not available through the console: %+v", filepath.Base(initialRun.file), files)
	}
	initialDownload := req(s, "GET", "/api/record/download/"+filepath.Base(initialRun.file), "", false)
	if initialDownload.Code != http.StatusOK || initialDownload.Body.Len() == 0 {
		t.Fatalf("closed initial segment download returned HTTP %d (%d bytes)", initialDownload.Code, initialDownload.Body.Len())
	}
	if reconnectName == "" {
		t.Fatalf("no closed reconnect segment is available through the console: %+v", files)
	}
	download := req(s, "GET", "/api/record/download/"+reconnectName, "", false)
	if download.Code != http.StatusOK || download.Body.Len() == 0 {
		t.Fatalf("reconnect segment download returned HTTP %d (%d bytes)", download.Code, download.Body.Len())
	}
	verifyDownloaded("reconnect", download.Body.Bytes())
	t.Logf("real FFmpeg used segment_time=300, recovered after loopback RTSP interruption, and exposed %s through list/download", reconnectName)
}

func validateFFmpegDecodeResult(processErr error, output []byte) error {
	if processErr != nil {
		return fmt.Errorf("process failed: %w; output: %s", processErr, strings.TrimSpace(string(output)))
	}
	if diagnostics := strings.TrimSpace(string(output)); diagnostics != "" {
		return fmt.Errorf("process emitted decode diagnostics: %s", diagnostics)
	}
	return nil
}

func TestValidateFFmpegDecodeResultRejectsDiagnostics(t *testing.T) {
	if err := validateFFmpegDecodeResult(nil, nil); err != nil {
		t.Fatalf("clean decode was rejected: %v", err)
	}
	if err := validateFFmpegDecodeResult(nil, []byte("Error while decoding stream")); err == nil {
		t.Fatal("decoder diagnostics were accepted with exit code 0")
	}
	if err := validateFFmpegDecodeResult(errors.New("exit status 1"), nil); err == nil {
		t.Fatal("nonzero process result was accepted")
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
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
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
	if result.CameraID != "xiaomi_0123456789abcdef" || result.HTTPWriteStatus != "sent" || result.Reply113 != "not_measured" || result.Motion != "not_measured" {
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
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
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

func TestPTZStepRejectsStaleCameraAfterSelectionChanges(t *testing.T) {
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
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
		`]}`
	var inventory managedInventory
	initialInventoryJSON := strings.ReplaceAll(inventoryJSON, `"state":"streaming"`, `"state":"starting"`)
	if err := json.Unmarshal([]byte(initialInventoryJSON), &inventory); err != nil {
		t.Fatal(err)
	}
	s.gateway.applyInventory(inventory)
	if err := s.gateway.SelectCamera(cameraB); err != nil {
		t.Fatal(err)
	}
	var gatewayPosts atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/ptz/step") {
			gatewayPosts.Add(1)
			return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
		}
		return jsonResponse(r, http.StatusNotFound, `{}`), nil
	})

	response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraA+`","direction":"up"}`, true)
	if response.Code != http.StatusConflict {
		t.Fatalf("selection race HTTP %d, want 409: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "camera selection changed") || !strings.Contains(response.Body.String(), "no request was sent") {
		t.Fatalf("stale selection did not return a clear no-write error: %s", response.Body.String())
	}
	if s.gateway.SelectedCameraID() != cameraB {
		t.Fatalf("selection after simulated concurrent change = %q, want camera B", s.gateway.SelectedCameraID())
	}
	if gatewayPosts.Load() != 0 {
		t.Fatalf("stale selection made %d PTZ writes; want no write to either camera", gatewayPosts.Load())
	}
}

func TestPTZStepUsesActiveC201SessionBeforeRTSPReadyAndRejectsEndedSession(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("pre-ready-ptz-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraID = "xiaomi_0123456789abcdef"
	var document managedInventory
	body := `{"cameras":[{"id":"` + cameraID + `","name":"C201","model":"xiaomi.camera.c201","capabilities":{"video":true,"ptz":true},"video":{"state":"idle","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/c201"}]}]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	inventory := s.gateway.applyInventory(document)
	if inventory.SessionReady || !inventory.SessionActive || inventory.Cameras[0].VideoReady {
		t.Fatalf("fixture must have active session before RTSP readiness: %+v", inventory)
	}
	var writes atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/cameras/"+cameraID+"/ptz/step" {
			t.Errorf("unexpected gateway request %s %s", r.Method, r.URL.Path)
			return jsonResponse(r, http.StatusNotFound, `{}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer pre-ready-ptz-token" {
			t.Errorf("pre-ready PTZ request was not authenticated")
		}
		writes.Add(1)
		return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
	})
	response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraID+`","direction":"left"}`, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"http_write_status":"sent"`) {
		t.Fatalf("active pre-ready PTZ HTTP %d: %s", response.Code, response.Body.String())
	}
	if writes.Load() != 1 {
		t.Fatalf("pre-ready active session sent %d writes, want exactly one", writes.Load())
	}

	var ended managedInventory
	if err := json.Unmarshal([]byte(strings.Replace(body, `"session_active":true`, `"session_active":false`, 1)), &ended); err != nil {
		t.Fatal(err)
	}
	item, found := findManagedCamera(s.gateway, ended, cameraID, "xiaomi.camera.c201")
	if !found {
		t.Fatal("ended camera disappeared from inventory")
	}
	s.gateway.setCameraSession(cameraID, item)
	response = req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraID+`","direction":"left"}`, true)
	if response.Code != http.StatusServiceUnavailable || writes.Load() != 1 {
		t.Fatalf("ended session HTTP=%d writes=%d: %s", response.Code, writes.Load(), response.Body.String())
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
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"xiaomi_0123456789abcdef","name":"Test C201","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/live"}]}]}`), nil
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

func TestGatewaySelectsSingleNonC201CameraAndUsesItsPreview(t *testing.T) {
	g := newGateway(testCfg())
	var document managedInventory
	body := `{"cameras":[{"id":"xiaomi_3333333333333333","name":"Test C400","model":"chuangmi.camera.039a04","capabilities":{"video":true,"ptz":false},"video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/c400"}]}]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	inv := g.applyInventory(document)
	if inv.SelectedCameraID != "xiaomi_3333333333333333" || !inv.SessionReady || len(inv.Cameras) != 1 || inv.Cameras[0].PTZ {
		t.Fatalf("single non-C201 inventory was not selected safely: %+v", inv)
	}
	streamURL, err := g.RTSPURL()
	if err != nil || streamURL != "rtsp://127.0.0.1:8554/c400" {
		t.Fatalf("selected non-C201 preview source = %q, %v", streamURL, err)
	}
}

func TestManagedPTZCapabilityWorksForUnknownModelAndRequiresItsActiveSession(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	cfg.PTZ.Enabled = true
	cfg.TokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(cfg.TokenFile, []byte("future-model-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraID = "xiaomi_4444444444444444"
	var document managedInventory
	body := `{"cameras":[{"id":"` + cameraID + `","name":"Future PTZ camera","model":"vendor.camera.future-ptz","capabilities":{"video":true,"ptz":true},"video":{"state":"idle","session_active":false}}]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	inventory := s.gateway.applyInventory(document)
	if inventory.C201Count != 0 || inventory.SelectedCameraID != "" || len(inventory.Cameras) != 1 || !inventory.Cameras[0].PTZ || inventory.Cameras[0].SessionActive {
		t.Fatalf("gateway capability for unknown model was lost: %+v", inventory)
	}
	var sessionStarts, writes atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/cameras" && r.URL.Query().Get("streams_for") == cameraID {
			sessionStarts.Add(1)
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"`+cameraID+`","name":"Future PTZ camera","model":"vendor.camera.future-ptz","capabilities":{"video":true,"ptz":true},"video":{"state":"starting","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/future-ptz"}]}]}`), nil
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v2/cameras/"+cameraID+"/ptz/step" {
			t.Errorf("unexpected gateway request %s %s", r.Method, r.URL.Path)
			return jsonResponse(r, http.StatusNotFound, `{}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer future-model-token" {
			t.Errorf("future-model PTZ request was not authenticated")
		}
		var step struct {
			Direction string `json:"direction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&step); err != nil || step.Direction != "left" {
			t.Errorf("PTZ request body direction=%q err=%v", step.Direction, err)
		}
		writes.Add(1)
		return jsonResponse(r, http.StatusOK, `{"write_status":"sent"}`), nil
	})
	if response := req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"`+cameraID+`"}`, true); response.Code != http.StatusOK {
		t.Fatalf("selected unknown-model session start failed: HTTP %d: %s", response.Code, response.Body.String())
	}
	active := s.gateway.inventorySnapshot()
	if sessionStarts.Load() != 1 || !active.Cameras[0].PTZ || !active.Cameras[0].SessionActive {
		t.Fatalf("session response lost generic PTZ capability: starts=%d inventory=%+v", sessionStarts.Load(), active)
	}
	response := req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraID+`","direction":"left"}`, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"http_write_status":"sent"`) || writes.Load() != 1 || sessionStarts.Load() != 1 {
		t.Fatalf("unknown-model step HTTP=%d starts=%d writes=%d: %s", response.Code, sessionStarts.Load(), writes.Load(), response.Body.String())
	}

	var ended managedInventory
	if err := json.Unmarshal([]byte(strings.Replace(body, `"session_active":true`, `"session_active":false`, 1)), &ended); err != nil {
		t.Fatal(err)
	}
	item, found := findManagedCamera(s.gateway, ended, cameraID, "vendor.camera.future-ptz")
	if !found {
		t.Fatal("future camera disappeared from inventory")
	}
	s.gateway.setCameraSession(cameraID, item)
	response = req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraID+`","direction":"left"}`, true)
	if response.Code != http.StatusServiceUnavailable || writes.Load() != 1 {
		t.Fatalf("inactive unknown-model session HTTP=%d writes=%d: %s", response.Code, writes.Load(), response.Body.String())
	}

	var unsupported managedInventory
	unsupportedBody := strings.Replace(body, `"session_active":false`, `"session_active":true`, 1)
	unsupportedBody = strings.Replace(unsupportedBody, `"ptz":true`, `"ptz":false`, 1)
	if err := json.Unmarshal([]byte(unsupportedBody), &unsupported); err != nil {
		t.Fatal(err)
	}
	item, found = findManagedCamera(s.gateway, unsupported, cameraID, "vendor.camera.future-ptz")
	if !found {
		t.Fatal("unsupported future camera disappeared from inventory")
	}
	s.gateway.setCameraSession(cameraID, item)
	response = req(s, http.MethodPost, "/api/ptz/step", `{"camera_id":"`+cameraID+`","direction":"left"}`, true)
	if response.Code != http.StatusForbidden || writes.Load() != 1 {
		t.Fatalf("unsupported unknown-model session HTTP=%d writes=%d: %s", response.Code, writes.Load(), response.Body.String())
	}
}

func TestSelectedStreamStartsOnceAndMetadataPollCanMarkC400Ready(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("c400-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenPath
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var metadataCalls, streamStarts atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			return jsonResponse(r, http.StatusOK, `{"status":"ok"}`), nil
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v2/info" {
			return jsonResponse(r, http.StatusOK, `{"api_version":"v2"}`), nil
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v2/cameras" {
			return nil, fmt.Errorf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("include_streams") == "false" {
			call := metadataCalls.Add(1)
			state, active := "idle", "false"
			if call > 1 {
				state, active = "streaming", "true"
			}
			body := `{"cameras":[{"id":"xiaomi_3333333333333333","name":"Test C400","model":"chuangmi.camera.039a04","capabilities":{"video":true,"ptz":false},"video":{"state":"` + state + `","session_active":` + active + `}}]}`
			return jsonResponse(r, http.StatusOK, body), nil
		} else if r.URL.Query().Get("streams_for") == "xiaomi_3333333333333333" {
			if streamStarts.Add(1) != 1 {
				return nil, fmt.Errorf("selected source was opened more than once")
			}
			body := `{"cameras":[{"id":"xiaomi_3333333333333333","name":"Test C400","model":"chuangmi.camera.039a04","capabilities":{"video":true,"ptz":false},"video":{"state":"idle","session_active":false},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/c400"}]}]}`
			return jsonResponse(r, http.StatusOK, body), nil
		} else {
			return nil, fmt.Errorf("unexpected stream query: %s", r.URL.RawQuery)
		}
	})
	response := req(s, http.MethodPost, "/api/inventory/refresh", "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("single C400 inventory refresh HTTP %d: %s", response.Code, response.Body.String())
	}
	if streamStarts.Load() != 0 {
		t.Fatal("inventory refresh started C400 before explicit selection")
	}
	response = req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"xiaomi_3333333333333333"}`, true)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit C400 selection HTTP %d: %s", response.Code, response.Body.String())
	}
	var inventory InventorySummary
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if streamStarts.Load() != 1 || metadataCalls.Load() != 1 {
		t.Fatalf("gateway stream starts=%d metadata reads=%d, want one start and initial inventory", streamStarts.Load(), metadataCalls.Load())
	}
	if inventory.SelectedCameraID != "xiaomi_3333333333333333" || inventory.SessionReady || inventory.SessionActive || inventory.SessionState != "idle" || len(inventory.Cameras) != 1 || inventory.Cameras[0].VideoReady || inventory.Cameras[0].PTZ {
		t.Fatalf("single C400 did not return promptly after its one source start: %+v", inventory)
	}
	status := req(s, http.MethodGet, "/api/status", "", false)
	if status.Code != http.StatusOK {
		t.Fatalf("metadata-only status HTTP %d: %s", status.Code, status.Body.String())
	}
	current := s.gateway.inventorySnapshot()
	if streamStarts.Load() != 1 || metadataCalls.Load() != 2 || !current.SessionActive || !current.SessionReady || !current.Cameras[0].VideoReady || current.Cameras[0].PTZ {
		t.Fatalf("metadata-only poll did not update C400 state without reopening: starts=%d metadata=%d inventory=%+v", streamStarts.Load(), metadataCalls.Load(), current)
	}
	if _, err := s.gateway.RTSPURL(); err != nil {
		t.Fatalf("ready C400 has no cached preview source: %v", err)
	}
}

func TestReadyRTSPMetadataWithoutActiveSessionStillStartsOnce(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("start-once-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenPath
	g := newGateway(cfg)
	const cameraID = "xiaomi_0123456789abcdef"
	var starts atomic.Int32
	g.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/cameras" || r.URL.Query().Get("streams_for") != cameraID {
			return nil, fmt.Errorf("unexpected gateway request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		starts.Add(1)
		body := `{"cameras":[{"id":"` + cameraID + `","name":"C201","model":"xiaomi.camera.c201","capabilities":{"video":true,"ptz":true},"video":{"state":"idle","session_active":false},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/c201"}]}]}`
		return jsonResponse(r, http.StatusOK, body), nil
	})
	var document managedInventory
	body := `{"cameras":[{"id":"` + cameraID + `","name":"C201","model":"xiaomi.camera.c201","capabilities":{"video":true,"ptz":true},"video":{"state":"streaming","session_active":false},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/stale"}]}]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(document)
	if err := g.StartCameraSession(context.Background(), cameraID); err != nil {
		t.Fatalf("selected video camera did not request its session: %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("selected source starts=%d, want one despite stale ready metadata", starts.Load())
	}
	snapshot := g.inventorySnapshot()
	g.mu.RLock()
	selectedRTSP := g.cameras[cameraID].rtspURL
	g.mu.RUnlock()
	if snapshot.SessionActive || snapshot.SessionReady || selectedRTSP == "" {
		t.Fatalf("inactive session state or selected RTSP URL was not kept distinct: inventory=%+v rtsp_present=%t", snapshot, selectedRTSP != "")
	}
}

func TestSelectedCameraStartRejectsMissingAllowedRTSPEndpoint(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("endpoint-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenPath
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraID = "xiaomi_0123456789abcdef"
	var streamStarts atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/cameras" {
			return nil, fmt.Errorf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
		}
		body := `{"cameras":[{"id":"` + cameraID + `","name":"C201","model":"xiaomi.camera.c201","capabilities":{"video":true,"ptz":true},"video":{"state":"idle","session_active":false}}]}`
		if r.URL.Query().Get("include_streams") == "false" {
			return jsonResponse(r, http.StatusOK, body), nil
		}
		if r.URL.Query().Get("streams_for") == cameraID {
			streamStarts.Add(1)
			return jsonResponse(r, http.StatusOK, body), nil
		}
		return nil, fmt.Errorf("unexpected camera query: %s", r.URL.RawQuery)
	})
	response := req(s, http.MethodPost, "/api/inventory/refresh", "", true)
	if response.Code != http.StatusOK || streamStarts.Load() != 0 {
		t.Fatalf("missing stream must not affect inventory refresh: HTTP %d: %s", response.Code, response.Body.String())
	}
	response = req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"`+cameraID+`"}`, true)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "allowed RTSP stream") {
		t.Fatalf("missing RTSP endpoint must be reported as not prepared; HTTP %d: %s", response.Code, response.Body.String())
	}
	if streamStarts.Load() != 1 {
		t.Fatalf("selected-source requests=%d, want exactly one", streamStarts.Load())
	}
	if snapshot := s.gateway.inventorySnapshot(); snapshot.SessionActive || snapshot.SessionReady || snapshot.Cameras[0].VideoReady {
		t.Fatalf("missing stream endpoint was published as a prepared session: %+v", snapshot)
	}
}

func TestRepeatedEOFMetadataPollingDoesNotReopenSelectedC201(t *testing.T) {
	cfg := testCfg()
	cfg.Mock = false
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("eof-test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenPath
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const cameraID = "xiaomi_0123456789abcdef"
	var metadataCalls, streamStarts atomic.Int32
	s.gateway.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			return jsonResponse(r, http.StatusOK, `{"status":"ok"}`), nil
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v2/info" {
			return jsonResponse(r, http.StatusOK, `{"api_version":"v2"}`), nil
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v2/cameras" {
			return nil, fmt.Errorf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("include_streams") == "false" {
			metadataCalls.Add(1)
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"`+cameraID+`","name":"C201","model":"xiaomi.camera.c201","video":{"state":"retrying","reason":"stream_timeout","session_active":false},"capabilities":{"video":true,"ptz":true}}]}`), nil
		}
		if r.URL.Query().Get("streams_for") == cameraID {
			if streamStarts.Add(1) != 1 {
				return nil, fmt.Errorf("EOF caused a repeated session start")
			}
			return jsonResponse(r, http.StatusOK, `{"cameras":[{"id":"`+cameraID+`","name":"C201","model":"xiaomi.camera.c201","video":{"state":"retrying","reason":"stream_timeout","session_active":false},"capabilities":{"video":true,"ptz":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/c201"}]}]}`), nil
		}
		return nil, fmt.Errorf("unexpected camera query: %s", r.URL.RawQuery)
	})
	response := req(s, http.MethodPost, "/api/inventory/refresh", "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("inventory refresh HTTP %d: %s", response.Code, response.Body.String())
	}
	response = req(s, http.MethodPost, "/api/inventory/select", `{"camera_id":"`+cameraID+`"}`, true)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit selection HTTP %d: %s", response.Code, response.Body.String())
	}
	var inventory InventorySummary
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.SelectedCameraID != cameraID || inventory.SessionActive || inventory.Cameras[0].VideoReady || streamStarts.Load() != 1 {
		t.Fatalf("EOF initial state did not remain bounded: starts=%d inventory=%+v", streamStarts.Load(), inventory)
	}
	for i := 0; i < 5; i++ {
		status := req(s, http.MethodGet, "/api/status", "", false)
		if status.Code != http.StatusOK {
			t.Fatalf("metadata status poll %d HTTP %d: %s", i+1, status.Code, status.Body.String())
		}
	}
	if streamStarts.Load() != 1 || metadataCalls.Load() != 2 {
		t.Fatalf("repeated EOF caused stream starts=%d metadata reads=%d; want one start, inventory and one terminal-state check", streamStarts.Load(), metadataCalls.Load())
	}
}

func TestGatewayStatusCopiesInventoryWhileSelectionAndRefreshRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := testCfg()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.TokenFile = tokenFile
	g := newGateway(cfg)
	g.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"status":"ok"}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	var document managedInventory
	body := `{"cameras":[` +
		`{"id":"xiaomi_1111111111111111","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/a"}]},` +
		`{"id":"xiaomi_2222222222222222","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming"},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/b"}]}]}`
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	g.applyInventory(document)
	var wg sync.WaitGroup
	errorsFound := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 250; i++ {
			status := g.Status(ctx)
			if !status.Inventory.Loaded || len(status.Inventory.Cameras) != 2 || len(status.Inventory.C201Cameras) != 2 {
				errorsFound <- fmt.Errorf("inconsistent status inventory: %+v", status.Inventory)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 250; i++ {
			g.applyInventory(document)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 250; i++ {
			id := "xiaomi_1111111111111111"
			if i%2 == 1 {
				id = "xiaomi_2222222222222222"
			}
			if err := g.SelectCamera(id); err != nil {
				errorsFound <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
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
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
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
		`{"id":"` + cameraA + `","name":"C201 A","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-a"}]},` +
		`{"id":"` + cameraB + `","name":"C201 B","model":"xiaomi.camera.c201","video":{"state":"streaming","session_active":true},"streams":[{"id":"live","protocol":"rtsp","url":"rtsp://127.0.0.1:8554/camera-b"}]}` +
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
	runID := "c201_20261111_120000_123456789"
	name := runID + "_000.mkv"
	outside := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.RecordDir, runID+".csv"), []byte(name+",0.000000,1.000000\n"), 0600); err != nil {
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
