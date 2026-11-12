package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/store"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi"
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

type fakeCameraProvider struct {
	cameras     []model.Camera
	err         error
	calls       int
	snapshot    []byte
	snapshotErr error
}

type fakeStreamProvider struct {
	*fakeCameraProvider
	streams map[model.CameraID][]backend.StreamEndpoint
}

type fakeCameraSettingsProvider struct {
	*fakeCameraProvider
	settings      backend.CameraSettings
	settingsErr   error
	settingsCalls int
	requestedID   model.CameraID
}

type fakeCameraDiagnosticsProvider struct {
	*fakeCameraProvider
	diagnostics      backend.CameraSessionDiagnostics
	diagnosticsErr   error
	diagnosticsCalls int
	requestedID      model.CameraID
}

func (p *fakeCameraDiagnosticsProvider) CameraDiagnostics(ctx context.Context, id model.CameraID) (backend.CameraSessionDiagnostics, error) {
	p.diagnosticsCalls++
	p.requestedID = id
	if err := ctx.Err(); err != nil {
		return backend.CameraSessionDiagnostics{}, err
	}
	return p.diagnostics, p.diagnosticsErr
}

func (p *fakeCameraSettingsProvider) CameraSettings(_ context.Context, id model.CameraID) (backend.CameraSettings, error) {
	p.settingsCalls++
	p.requestedID = id
	return p.settings, p.settingsErr
}

func (p *fakeStreamProvider) Streams(_ context.Context, id model.CameraID) ([]backend.StreamEndpoint, error) {
	return p.streams[id], nil
}

func (p *fakeCameraProvider) Cameras(context.Context) ([]model.Camera, error) {
	p.calls++
	return p.cameras, p.err
}

func (p *fakeCameraProvider) Snapshot(context.Context, model.CameraID) ([]byte, error) {
	return append([]byte(nil), p.snapshot...), p.snapshotErr
}

func TestManagedSnapshotRequiresAuthAndReturnsJPEG(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0x01, 0xff, 0xd9}
	provider := &fakeCameraProvider{snapshot: jpeg}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v2/cameras/c201/snapshot", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized snapshot status = %d, want 401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/v2/cameras/c201/snapshot", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" || !reflect.DeepEqual(response.Body.Bytes(), jpeg) {
		t.Fatalf("authorized snapshot = %d %q %v", response.Code, response.Header().Get("Content-Type"), response.Body.Bytes())
	}
}

