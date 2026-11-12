package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/store"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi"
)

const ManagedAPIVersion = "2.0"

type PTZ interface {
	Step(context.Context, string, string, string, float64) error
	Stop(context.Context, string, string) error
	Preset(context.Context, string, string, string) error
}

type managedStepProvider interface {
	Step(context.Context, model.CameraID, string) error
}

// RuntimeInfo contains only public, non-secret managed-runtime metadata.
type RuntimeInfo struct {
	InstanceID        string   `json:"instance_id"`
	APIVersion        string   `json:"api_version"`
	Features          []string `json:"features"`
	EnableDiagnostics bool     `json:"-"`
}

type Handler struct {
	cfg                 config.Config
	apiToken            string
	ptz                 PTZ
	cameraProvider      backend.CameraProvider
	settingsProvider    backend.CameraSettingsProvider
	managedStep         managedStepProvider
	managedPTZ          backend.PTZProvider
	snapshotProvider    backend.SnapshotProvider
	diagnosticsProvider backend.CameraDiagnosticsProvider
	runtimeInfo         RuntimeInfo
	managedState        *store.Store
	locks               map[string]*sync.Mutex
}

type PublicCamera struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	RTSPURL string `json:"rtsp_url"`
	PTZ     bool   `json:"ptz"`
	Mode    string `json:"mode"`
}

func New(cfg config.Config, token string, ptz PTZ) http.Handler {
	return NewWithProviders(cfg, token, ptz, nil, nil)
}

// NewWithProviders adds the optional managed camera provider while preserving
// the legacy /v1 PTZ adapter and routes. Managed PTZ remains a separate,
// optional interface so video-only providers do not need stub controls.
func NewWithProviders(cfg config.Config, token string, ptz PTZ, cameras backend.CameraProvider, managedPTZ backend.PTZProvider) http.Handler {
	return NewWithRuntimeInfo(cfg, token, ptz, cameras, managedPTZ, RuntimeInfo{})
}

// NewWithRuntimeInfo adds public managed-runtime metadata without changing
// legacy constructors or routes. /v2/info is registered only when the caller
// supplies a persisted instance identity and API version.
func NewWithRuntimeInfo(cfg config.Config, token string, ptz PTZ, cameras backend.CameraProvider, managedPTZ backend.PTZProvider, runtimeInfo RuntimeInfo) http.Handler {
	return newWithRuntimeInfo(cfg, token, ptz, cameras, managedPTZ, runtimeInfo, nil)
}

// NewWithManagedState enables the managed info and selection routes using the
// persisted state store. The static legacy constructors do not register them.
func NewWithManagedState(cfg config.Config, token string, ptz PTZ, cameras backend.CameraProvider, managedPTZ backend.PTZProvider, state *store.Store) http.Handler {
	return NewWithManagedStateAndDiagnostics(cfg, token, ptz, cameras, managedPTZ, state, false)
}

// NewWithManagedStateAndDiagnostics enables the metadata-only session route
// only when explicitly requested by the operator and the provider supports it.
func NewWithManagedStateAndDiagnostics(cfg config.Config, token string, ptz PTZ, cameras backend.CameraProvider, managedPTZ backend.PTZProvider, state *store.Store, enableDiagnostics bool) http.Handler {
	var runtimeInfo RuntimeInfo
	if state != nil {
		features := []string{"camera_selection"}
		if _, supported := cameras.(backend.CameraSettingsProvider); supported {
			features = append(features, "camera_settings_read")
		}
		if _, supported := cameras.(backend.CameraDiagnosticsProvider); supported && enableDiagnostics {
			features = append(features, "camera_session_diagnostics")
		}
		runtimeInfo = RuntimeInfo{
			InstanceID:        state.Snapshot().InstanceID,
			APIVersion:        ManagedAPIVersion,
			Features:          features,
			EnableDiagnostics: enableDiagnostics,
		}
	}
	return newWithRuntimeInfo(cfg, token, ptz, cameras, managedPTZ, runtimeInfo, state)
}

func containsFeature(features []string, feature string) bool {
	for _, current := range features {
		if current == feature {
			return true
		}
	}
	return false
}