func TestManagedCameraSettingsRequiresAuthAndAdvertisesCapability(t *testing.T) {
	observedAt := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	provider := &fakeCameraSettingsProvider{
		fakeCameraProvider: &fakeCameraProvider{},
		settings: backend.CameraSettings{
			ObservedAt: observedAt,
			Properties: map[string]backend.SettingValue{
				"night_shot":       {Code: 0, Value: json.RawMessage(`2`)},
				"motion_detection": {Code: 0, Value: json.RawMessage(`true`)},
			},
		},
	}
	state, err := store.Open(filepath.Join(t.TempDir(), "managed-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewWithManagedState(config.Config{}, "secret", nil, provider, nil, state)

	path := "/v2/cameras/xiaomi_0123456789abcdef/settings"
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != http.StatusUnauthorized || provider.settingsCalls != 0 {
		t.Fatalf("unauthorized settings = %d, provider calls=%d; want 401 and no read", unauthorized.Code, provider.settingsCalls)
	}

	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || provider.settingsCalls != 1 || provider.requestedID != model.CameraID("xiaomi_0123456789abcdef") {
		t.Fatalf("authorized settings = %d %s, calls=%d id=%q", response.Code, response.Body.String(), provider.settingsCalls, provider.requestedID)
	}
	var got backend.CameraSettings
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if !got.ObservedAt.Equal(observedAt) || string(got.Properties["night_shot"].Value) != "2" || string(got.Properties["motion_detection"].Value) != "true" {
		t.Fatalf("settings response = %#v", got)
	}

	infoRequest := httptest.NewRequest(http.MethodGet, "/v2/info", nil)
	infoRequest.Header.Set("Authorization", "Bearer secret")
	infoResponse := httptest.NewRecorder()
	h.ServeHTTP(infoResponse, infoRequest)
	var info RuntimeInfo
	if infoResponse.Code != http.StatusOK || json.Unmarshal(infoResponse.Body.Bytes(), &info) != nil {
		t.Fatalf("managed info = %d %s", infoResponse.Code, infoResponse.Body.String())
	}
	if !reflect.DeepEqual(info.Features, []string{"camera_selection", "camera_settings_read"}) {
		t.Fatalf("managed features = %v", info.Features)
	}
}

type sharedInventoryProvider struct {
	inventory     []model.Camera
	expectedCalls int64
	calls         atomic.Int64
	ready         chan struct{}
}

func (p *sharedInventoryProvider) Cameras(ctx context.Context) ([]model.Camera, error) {
	if p.calls.Add(1) == p.expectedCalls {
		close(p.ready)
	}
	select {
	case <-p.ready:
		return p.inventory, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return nil, errors.New("timed out waiting for concurrent inventory reads")
	}
}

type fakeManagedPTZ struct{}

func (fakeManagedPTZ) Step(context.Context, model.CameraID, string) error { return nil }
func (fakeManagedPTZ) Stop(context.Context, model.CameraID) error         { return nil }
func (fakeManagedPTZ) Preset(context.Context, model.CameraID, string) error {
	return nil
}

type fakeManagedStepProvider struct {
	*fakeCameraProvider
	calls   []string
	stepErr error
}

func (p *fakeManagedStepProvider) Step(_ context.Context, id model.CameraID, direction string) error {
	p.calls = append(p.calls, string(id)+":"+direction)
	return p.stepErr
}

func TestManagedXiaomiPTZStepAuthValidationAndSingleDispatch(t *testing.T) {
	provider := &fakeManagedStepProvider{fakeCameraProvider: &fakeCameraProvider{}}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	validPath := "/v2/cameras/xiaomi_0123456789abcdef/ptz/step"

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, validPath, strings.NewReader(`{"direction":"left"}`)))
	if unauthorized.Code != http.StatusUnauthorized || len(provider.calls) != 0 {
		t.Fatalf("unauthorized request = %d, calls=%v; want 401 and no dispatch", unauthorized.Code, provider.calls)
	}

	for _, testCase := range []struct {
		name string
		path string
		body string
	}{
		{name: "malformed JSON", path: validPath, body: `{"direction":`},
		{name: "unknown field", path: validPath, body: `{"direction":"left","speed":1}`},
		{name: "duplicate direction", path: validPath, body: `{"direction":"right","direction":"left"}`},
		{name: "trailing JSON", path: validPath, body: `{"direction":"left"} {}`},
		{name: "unknown direction", path: validPath, body: `{"direction":"diagonal"}`},
		{name: "malformed camera ID", path: "/v2/cameras/c201/ptz/step", body: `{"direction":"left"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader(testCase.body))
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || len(provider.calls) != 0 {
				t.Fatalf("request = %d %s, calls=%v; want 400 and no dispatch", response.Code, response.Body.String(), provider.calls)
			}
		})
	}
	if provider.fakeCameraProvider.calls != 0 {
		t.Fatalf("step route queried camera inventory %d times", provider.fakeCameraProvider.calls)
	}

	for _, direction := range []string{"left", "right", "up", "down"} {
		request := httptest.NewRequest(http.MethodPost, validPath, strings.NewReader(`{"direction":"`+direction+`"}`))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"write_status":"sent"`) {
			t.Fatalf("valid %s request = %d %s; want write_status sent", direction, response.Code, response.Body.String())
		}
	}
	wantCalls := []string{
		"xiaomi_0123456789abcdef:left",
		"xiaomi_0123456789abcdef:right",
		"xiaomi_0123456789abcdef:up",
		"xiaomi_0123456789abcdef:down",
	}
	if !reflect.DeepEqual(provider.calls, wantCalls) {
		t.Fatalf("Step calls = %v; want one step per direction", provider.calls)
	}
	if provider.fakeCameraProvider.calls != 0 {
		t.Fatalf("step route queried camera inventory %d times", provider.fakeCameraProvider.calls)
	}
}

func TestManagedXiaomiPTZStepSanitizesProviderFailure(t *testing.T) {
	provider := &fakeManagedStepProvider{
		fakeCameraProvider: &fakeCameraProvider{},
		stepErr:            errors.New("private account token and session URL"),
	}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodPost, "/v2/cameras/xiaomi_0123456789abcdef/ptz/step", strings.NewReader(`{"direction":"left"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "token") {
		t.Fatalf("provider failure leaked details: %d %s", response.Code, response.Body.String())
	}
}

func TestManagedXiaomiPTZStepMapsCameraAndSessionErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		stepErr    error
		wantStatus int
		wantError  string
	}{
		{name: "unknown camera", stepErr: xiaomi.ErrCameraNotFound, wantStatus: http.StatusNotFound, wantError: "camera_not_found"},
		{name: "known unsupported model", stepErr: xiaomi.ErrUnsupportedCamera, wantStatus: http.StatusBadGateway, wantError: "ptz_step_failed"},
		{name: "unavailable session", stepErr: xiaomi.ErrCameraUnavailable, wantStatus: http.StatusServiceUnavailable, wantError: string(model.ErrorBackendUnavailable)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &fakeManagedStepProvider{
				fakeCameraProvider: &fakeCameraProvider{},
				stepErr:            testCase.stepErr,
			}
			h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
			request := httptest.NewRequest(http.MethodPost, "/v2/cameras/xiaomi_0123456789abcdef/ptz/step", strings.NewReader(`{"direction":"left"}`))
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus || !strings.Contains(response.Body.String(), `"error":"`+testCase.wantError+`"`) {
				t.Fatalf("response = %d %s; want %d with %q", response.Code, response.Body.String(), testCase.wantStatus, testCase.wantError)
			}
			if len(provider.calls) != 1 || provider.fakeCameraProvider.calls != 0 {
				t.Fatalf("Step calls = %v, inventory calls = %d; want one local Step dispatch and no inventory lookup", provider.calls, provider.fakeCameraProvider.calls)
			}
		})
	}
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

func TestLegacyPTZKeepsPermissiveJSONDecoding(t *testing.T) {
	cfg := config.Config{Cameras: []config.Camera{{ID: "garden", Name: "Garden", RTSPURL: "rtsp://camera/garden", ONVIFURL: "http://camera/onvif/ptz_service", PTZ: true}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	fake := &fakePTZ{}
	h := New(cfg, "secret", fake)
	for _, testCase := range []struct {
		path string
		body string
		call string
	}{
		{"/v1/cameras/garden/ptz/step", `{"direction":"left"} {}`, "step:left"},
		{"/v1/cameras/garden/ptz/preset", `{"token":"home"} {}`, "preset:home"},
	} {
		req := httptest.NewRequest(http.MethodPost, testCase.path, strings.NewReader(testCase.body))
		req.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("path %q body %q: status %d, want %d: %s", testCase.path, testCase.body, response.Code, http.StatusOK, response.Body.String())
		}
		if len(fake.calls) != 1 || fake.calls[0] != testCase.call {
			t.Fatalf("path %q calls = %v, want [%q]", testCase.path, fake.calls, testCase.call)
		}
		fake.calls = nil
	}
}

func TestManagedSelectionRejectsTrailingJSONWithoutChangingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReplaceSelection(0, []string{"existing"}); err != nil {
		t.Fatal(err)
	}
	h := NewWithManagedState(config.Config{}, "secret", nil, nil, nil, state)
	request := httptest.NewRequest(http.MethodPut, "/v2/selection", strings.NewReader(`{"revision":1,"camera_ids":["other"]} {}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"error":"invalid_json"`) {
		t.Fatalf("trailing JSON status/body = %d/%s, want 400 invalid_json", response.Code, response.Body.String())
	}
	want := []string{"existing"}
	if got := state.Snapshot(); got.SelectionRevision != 1 || !reflect.DeepEqual(got.SelectedCameraIDs, want) {
		t.Fatalf("invalid trailing JSON changed in-memory selection: %#v", got)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen state after invalid request: %v", err)
	}
	if got := reopened.Snapshot(); got.SelectionRevision != 1 || !reflect.DeepEqual(got.SelectedCameraIDs, want) {
		t.Fatalf("invalid trailing JSON changed persisted selection: %#v", got)
	}
}

func TestManagedCameraListRequiresAuth(t *testing.T) {
	provider := &fakeCameraProvider{}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/cameras", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times before auth", provider.calls)
	}

	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", response.Code, http.StatusOK)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestManagedInfoRequiresAuthAndReturnsPublicMetadata(t *testing.T) {
	h := NewWithRuntimeInfo(config.Config{}, "gateway-token", nil, nil, nil, RuntimeInfo{
		InstanceID: "inst_0123456789abcdef0123456789abcdef",
		APIVersion: "2.0",
		Features:   []string{},
	})

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v2/info", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodGet, "/v2/info", nil)
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", response.Code, http.StatusOK)
	}
	var got RuntimeInfo
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode /v2/info: %v", err)
	}
	want := RuntimeInfo{
		InstanceID: "inst_0123456789abcdef0123456789abcdef",
		APIVersion: "2.0",
		Features:   []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("/v2/info = %#v, want %#v", got, want)
	}
	if strings.Contains(response.Body.String(), "gateway-token") || strings.Contains(response.Body.String(), "camera_id") {
		t.Fatalf("/v2/info leaked private metadata: %s", response.Body.String())
	}
}

func TestLegacyHandlerDoesNotEnableManagedInfo(t *testing.T) {
	h := New(config.Config{}, "gateway-token", nil)
	for _, methodPath := range []string{
		http.MethodGet + " /v2/info",
		http.MethodPut + " /v2/selection",
	} {
		method, path, _ := strings.Cut(methodPath, " ")
		request := httptest.NewRequest(method, path, strings.NewReader(`{"revision":0,"camera_ids":[]}`))
		request.Header.Set("Authorization", "Bearer gateway-token")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy %s status = %d, want %d", methodPath, response.Code, http.StatusNotFound)
		}
	}
}

func TestManagedSelectionPersistsAndReturnsEmptyInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	instanceID := state.Snapshot().InstanceID
	h := NewWithManagedState(config.Config{}, "secret", nil, nil, nil, state)

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPut, "/v2/selection", strings.NewReader(`{"revision":0,"camera_ids":["cam_a"]}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized selection status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	if got := state.Snapshot(); got.SelectionRevision != 0 || len(got.SelectedCameraIDs) != 0 {
		t.Fatalf("unauthorized request changed selection: %#v", got)
	}

	put := func(handler http.Handler, payload string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/v2/selection", strings.NewReader(payload))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	getCameras := func(handler http.Handler) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	initialInventory := getCameras(h)
	if initialInventory.Code != http.StatusOK || !strings.Contains(initialInventory.Body.String(), `"cameras":[]`) {
		t.Fatalf("empty managed inventory = %d %s, want 200 with []", initialInventory.Code, initialInventory.Body.String())
	}

	first := put(h, `{"revision":0,"camera_ids":["cam_a"]}`)
	var firstSelection managedSelectionResponse
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &firstSelection) != nil {
		t.Fatalf("first selection = %d %s", first.Code, first.Body.String())
	}
	if firstSelection.Revision != 1 || !reflect.DeepEqual(firstSelection.CameraIDs, []string{"cam_a"}) {
		t.Fatalf("first selection response = %#v", firstSelection)
	}
	if response := getCameras(h); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("selected inventory without provider status = %d, want 503", response.Code)
	}

	idempotent := put(h, `{"revision":1,"camera_ids":["cam_a"]}`)
	var idempotentSelection managedSelectionResponse
	if idempotent.Code != http.StatusOK || json.Unmarshal(idempotent.Body.Bytes(), &idempotentSelection) != nil || idempotentSelection.Revision != 1 {
		t.Fatalf("idempotent selection = %d %s", idempotent.Code, idempotent.Body.String())
	}

	conflict := put(h, `{"revision":0,"camera_ids":["cam_b"]}`)
	var conflictSelection managedSelectionResponse
	if conflict.Code != http.StatusConflict || json.Unmarshal(conflict.Body.Bytes(), &conflictSelection) != nil {
		t.Fatalf("stale selection = %d %s, want 409", conflict.Code, conflict.Body.String())
	}
	if conflictSelection.Revision != 1 || !reflect.DeepEqual(conflictSelection.CameraIDs, []string{"cam_a"}) {
		t.Fatalf("conflict response = %#v", conflictSelection)
	}
	if got := state.Snapshot(); got.SelectionRevision != 1 || !reflect.DeepEqual(got.SelectedCameraIDs, []string{"cam_a"}) {
		t.Fatalf("conflict changed persisted selection: %#v", got)
	}

	removed := put(h, `{"revision":1,"camera_ids":[]}`)
	var emptySelection managedSelectionResponse
	if removed.Code != http.StatusOK || json.Unmarshal(removed.Body.Bytes(), &emptySelection) != nil {
		t.Fatalf("remove-last-camera response = %d %s", removed.Code, removed.Body.String())
	}
	if emptySelection.Revision != 2 || emptySelection.CameraIDs == nil || len(emptySelection.CameraIDs) != 0 {
		t.Fatalf("empty selection response = %#v", emptySelection)
	}
	if inventory := getCameras(h); inventory.Code != http.StatusOK || !strings.Contains(inventory.Body.String(), `"cameras":[]`) {
		t.Fatalf("empty inventory after removal = %d %s", inventory.Code, inventory.Body.String())
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen managed state: %v", err)
	}
	restarted := NewWithManagedState(config.Config{}, "secret", nil, nil, nil, reopened)
	infoRequest := httptest.NewRequest(http.MethodGet, "/v2/info", nil)
	infoRequest.Header.Set("Authorization", "Bearer secret")
	infoResponse := httptest.NewRecorder()
	restarted.ServeHTTP(infoResponse, infoRequest)
	var info RuntimeInfo
	if infoResponse.Code != http.StatusOK || json.Unmarshal(infoResponse.Body.Bytes(), &info) != nil {
		t.Fatalf("restarted info = %d %s", infoResponse.Code, infoResponse.Body.String())
	}
	if info.InstanceID != instanceID || info.APIVersion != ManagedAPIVersion || !reflect.DeepEqual(info.Features, []string{"camera_selection"}) {
		t.Fatalf("managed info after restart = %#v, want stable identity/version/selection feature", info)
	}
	if got := reopened.Snapshot(); got.SelectionRevision != 2 || got.SelectedCameraIDs == nil || len(got.SelectedCameraIDs) != 0 {
		t.Fatalf("persisted selection after restart = %#v", got)
	}
	if inventory := getCameras(restarted); inventory.Code != http.StatusOK || !strings.Contains(inventory.Body.String(), `"cameras":[]`) {
		t.Fatalf("restarted empty inventory = %d %s", inventory.Code, inventory.Body.String())
	}
}