func newWithRuntimeInfo(cfg config.Config, token string, ptz PTZ, cameras backend.CameraProvider, managedPTZ backend.PTZProvider, runtimeInfo RuntimeInfo, state *store.Store) http.Handler {
	if runtimeInfo.Features == nil {
		runtimeInfo.Features = []string{}
	} else {
		runtimeInfo.Features = append([]string{}, runtimeInfo.Features...)
	}
	stepProvider, _ := cameras.(managedStepProvider)
	settingsProvider, _ := cameras.(backend.CameraSettingsProvider)
	diagnosticsProvider, _ := cameras.(backend.CameraDiagnosticsProvider)
	runtimeInfo.EnableDiagnostics = runtimeInfo.EnableDiagnostics && diagnosticsProvider != nil
	if runtimeInfo.EnableDiagnostics && !containsFeature(runtimeInfo.Features, "camera_session_diagnostics") {
		runtimeInfo.Features = append(runtimeInfo.Features, "camera_session_diagnostics")
	}
	h := &Handler{
		cfg:                 cfg,
		apiToken:            token,
		ptz:                 ptz,
		cameraProvider:      cameras,
		settingsProvider:    settingsProvider,
		managedStep:         stepProvider,
		managedPTZ:          managedPTZ,
		snapshotProvider:    snapshotProvider(cameras),
		diagnosticsProvider: diagnosticsProvider,
		runtimeInfo:         runtimeInfo,
		managedState:        state,
		locks:               map[string]*sync.Mutex{},
	}
	for _, camera := range cfg.Cameras {
		h.locks[camera.ID] = &sync.Mutex{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]any{"status": "configured", "cameras": len(cfg.Cameras)})
	})
	mux.HandleFunc("GET /v1/cameras", h.list)
	mux.HandleFunc("GET /v2/cameras", h.managedList)
	mux.HandleFunc("GET /v2/cameras/{id}/settings", h.managedSettings)
	mux.HandleFunc("GET /v2/cameras/{id}/snapshot", h.managedSnapshot)
	if runtimeInfo.EnableDiagnostics {
		mux.HandleFunc("GET /v2/cameras/{id}/diagnostics", h.managedCameraDiagnostics)
	}
	mux.HandleFunc("POST /v2/cameras/{id}/ptz/step", h.managedStepOnce)
	if state != nil {
		mux.HandleFunc("PUT /v2/selection", h.replaceSelection)
	}
	if runtimeInfo.InstanceID != "" && runtimeInfo.APIVersion != "" {
		mux.HandleFunc("GET /v2/info", h.managedInfo)
	}
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/step", h.step)
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/stop", h.stop)
	mux.HandleFunc("POST /v1/cameras/{id}/ptz/preset", h.preset)
	mux.HandleFunc("GET /v1/frigate/config", h.frigate)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
			auth := r.Header.Get("Authorization")
			actual := strings.TrimPrefix(auth, "Bearer ")
			if token == "" || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(actual), []byte(token)) != 1 {
				respond(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func snapshotProvider(cameras backend.CameraProvider) backend.SnapshotProvider {
	provider, _ := cameras.(backend.SnapshotProvider)
	return provider
}

func (h *Handler) managedSnapshot(w http.ResponseWriter, r *http.Request) {
	if h.snapshotProvider == nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	image, err := h.snapshotProvider.Snapshot(ctx, model.CameraID(r.PathValue("id")))
	if err != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorStreamTimeout)})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func (h *Handler) managedSettings(w http.ResponseWriter, r *http.Request) {
	if h.settingsProvider == nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	settings, err := h.settingsProvider.CameraSettings(ctx, model.CameraID(r.PathValue("id")))
	if err != nil {
		switch {
		case errors.Is(err, xiaomi.ErrCameraNotFound):
			respond(w, http.StatusNotFound, map[string]string{"error": "camera_not_found"})
		case errors.Is(err, xiaomi.ErrUnsupportedCamera):
			respond(w, http.StatusConflict, map[string]string{"error": string(model.ErrorUnsupportedModel)})
		case errors.Is(err, xiaomi.ErrInvalidCameraSettings):
			respond(w, http.StatusBadGateway, map[string]string{"error": "invalid_backend_response"})
		default:
			respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		}
		return
	}
	respond(w, http.StatusOK, settings)
}