func TestManagedSelectionRejectsInvalidAndDuplicateIDs(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "managed-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewWithManagedState(config.Config{}, "secret", nil, nil, nil, state)
	for _, testCase := range []struct {
		payload   string
		wantError string
	}{
		{`{"revision":0,"camera_ids":["cam_a","cam_a"]}`, `"error":"duplicate_camera_id"`},
		{`{"revision":0,"camera_ids":[""]}`, `"error":"invalid_camera_id"`},
		{`{"revision":0,"camera_ids":[" cam_a"]}`, `"error":"invalid_camera_id"`},
		{`{"revision":0,"camera_ids":null}`, `"error":"invalid_selection"`},
		{`{"camera_ids":[]}`, `"error":"invalid_selection"`},
	} {
		request := httptest.NewRequest(http.MethodPut, "/v2/selection", strings.NewReader(testCase.payload))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("payload %s: status = %d, want 400 (%s)", testCase.payload, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), testCase.wantError) {
			t.Errorf("payload %s: body = %s, want %s", testCase.payload, response.Body.String(), testCase.wantError)
		}
		if got := state.Snapshot(); got.SelectionRevision != 0 || got.SelectedCameraIDs == nil || len(got.SelectedCameraIDs) != 0 {
			t.Fatalf("payload %s changed selection: %#v", testCase.payload, got)
		}
	}
}

func TestManagedCameraListEmptyIsJSONArray(t *testing.T) {
	provider := &fakeCameraProvider{cameras: nil}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}

	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Cameras == nil || len(got.Cameras) != 0 {
		t.Fatalf("cameras = %#v, want non-nil empty list", got.Cameras)
	}
}

func TestManagedCameraListMatchesSharedStateFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "tests", "fixtures", "managed-cameras.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var want managedCameraList
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatalf("decode shared fixture: %v", err)
	}
	fixtureCameras := make([]model.Camera, len(want.Cameras))
	for index, camera := range want.Cameras {
		fixtureCameras[index] = camera.Camera
	}
	provider := &fakeCameraProvider{cameras: fixtureCameras}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response does not match shared fixture\n got: %#v\nwant: %#v", got, want)
	}
	if got.Cameras[0].Video.State != model.VideoOffline || got.Cameras[0].Video.Reason != model.ErrorLANUnreachable {
		t.Fatalf("offline state = %#v, want offline/lan_unreachable", got.Cameras[0].Video)
	}
	if got.Cameras[1].Video.State != model.VideoAuthRequired || got.Cameras[1].Video.Reason != model.ErrorAuthRequired {
		t.Fatalf("auth state = %#v, want auth_required/auth_required", got.Cameras[1].Video)
	}
	for _, camera := range got.Cameras {
		if camera.Capabilities.PTZ || camera.Capabilities.Presets || camera.Control.State != model.ControlUnsupported {
			t.Fatalf("video-only provider advertised PTZ for %q: %#v", camera.ID, camera)
		}
	}
}