func (h *Handler) managedCameraDiagnostics(w http.ResponseWriter, r *http.Request) {
	if h.diagnosticsProvider == nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	id := r.PathValue("id")
	if !validXiaomiCameraID(id) {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_camera_id"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	diagnostics, err := h.diagnosticsProvider.CameraDiagnostics(ctx, model.CameraID(id))
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			respond(w, http.StatusRequestTimeout, map[string]string{"error": string(model.ErrorTimeout)})
		case errors.Is(err, xiaomi.ErrCameraNotFound):
			respond(w, http.StatusNotFound, map[string]string{"error": "camera_not_found"})
		default:
			respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		}
		return
	}
	respond(w, http.StatusOK, sanitizeSessionDiagnostics(diagnostics))
}

func sanitizeSessionDiagnostics(value backend.CameraSessionDiagnostics) backend.CameraSessionDiagnostics {
	switch value.State {
	case backend.SessionIdle, backend.SessionStarting, backend.SessionStreaming,
		backend.SessionEnding, backend.SessionUnavailable, backend.SessionUnsupported:
	default:
		value.State = "unknown"
	}
	switch value.Protocol {
	case "", "cs2+tcp", "cs2+udp":
	default:
		value.Protocol = "unknown"
	}
	switch value.PeerClass {
	case "", "loopback_ip", "private_ip", "public_ip", "hostname_unverified", "unknown":
	default:
		value.PeerClass = "unknown"
	}
	switch value.Reason {
	case "", model.ErrorAuthRequired, model.ErrorWrongRegionOrEmptyAccount,
		model.ErrorUnsupportedModel, model.ErrorUnsupportedTransport,
		model.ErrorUnverifiedFirmware, model.ErrorChannelUnverified,
		model.ErrorTimeout, model.ErrorStale, model.ErrorLANUnreachable,
		model.ErrorStreamTimeout, model.ErrorCodecUnsupported,
		model.ErrorBackendUnavailable, model.ErrorStorage, model.ErrorAPIIncompatible:
	default:
		value.Reason = model.ErrorBackendUnavailable
	}
	return value
}

type managedStepRequest struct {
	Direction string `json:"direction"`
}

func (request *managedStepRequest) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("step request must be an object")
	}
	seenDirection := false
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return errors.New("invalid step request")
		}
		key, ok := token.(string)
		if !ok || key != "direction" || seenDirection {
			return errors.New("invalid step request field")
		}
		seenDirection = true
		if err := decoder.Decode(&request.Direction); err != nil {
			return errors.New("invalid step direction")
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || !seenDirection {
		return errors.New("invalid step request")
	}
	return nil
}

func (h *Handler) managedStepOnce(w http.ResponseWriter, r *http.Request) {
	if h.managedStep == nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	id := r.PathValue("id")
	if !validXiaomiCameraID(id) {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_camera_id"})
		return
	}
	var request managedStepRequest
	if !decodeWithLimit(w, r, &request, 1024) {
		return
	}
	if request.Direction != "left" && request.Direction != "right" && request.Direction != "up" && request.Direction != "down" {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_direction"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.managedStep.Step(ctx, model.CameraID(id), request.Direction); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			respond(w, http.StatusRequestTimeout, map[string]string{"error": "ptz_step_not_sent"})
			return
		}
		if errors.Is(err, xiaomi.ErrCameraNotFound) {
			respond(w, http.StatusNotFound, map[string]string{"error": "camera_not_found"})
			return
		}
		if errors.Is(err, xiaomi.ErrCameraUnavailable) {
			respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
			return
		}
		respond(w, http.StatusBadGateway, map[string]string{"error": "ptz_step_failed"})
		return
	}
	respond(w, http.StatusOK, map[string]string{"write_status": "sent"})
}

func validXiaomiCameraID(id string) bool {
	const prefix = "xiaomi_"
	if len(id) != len(prefix)+16 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, digit := range id[len(prefix):] {
		if !(digit >= '0' && digit <= '9') && !(digit >= 'a' && digit <= 'f') {
			return false
		}
	}
	return true
}

func (h *Handler) managedInfo(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, h.runtimeInfo)
}

type managedSelectionRequest struct {
	Revision  *uint64  `json:"revision"`
	CameraIDs []string `json:"camera_ids"`
}

type managedSelectionResponse struct {
	Revision  uint64   `json:"revision"`
	CameraIDs []string `json:"camera_ids"`
}