func TestCloneManagedCamerasCopiesNestedSupportEvidence(t *testing.T) {
	source := []model.Camera{{
		ID: "cam_a1b2", Name: "C201",
		Support: &model.CameraSupport{
			SchemaVersion: 1,
			Features: map[string]model.FeatureSupport{
				"video": {Implementation: "implemented", Validation: "device", Path: "camera_lan", RequiresCloud: []string{"new_session_negotiation"}},
			},
		},
	}}
	copy := cloneManagedCameras(source)
	copy[0].Support.Features["video"] = model.FeatureSupport{Implementation: "absent", Validation: "none", Path: "unknown"}
	if got := source[0].Support.Features["video"].Implementation; got != "implemented" {
		t.Fatalf("mutating response changed provider feature metadata to %q", got)
	}
	copy = cloneManagedCameras(source)
	feature := copy[0].Support.Features["video"]
	feature.RequiresCloud[0] = "modified"
	copy[0].Support.Features["video"] = feature
	if got := source[0].Support.Features["video"].RequiresCloud[0]; got != "new_session_negotiation" {
		t.Fatalf("mutating response changed provider cloud requirements to %q", got)
	}
}

func TestManagedCameraListOnlyAdvertisesAvailableSupportProviders(t *testing.T) {
	camera := model.Camera{
		ID: "cam_a1b2", Name: "C201",
		Capabilities: model.Capabilities{Video: true, PTZ: true, Presets: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlReady},
		Support: &model.CameraSupport{SchemaVersion: 1, Features: map[string]model.FeatureSupport{
			"video":                {Implementation: "implemented", Validation: "device", Path: "camera_lan"},
			"ptz":                  {Implementation: "implemented", Validation: "device", Path: "camera_lan"},
			"presets":              {Implementation: "research", Validation: "none", Path: "unknown"},
			"camera_settings_read": {Implementation: "implemented", Validation: "offline", Path: "cloud", RequiresCloud: []string{"account_auth", "inventory", "settings_read"}},
		}},
	}
	h := NewWithProviders(config.Config{}, "secret", nil, &fakeCameraProvider{cameras: []model.Camera{camera}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cameras) != 1 || got.Cameras[0].Support == nil {
		t.Fatalf("camera support metadata missing: %#v", got.Cameras)
	}
	if _, ok := got.Cameras[0].Support.Features["video"]; !ok || len(got.Cameras[0].Support.Features) != 1 {
		t.Fatalf("API advertised support without injected PTZ/settings providers: %#v", got.Cameras[0].Support.Features)
	}
}

func TestManagedCameraListDropsInvalidOptionalSupportPerCamera(t *testing.T) {
	valid := model.Camera{
		ID: "cam_valid", Name: "Valid",
		Capabilities: model.Capabilities{Video: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlUnsupported},
		Support: &model.CameraSupport{SchemaVersion: 1, Features: map[string]model.FeatureSupport{
			"video": {Implementation: "implemented", Validation: "offline", Path: "camera_lan", Availability: "available", RequiresCloud: []string{}},
		}},
	}
	unknownVersion := model.Camera{
		ID: "cam_future", Name: "Future schema",
		Capabilities: model.Capabilities{Video: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlUnsupported},
		Support:      &model.CameraSupport{SchemaVersion: 2, Features: map[string]model.FeatureSupport{}},
	}
	malformed := model.Camera{
		ID: "cam_malformed", Name: "Malformed support",
		Capabilities: model.Capabilities{Video: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlUnsupported},
		Support: &model.CameraSupport{SchemaVersion: 1, Features: map[string]model.FeatureSupport{
			"video": {Implementation: "invented", Validation: "offline", Path: "camera_lan", Availability: "available", RequiresCloud: []string{}},
		}},
	}
	h := NewWithProviders(config.Config{}, "secret", nil, &fakeCameraProvider{cameras: []model.Camera{valid, unknownVersion, malformed}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with optional metadata ignored: %s", response.Code, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cameras) != 3 {
		t.Fatalf("mixed camera inventory length = %d, want 3", len(got.Cameras))
	}
	for _, camera := range got.Cameras {
		if !camera.Capabilities.Video {
			t.Errorf("optional support failure changed capability on %q", camera.ID)
		}
	}
	if got.Cameras[0].Support == nil || got.Cameras[1].Support != nil || got.Cameras[2].Support != nil {
		t.Fatalf("support normalization was not isolated per camera: %#v", got.Cameras)
	}
}

func TestCameraSessionDiagnosticsRouteIsAuthenticatedAndOptIn(t *testing.T) {
	provider := &fakeCameraDiagnosticsProvider{
		fakeCameraProvider: &fakeCameraProvider{},
		diagnostics: backend.CameraSessionDiagnostics{
			State: backend.SessionStreaming, Protocol: "cs2+tcp", PeerClass: "private_ip", Frames: 42, Bytes: 8192,
		},
	}
	info := RuntimeInfo{InstanceID: "inst_0123456789abcdef0123456789abcdef", APIVersion: ManagedAPIVersion, Features: []string{"camera_selection"}}
	route := "/v2/cameras/xiaomi_0123456789abcdef/diagnostics"

	defaultHandler := NewWithRuntimeInfo(config.Config{}, "secret", nil, provider, nil, info)
	defaultRequest := httptest.NewRequest(http.MethodGet, route, nil)
	defaultRequest.Header.Set("Authorization", "Bearer secret")
	defaultResponse := httptest.NewRecorder()
	defaultHandler.ServeHTTP(defaultResponse, defaultRequest)
	if defaultResponse.Code != http.StatusNotFound || provider.diagnosticsCalls != 0 {
		t.Fatalf("diagnostics route should be disabled by default: status=%d calls=%d", defaultResponse.Code, provider.diagnosticsCalls)
	}

	info.EnableDiagnostics = true
	enabledHandler := NewWithRuntimeInfo(config.Config{}, "secret", nil, provider, nil, info)
	unauthorized := httptest.NewRecorder()
	enabledHandler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, route, nil))
	if unauthorized.Code != http.StatusUnauthorized || provider.diagnosticsCalls != 0 {
		t.Fatalf("unauthorized diagnostics response = %d, provider calls=%d", unauthorized.Code, provider.diagnosticsCalls)
	}

	request := httptest.NewRequest(http.MethodGet, route, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	enabledHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || provider.diagnosticsCalls != 1 || provider.requestedID != "xiaomi_0123456789abcdef" {
		t.Fatalf("authorized diagnostics = %d calls=%d id=%q: %s", response.Code, provider.diagnosticsCalls, provider.requestedID, response.Body.String())
	}
	var got backend.CameraSessionDiagnostics
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != backend.SessionStreaming || got.PeerClass != "private_ip" || got.Frames != 42 {
		t.Fatalf("diagnostics response = %#v", got)
	}
	infoRequest := httptest.NewRequest(http.MethodGet, "/v2/info", nil)
	infoRequest.Header.Set("Authorization", "Bearer secret")
	infoResponse := httptest.NewRecorder()
	enabledHandler.ServeHTTP(infoResponse, infoRequest)
	if infoResponse.Code != http.StatusOK || !strings.Contains(infoResponse.Body.String(), `"camera_session_diagnostics"`) {
		t.Fatalf("enabled /v2/info did not advertise diagnostics: status=%d body=%s", infoResponse.Code, infoResponse.Body.String())
	}
}

func TestCameraSessionDiagnosticsRouteHonorsRequestCancellation(t *testing.T) {
	provider := &fakeCameraDiagnosticsProvider{fakeCameraProvider: &fakeCameraProvider{}}
	h := NewWithRuntimeInfo(config.Config{}, "secret", nil, provider, nil, RuntimeInfo{
		InstanceID: "inst_0123456789abcdef0123456789abcdef", APIVersion: ManagedAPIVersion, EnableDiagnostics: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras/xiaomi_0123456789abcdef/diagnostics", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusRequestTimeout || provider.diagnosticsCalls != 1 || !strings.Contains(response.Body.String(), `"error":"timeout"`) {
		t.Fatalf("canceled diagnostics = %d calls=%d body=%s", response.Code, provider.diagnosticsCalls, response.Body.String())
	}
}

func TestManagedCameraListIncludesAuthorizedCredentialFreeStreams(t *testing.T) {
	camera := model.Camera{
		ID: "cam-1", Name: "Camera",
		Capabilities: model.Capabilities{Video: true, Snapshot: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlUnsupported},
	}
	provider := &fakeStreamProvider{
		fakeCameraProvider: &fakeCameraProvider{cameras: []model.Camera{camera}},
		streams: map[model.CameraID][]backend.StreamEndpoint{
			"cam-1": {{ID: "main", Protocol: "rtsp", URL: "rtsp://gateway:8554/cam-1"}},
		},
	}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cameras) != 1 || len(got.Cameras[0].Streams) != 1 || got.Cameras[0].Streams[0].URL != "rtsp://gateway:8554/cam-1" {
		t.Fatalf("managed camera stream = %#v", got.Cameras)
	}
}

func TestManagedCameraListRejectsCredentialBearingStream(t *testing.T) {
	provider := &fakeStreamProvider{
		fakeCameraProvider: &fakeCameraProvider{cameras: []model.Camera{{
			ID: "cam-1", Name: "Camera", Capabilities: model.Capabilities{Video: true},
			Video: model.VideoStatus{State: model.VideoIdle}, Control: model.ControlStatus{State: model.ControlUnsupported},
		}}},
		streams: map[model.CameraID][]backend.StreamEndpoint{
			"cam-1": {{ID: "main", Protocol: "rtsp", URL: "rtsp://user:secret@gateway:8554/cam-1"}},
		},
	}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("credential-bearing stream response = %d %s", response.Code, response.Body.String())
	}
}