func (h *Handler) replaceSelection(w http.ResponseWriter, r *http.Request) {
	var request managedSelectionRequest
	if !decodeWithLimit(w, r, &request, 64*1024) {
		return
	}
	if request.Revision == nil || request.CameraIDs == nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_selection"})
		return
	}
	state, err := h.managedState.ReplaceSelection(*request.Revision, request.CameraIDs)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRevisionConflict):
			respond(w, http.StatusConflict, managedSelectionResponse{
				Revision:  state.SelectionRevision,
				CameraIDs: state.SelectedCameraIDs,
			})
		case errors.Is(err, store.ErrDuplicateCameraID):
			respond(w, http.StatusBadRequest, map[string]string{"error": "duplicate_camera_id"})
		case errors.Is(err, store.ErrInvalidCameraID):
			respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_camera_id"})
		case errors.Is(err, store.ErrStateTooLarge):
			respond(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "selection_too_large"})
		default:
			respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorStorage)})
		}
		return
	}
	respond(w, http.StatusOK, managedSelectionResponse{
		Revision:  state.SelectionRevision,
		CameraIDs: state.SelectedCameraIDs,
	})
}

func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	cams := []PublicCamera{}
	for _, c := range h.cfg.Cameras {
		cams = append(cams, PublicCamera{ID: c.ID, Name: c.Name, Model: c.Model, RTSPURL: c.RTSPURL, PTZ: c.PTZ, Mode: "onvif-bridge"})
	}
	respond(w, 200, map[string]any{"version": "0.1.0", "cameras": cams})
}

type managedCameraList struct {
	Cameras []managedCamera `json:"cameras"`
}

type managedCamera struct {
	model.Camera
	Streams []backend.StreamEndpoint `json:"streams,omitempty"`
}

func cloneManagedCameras(source []model.Camera) []model.Camera {
	cameras := make([]model.Camera, len(source))
	for i, camera := range source {
		cameras[i] = camera
		cameras[i].Video.LastFrameAt = cloneTimestamp(camera.Video.LastFrameAt)
		cameras[i].Video.UpdatedAt = cloneTimestamp(camera.Video.UpdatedAt)
		if camera.Support != nil {
			support := *camera.Support
			support.Features = make(map[string]model.FeatureSupport, len(camera.Support.Features))
			for name, feature := range camera.Support.Features {
				feature.RequiresCloud = append([]string{}, feature.RequiresCloud...)
				support.Features[name] = feature
			}
			cameras[i].Support = &support
		}
	}
	return cameras
}

func removeFeatureEvidence(camera *model.Camera, names ...string) {
	if camera.Support == nil {
		return
	}
	for _, name := range names {
		delete(camera.Support.Features, name)
	}
	if len(camera.Support.Features) == 0 {
		camera.Support = nil
	}
}

func cloneTimestamp(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	timestamp := *source
	return &timestamp
}

func (h *Handler) managedList(w http.ResponseWriter, r *http.Request) {
	if h.cameraProvider == nil {
		if h.managedState != nil && len(h.managedState.Snapshot().SelectedCameraIDs) == 0 {
			respond(w, http.StatusOK, managedCameraList{Cameras: []managedCamera{}})
			return
		}
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	cameras, err := h.cameraProvider.Cameras(r.Context())
	if err != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
		return
	}
	// The provider owns its snapshot. Copy both the slice and pointer fields
	// before normalizing capabilities so concurrent requests cannot mutate
	// shared provider state.
	cameras = cloneManagedCameras(cameras)
	response := managedCameraList{Cameras: make([]managedCamera, 0, len(cameras))}
	streamProvider, hasStreams := h.cameraProvider.(backend.StreamProvider)
	for i := range cameras {
		if h.managedPTZ == nil {
			cameras[i].Capabilities.PTZ = false
			cameras[i].Capabilities.Presets = false
			cameras[i].Control = model.ControlStatus{State: model.ControlUnsupported}
			removeFeatureEvidence(&cameras[i], "ptz", "presets")
		}
		if h.settingsProvider == nil {
			removeFeatureEvidence(&cameras[i], "camera_settings_read")
		}
		// Support metadata is optional. Keep the camera and its stable core
		// capabilities if a provider sends an unknown or malformed support schema.
		if err := cameras[i].ValidateSupport(); err != nil {
			cameras[i].Support = nil
		}
		if err := cameras[i].ValidateCore(); err != nil {
			respond(w, http.StatusBadGateway, map[string]string{"error": "invalid_backend_response"})
			return
		}
		entry := managedCamera{Camera: cameras[i]}
		if hasStreams && cameras[i].Capabilities.Video {
			streams, err := streamProvider.Streams(r.Context(), cameras[i].ID)
			if err != nil {
				respond(w, http.StatusServiceUnavailable, map[string]string{"error": string(model.ErrorBackendUnavailable)})
				return
			}
			seenStreams := make(map[string]struct{}, len(streams))
			for _, stream := range streams {
				if stream.Validate() != nil {
					respond(w, http.StatusBadGateway, map[string]string{"error": "invalid_backend_response"})
					return
				}
				if _, exists := seenStreams[stream.ID]; exists {
					respond(w, http.StatusBadGateway, map[string]string{"error": "invalid_backend_response"})
					return
				}
				seenStreams[stream.ID] = struct{}{}
			}
			entry.Streams = streams
		}
		response.Cameras = append(response.Cameras, entry)
	}
	respond(w, http.StatusOK, response)
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) (config.Camera, bool) {
	for _, c := range h.cfg.Cameras {
		if c.ID == r.PathValue("id") {
			if !c.PTZ {
				respond(w, 409, map[string]string{"error": "ptz_not_supported"})
				return c, false
			}
			return c, true
		}
	}
	respond(w, 404, map[string]string{"error": "camera_not_found"})
	return config.Camera{}, false
}

func decode(w http.ResponseWriter, r *http.Request, to any) bool {
	return decodeJSON(w, r, to, 4096, false)
}

func decodeWithLimit(w http.ResponseWriter, r *http.Request, to any, maxBytes int64) bool {
	return decodeJSON(w, r, to, maxBytes, true)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, to any, maxBytes int64, requireEOF bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(to); err != nil {
		respond(w, 400, map[string]string{"error": "invalid_json"})
		return false
	}
	if requireEOF {
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			respond(w, 400, map[string]string{"error": "invalid_json"})
			return false
		}
	}
	return true
}

func (h *Handler) step(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	var req struct {
		Direction string `json:"direction"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Direction != "up" && req.Direction != "down" && req.Direction != "left" && req.Direction != "right" {
		respond(w, 400, map[string]string{"error": "invalid_direction"})
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.ptz.Step(ctx, cam.ONVIFURL, cam.Profile, req.Direction, cam.Step); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.ptz.Stop(ctx, cam.ONVIFURL, cam.Profile); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) preset(w http.ResponseWriter, r *http.Request) {
	cam, ok := h.lookup(w, r)
	if !ok {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Token == "" || len(req.Token) > 80 {
		respond(w, 400, map[string]string{"error": "invalid_preset"})
		return
	}
	h.locks[cam.ID].Lock()
	defer h.locks[cam.ID].Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := h.ptz.Preset(ctx, cam.ONVIFURL, cam.Profile, req.Token); err != nil {
		respond(w, 502, map[string]string{"error": "ptz_backend_failed"})
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func quoteYAML(s string) string { b, _ := json.Marshal(s); return string(b) }

func (h *Handler) frigate(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("# Merge into existing Frigate config, do not replace it.\n")
	b.WriteString("# RTSP source: Xiaomi bridge; Frigate restream: go2rtc\n")
	b.WriteString("go2rtc:\n  streams:\n")
	for _, cam := range h.cfg.Cameras {
		fmt.Fprintf(&b, "    %s:\n      - %s\n", cam.ID, quoteYAML(cam.RTSPURL))
	}
	b.WriteString("cameras:\n")
	for _, cam := range h.cfg.Cameras {
		fmt.Fprintf(&b, "  %s:\n    ffmpeg:\n      inputs:\n        - path: %s\n          input_args: preset-rtsp-restream\n          roles: [detect, record]\n", cam.ID, quoteYAML("rtsp://127.0.0.1:8554/"+cam.ID))
		if cam.PTZ {
			u, _ := url.Parse(cam.ONVIFURL)
			host := u.Hostname()
			port := u.Port()
			if port == "" {
				port = "80"
			}
			fmt.Fprintf(&b, "    onvif:\n      host: %s\n      port: %s\n      user: \"\"\n      password: \"\"\n", quoteYAML(host), port)
		}
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(b.String()))
}