func TestManagedCameraListDowngradesPTZWithoutOptionalProvider(t *testing.T) {
	camera := model.Camera{
		ID:   "camera-one",
		Name: "Camera one",
		Capabilities: model.Capabilities{
			Video:   true,
			PTZ:     true,
			Presets: true,
		},
		Video:   model.VideoStatus{State: model.VideoIdle},
		Control: model.ControlStatus{State: model.ControlReady},
	}
	provider := &fakeCameraProvider{cameras: []model.Camera{camera}}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Cameras) != 1 || got.Cameras[0].Capabilities.PTZ || got.Cameras[0].Capabilities.Presets || got.Cameras[0].Control.State != model.ControlUnsupported {
		t.Fatalf("PTZ without optional provider was not downgraded: %#v", got.Cameras)
	}
}

func TestManagedCameraListProviderFailureIsSanitized(t *testing.T) {
	provider := &fakeCameraProvider{err: errors.New("private upstream details")}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(response.Body.String(), "private upstream details") || !strings.Contains(response.Body.String(), string(model.ErrorBackendUnavailable)) {
		t.Fatalf("provider error was not sanitized: %s", response.Body.String())
	}
}

func TestManagedPTZProviderIsOptionalAndInjectable(t *testing.T) {
	var _ interface {
		Step(context.Context, model.CameraID, string) error
		Stop(context.Context, model.CameraID) error
		Preset(context.Context, model.CameraID, string) error
	} = fakeManagedPTZ{}
	camera := model.Camera{
		ID:           "camera-one",
		Name:         "Camera one",
		Capabilities: model.Capabilities{Video: true, PTZ: true},
		Video:        model.VideoStatus{State: model.VideoIdle},
		Control:      model.ControlStatus{State: model.ControlReady},
	}
	provider := &fakeCameraProvider{cameras: []model.Camera{camera}}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, fakeManagedPTZ{})
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got managedCameraList
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Cameras) != 1 || !got.Cameras[0].Capabilities.PTZ {
		t.Fatalf("injected PTZ provider was not honored: %#v", got.Cameras)
	}
}

func TestManagedCameraListUnavailableWithoutProvider(t *testing.T) {
	h := New(config.Config{}, "secret", nil)
	request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), string(model.ErrorBackendUnavailable)) {
		t.Fatalf("status/body = %d/%s, want 503 backend_unavailable", response.Code, response.Body.String())
	}
}

func TestManagedCameraListDoesNotMutateSharedProviderInventory(t *testing.T) {
	const concurrentRequests = 64
	original := model.Camera{
		ID:   "shared-camera",
		Name: "Shared camera",
		Capabilities: model.Capabilities{
			Video:   true,
			PTZ:     true,
			Presets: true,
		},
		Video:   model.VideoStatus{State: model.VideoIdle},
		Control: model.ControlStatus{State: model.ControlReady},
	}
	provider := &sharedInventoryProvider{
		inventory:     []model.Camera{original},
		expectedCalls: concurrentRequests,
		ready:         make(chan struct{}),
	}
	h := NewWithProviders(config.Config{}, "secret", nil, provider, nil)
	failures := make(chan error, concurrentRequests)
	var workers sync.WaitGroup
	workers.Add(concurrentRequests)
	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer workers.Done()
			request := httptest.NewRequest(http.MethodGet, "/v2/cameras", nil)
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				failures <- errors.New("concurrent GET /v2/cameras did not return 200")
				return
			}
			var got managedCameraList
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				failures <- errors.New("concurrent camera response was not valid JSON")
				return
			}
			if len(got.Cameras) != 1 || got.Cameras[0].Capabilities.PTZ || got.Cameras[0].Capabilities.Presets || got.Cameras[0].Control.State != model.ControlUnsupported {
				failures <- errors.New("concurrent response did not normalize unsupported PTZ")
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if got := provider.calls.Load(); got != concurrentRequests {
		t.Errorf("provider calls = %d, want %d", got, concurrentRequests)
	}
	if !reflect.DeepEqual(provider.inventory, []model.Camera{original}) {
		t.Errorf("GET mutated the provider's shared inventory: got %#v, want %#v", provider.inventory, original)
	}
}
