// Package xiaomi connects the supported Xiaomi camera transport to Gateway's
// managed camera interfaces.
package xiaomi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/streamrtsp"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/profiles"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/protocol/cloud"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/protocol/stream"
)

const supportedModel = profiles.ModelC201

var cameraProfiles = profiles.DefaultRegistry()

const (
	defaultRTSPListen = ":8554"
	defaultRTSPPort   = 8554
	defaultFFmpeg     = "ffmpeg"
	frameReadyTimeout = 20 * time.Second
	snapshotTimeout   = 12 * time.Second
	maxSnapshotBytes  = 8 << 20
)

var (
	ErrCredentialsIncomplete = errors.New("Xiaomi account settings are incomplete")
	ErrCredentialsInvalid    = errors.New("Xiaomi account authorization failed")
	ErrCameraNotFound        = errors.New("Xiaomi camera was not found in the loaded inventory")
	ErrCameraUnavailable     = errors.New("Xiaomi camera is unavailable")
	ErrInvalidCameraSettings = errors.New("Xiaomi camera returned an invalid settings response")
	ErrUnsupportedCamera     = errors.New("Xiaomi camera model is not supported")
	ErrUnsupportedDirection  = errors.New("Xiaomi PTZ direction is not supported")
)

type Config struct {
	UserID                   string
	Region                   string
	Token                    string
	AdvertiseHost            string
	RTSPListen               string
	RTSPPort                 int
	FFmpegPath               string
	TestExperimentalProfiles bool
}

// ConfigFromEnv reads the private pilot/runtime settings. Empty settings mean
// that the optional Xiaomi provider is not configured.
func ConfigFromEnv() (Config, bool, error) {
	cfg := Config{
		UserID:        strings.TrimSpace(os.Getenv("XLG_XIAOMI_USER_ID")),
		Region:        strings.TrimSpace(os.Getenv("XLG_XIAOMI_REGION")),
		Token:         os.Getenv("XLG_XIAOMI_TOKEN"),
		AdvertiseHost: strings.TrimSpace(os.Getenv("XLG_RTSP_ADVERTISE_HOST")),
		RTSPListen:    strings.TrimSpace(os.Getenv("XLG_RTSP_LISTEN")),
		FFmpegPath:    strings.TrimSpace(os.Getenv("XLG_FFMPEG_PATH")),
	}
	configured := cfg.UserID != "" || cfg.Region != "" || cfg.Token != ""
	if !configured {
		return Config{}, false, nil
	}
	if cfg.UserID == "" || cfg.Region == "" || cfg.Token == "" || cfg.AdvertiseHost == "" {
		return Config{}, true, ErrCredentialsIncomplete
	}
	if raw := strings.TrimSpace(os.Getenv("XLG_XIAOMI_PROFILE_TEST_MODE")); raw != "" {
		profileTestMode, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, true, errors.New("invalid Xiaomi profile test mode")
		}
		cfg.TestExperimentalProfiles = profileTestMode
	}
	if cfg.RTSPListen == "" {
		cfg.RTSPListen = defaultRTSPListen
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = defaultFFmpeg
	}
	_, portText, err := net.SplitHostPort(cfg.RTSPListen)
	if err != nil {
		return Config{}, true, errors.New("invalid RTSP listener address")
	}
	if portText == "" {
		return Config{}, true, errors.New("RTSP listener port is required")
	}
	cfg.RTSPPort, err = strconv.Atoi(portText)
	if err != nil || cfg.RTSPPort < 1 || cfg.RTSPPort > 65535 {
		return Config{}, true, errors.New("invalid RTSP listener port")
	}
	if strings.ContainsAny(cfg.AdvertiseHost, "/?#@") {
		return Config{}, true, errors.New("invalid RTSP advertised host")
	}
	return cfg, true, nil
}

type Provider struct {
	cfg             Config
	account         *cloud.Account
	relay           *streamrtsp.Relay
	profileRegistry *profiles.Registry

	mu      sync.RWMutex
	sources map[model.CameraID]*cameraSource
	devices []*cloud.Device
	loaded  time.Time
	closed  bool

	closeOnce sync.Once
}

type cameraSession interface {
	Read() (*stream.Frame, error)
	Close()
	Protocol() string
	DiagnosticMotorStepObserved(string, func()) (bool, string)
}

type liveCameraSession struct {
	session *stream.Session
}

type cameraSessionDiagnostics interface {
	CameraSessionDiagnostics(context.Context) (backend.CameraSessionDiagnostics, error)
}

func (s *liveCameraSession) Read() (*stream.Frame, error) { return s.session.Read() }
func (s *liveCameraSession) Close()                       { s.session.Close() }
func (s *liveCameraSession) Protocol() string             { return s.session.Protocol }
func (s *liveCameraSession) CameraSessionDiagnostics(ctx context.Context) (backend.CameraSessionDiagnostics, error) {
	if err := ctx.Err(); err != nil {
		return backend.CameraSessionDiagnostics{}, err
	}
	stats := s.session.Stats()
	return backend.CameraSessionDiagnostics{
		Protocol:       safeSessionProtocol(s.session.Protocol),
		PeerClass:      sessionPeerClass(s.session.RemoteAddr),
		Frames:         stats.Frames,
		Bytes:          stats.Bytes,
		Dropped:        stats.Dropped,
		Replies:        stats.Replies,
		AudioRequested: stats.AudioAsked,
	}, ctx.Err()
}
func (s *liveCameraSession) DiagnosticMotorStepObserved(direction string, onWrite func()) (bool, string) {
	result := s.session.DiagnosticMotorStepObserved(direction, onWrite)
	return result.WriteSucceeded(), result.SanitizedLine()
}

// CameraDiagnostics performs an allowlisted read against the already-owned
// session. It never refreshes inventory, negotiates MISS, opens a socket, or
// reads a frame. The session lifecycle lock pins the exact run during metadata
// reads and every lock wait honors the request deadline.
func (p *Provider) CameraDiagnostics(ctx context.Context, id model.CameraID, request backend.CameraDiagnosticRequest) (backend.CameraSessionDiagnostics, error) {
	if ctx == nil {
		return backend.CameraSessionDiagnostics{}, context.Canceled
	}
	if p == nil {
		return backend.CameraSessionDiagnostics{}, ErrCameraUnavailable
	}
	if request.Operation != backend.DiagnosticSessionStatus && request.Operation != backend.DiagnosticTransportMetadata {
		return backend.CameraSessionDiagnostics{}, backend.ErrInvalidCameraDiagnosticOperation
	}
	if request.CorrelationID == "" || len(request.CorrelationID) > 64 {
		return backend.CameraSessionDiagnostics{}, backend.ErrInvalidCameraDiagnosticOperation
	}
	if request.Operation == backend.DiagnosticTransportMetadata && !validSessionGeneration(request.ExpectedGeneration) {
		return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
	}
	var source *cameraSource
	var knownDevice *cloud.Device
	providerClosed := false
	for {
		if err := ctx.Err(); err != nil {
			return backend.CameraSessionDiagnostics{}, err
		}
		if p.mu.TryRLock() {
			providerClosed = p.closed
			source = p.sources[id]
			for _, device := range p.devices {
				if device != nil && device.IsCamera() && cameraID(device.Did) == id {
					knownDevice = device
					break
				}
			}
			p.mu.RUnlock()
			break
		}
		if err := waitDiagnosticLock(ctx); err != nil {
			return backend.CameraSessionDiagnostics{}, err
		}
	}
	if providerClosed {
		return backend.CameraSessionDiagnostics{Operation: request.Operation, CorrelationID: request.CorrelationID, State: backend.SessionUnavailable, Reason: model.ErrorBackendUnavailable}, nil
	}
	if source == nil {
		if knownDevice == nil {
			return backend.CameraSessionDiagnostics{}, ErrCameraNotFound
		}
		if request.ExpectedGeneration != "" {
			return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
		}
		if request.Operation == backend.DiagnosticTransportMetadata {
			return backend.CameraSessionDiagnostics{}, backend.ErrNoActiveCameraSession
		}
		match, supported := p.profileForDevice(knownDevice)
		if !supported || !match.Supports(profiles.FeatureSessionDiagnostics) {
			return backend.CameraSessionDiagnostics{Operation: request.Operation, CorrelationID: request.CorrelationID, State: backend.SessionUnsupported, Reason: model.ErrorUnsupportedModel}, nil
		}
		return backend.CameraSessionDiagnostics{Operation: request.Operation, CorrelationID: request.CorrelationID, State: backend.SessionIdle}, nil
	}
	return p.cameraDiagnosticsForSource(ctx, id, source, request)
}

// cameraDiagnosticsForSource reads a source that was captured from the
// provider inventory. It revalidates that identity while holding the source
// lifecycle lock, and all provider-lock acquisition remains cancellable.
func (p *Provider) cameraDiagnosticsForSource(ctx context.Context, id model.CameraID, source *cameraSource, request backend.CameraDiagnosticRequest) (backend.CameraSessionDiagnostics, error) {
	if err := lockDiagnosticContext(ctx, &source.lifecycleMu); err != nil {
		source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), nil)
		return backend.CameraSessionDiagnostics{}, err
	}
	defer source.lifecycleMu.Unlock()

	// Validate that inventory still points at this exact source and pin its run.
	var run *cameraSessionRun
	var path cameraPath
	var starting, closed, failed bool
	for {
		if err := ctx.Err(); err != nil {
			source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), nil)
			return backend.CameraSessionDiagnostics{}, err
		}
		if !p.mu.TryRLock() {
			if err := waitDiagnosticLock(ctx); err != nil {
				source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), nil)
				return backend.CameraSessionDiagnostics{}, err
			}
			continue
		}
		if p.closed || p.sources[id] != source {
			p.mu.RUnlock()
			source.recordDiagnosticEvent(request, "diagnostics_read", "stale", nil)
			return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
		}
		if source.mu.TryLock() {
			run, path = source.session, source.path
			starting, closed, failed = source.starting != nil, source.closed, source.lastErr != nil
			source.mu.Unlock()
			p.mu.RUnlock()
			break
		}
		p.mu.RUnlock()
		if err := waitDiagnosticLock(ctx); err != nil {
			source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), nil)
			return backend.CameraSessionDiagnostics{}, err
		}
	}

	state := backend.SessionIdle
	switch {
	case closed:
		state = backend.SessionUnavailable
	case run == nil && starting:
		state = backend.SessionStarting
	case run == nil && failed:
		state = backend.SessionUnavailable
	case run != nil && run.isEnding():
		state = backend.SessionEnding
	case run != nil && path != nil && path.Ready():
		state = backend.SessionStreaming
	case run != nil:
		state = backend.SessionStarting
	}
	diagnostics := backend.CameraSessionDiagnostics{
		Operation: request.Operation, CorrelationID: request.CorrelationID, State: state,
	}
	if run == nil || run.isEnding() || run.session == nil {
		if request.ExpectedGeneration != "" {
			source.recordDiagnosticEvent(request, "diagnostics_read", "stale", &diagnostics)
			return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
		}
		if request.Operation == backend.DiagnosticTransportMetadata {
			source.recordDiagnosticEvent(request, "diagnostics_read", "unavailable", &diagnostics)
			return backend.CameraSessionDiagnostics{}, backend.ErrNoActiveCameraSession
		}
		if state == backend.SessionUnavailable {
			diagnostics.Reason = model.ErrorBackendUnavailable
		}
		source.recordDiagnosticEvent(request, "diagnostics_read", "ok", &diagnostics)
		diagnostics.Events = source.diagnosticEventSnapshot()
		return diagnostics, nil
	}
	diagnostics.Generation = run.generation
	if request.ExpectedGeneration != "" && request.ExpectedGeneration != run.generation {
		source.recordDiagnosticEvent(request, "diagnostics_read", "stale", &diagnostics)
		return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
	}
	if request.Operation == backend.DiagnosticTransportMetadata {
		if request.ExpectedGeneration != run.generation {
			source.recordDiagnosticEvent(request, "diagnostics_read", "stale", &diagnostics)
			return backend.CameraSessionDiagnostics{}, backend.ErrStaleCameraSessionGeneration
		}
		if reader, ok := run.session.(cameraSessionDiagnostics); ok {
			metadata, err := reader.CameraSessionDiagnostics(ctx)
			if err != nil {
				source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), &diagnostics)
				return backend.CameraSessionDiagnostics{}, err
			}
			diagnostics.Protocol = safeSessionProtocol(metadata.Protocol)
			diagnostics.PeerClass = safeSessionPeerClass(metadata.PeerClass)
			diagnostics.Frames, diagnostics.Bytes = metadata.Frames, metadata.Bytes
			diagnostics.Dropped, diagnostics.Replies = metadata.Dropped, metadata.Replies
			diagnostics.AudioRequested = metadata.AudioRequested
		} else {
			diagnostics.Protocol = safeSessionProtocol(run.session.Protocol())
		}
	}
	if err := ctx.Err(); err != nil {
		source.recordDiagnosticEvent(request, "diagnostics_read", diagnosticResult(err), &diagnostics)
		return backend.CameraSessionDiagnostics{}, err
	}
	source.recordDiagnosticEvent(request, "diagnostics_read", "ok", &diagnostics)
	diagnostics.Events = source.diagnosticEventSnapshot()
	return diagnostics, nil
}

func lockDiagnosticContext(ctx context.Context, mutex *sync.Mutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mutex.TryLock() {
			return nil
		}
		if err := waitDiagnosticLock(ctx); err != nil {
			return err
		}
	}
}

func validSessionGeneration(value string) bool {
	if len(value) != len("gen_")+32 || !strings.HasPrefix(value, "gen_") {
		return false
	}
	for _, char := range value[len("gen_"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func diagnosticResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, backend.ErrStaleCameraSessionGeneration):
		return "stale"
	case errors.Is(err, backend.ErrNoActiveCameraSession):
		return "unavailable"
	default:
		return "error"
	}
}

func (s *cameraSource) recordDiagnosticEvent(request backend.CameraDiagnosticRequest, name, result string, state *backend.CameraSessionDiagnostics) {
	if s == nil {
		return
	}
	event := backend.CameraDiagnosticEvent{
		ObservedAt: time.Now().UTC(), Name: name, Result: result,
		Operation: request.Operation, CorrelationID: request.CorrelationID,
	}
	if state != nil {
		event.Generation, event.State = state.Generation, state.State
		event.Protocol, event.PeerClass = state.Protocol, state.PeerClass
	}
	s.diagnosticEventsMu.Lock()
	defer s.diagnosticEventsMu.Unlock()
	s.diagnosticSequence++
	event.Sequence = s.diagnosticSequence
	s.diagnosticEvents = append(s.diagnosticEvents, event)
	if len(s.diagnosticEvents) > backend.MaxCameraDiagnosticEvents {
		copy(s.diagnosticEvents, s.diagnosticEvents[len(s.diagnosticEvents)-backend.MaxCameraDiagnosticEvents:])
		s.diagnosticEvents = s.diagnosticEvents[:backend.MaxCameraDiagnosticEvents]
	}
}

func (s *cameraSource) diagnosticEventSnapshot() []backend.CameraDiagnosticEvent {
	s.diagnosticEventsMu.Lock()
	defer s.diagnosticEventsMu.Unlock()
	return append([]backend.CameraDiagnosticEvent(nil), s.diagnosticEvents...)
}

func waitDiagnosticLock(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeSessionProtocol(value string) string {
	if value == "cs2+tcp" || value == "cs2+udp" {
		return value
	}
	return "unknown"
}

func safeSessionPeerClass(value string) string {
	switch value {
	case "loopback_ip", "private_ip", "public_ip", "hostname_unverified", "unknown":
		return value
	default:
		return "unknown"
	}
}

func sessionPeerClass(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return "hostname_unverified"
	}
	if ip.IsLoopback() {
		return "loopback_ip"
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return "private_ip"
	}
	return "public_ip"
}

type cameraPath interface {
	URL(string, int) (string, error)
	Ready() bool
	ReadySignal() <-chan struct{}
	WriteAccessUnit([]byte, int64, bool) error
	Close()
}

type cameraSessionRun struct {
	session    cameraSession
	path       cameraPath
	generation string
	ending     chan struct{} // closed under cameraSource.mu when this run begins termination
}

func (run *cameraSessionRun) isEnding() bool {
	select {
	case <-run.ending:
		return true
	default:
		return false
	}
}

type cameraSource struct {
	provider *Provider
	device   *cloud.Device
	id       model.CameraID

	mu sync.Mutex // protects this source's current path, session, and lifecycle flags
	// lifecycleMu serializes accepted PTZ steps with retirement. When both locks
	// are needed, acquire lifecycleMu before mu; termination marking releases mu
	// before it waits for lifecycleMu.
	lifecycleMu        sync.Mutex
	diagnosticEventsMu sync.Mutex
	diagnosticEvents   []backend.CameraDiagnosticEvent
	diagnosticSequence uint64
	session            *cameraSessionRun
	path               cameraPath
	sessionDone        chan struct{}
	starting           chan struct{}
	startErr           error
	lastErr            error
	closed             bool
}

// New restores a Xiaomi account from its pass token and starts the local RTSP
// listener. Credentials are never included in returned errors or log messages.
func New(cfg Config) (*Provider, error) {
	if cfg.UserID == "" || cfg.Region == "" || cfg.Token == "" || cfg.AdvertiseHost == "" {
		return nil, ErrCredentialsIncomplete
	}
	if cfg.RTSPListen == "" {
		cfg.RTSPListen = defaultRTSPListen
	}
	if cfg.RTSPPort == 0 {
		cfg.RTSPPort = defaultRTSPPort
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = defaultFFmpeg
	}
	relay, err := streamrtsp.New(cfg.RTSPListen)
	if err != nil {
		return nil, err
	}
	registry := cloud.NewRegistry()
	account, err := registry.Restore(cfg.UserID, cfg.Region, cfg.Token)
	if err != nil {
		log.Printf("xiaomi_backend event=account_restore result=error reason=%s", restoreFailureClass(err))
		relay.Close()
		return nil, ErrCredentialsInvalid
	}
	log.Printf("xiaomi_backend event=account_restore result=ok")
	return &Provider{cfg: cfg, account: account, relay: relay, sources: make(map[model.CameraID]*cameraSource)}, nil
}

func restoreFailureClass(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "network"
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return "tls"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "http_request"
	}
	if strings.Contains(err.Error(), "saved token is no longer valid") {
		return "token_rejected"
	}
	if strings.Contains(err.Error(), "login did not yield a session") {
		return "session_missing"
	}
	return "protocol_error"
}

// Cameras returns Xiaomi inventory with profile-derived capabilities. Profiles
// marked experimental are available only when the explicit test mode is on.
func (p *Provider) Cameras(ctx context.Context) ([]model.Camera, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	devices, err := p.cameraDevices()
	if err != nil {
		return nil, ErrCameraUnavailable
	}
	cameras := make([]model.Camera, 0, len(devices))
	sources := make(map[model.CameraID]*cameraSource, len(devices))
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrCameraUnavailable
	}
	for _, device := range devices {
		if device == nil || !device.IsCamera() {
			continue
		}
		match, supported := p.profileForDevice(device)
		id := cameraID(device.Did)
		camera := model.Camera{
			ID:       id,
			Name:     cameraName(device),
			Model:    device.Model,
			ModelID:  device.Model,
			Selected: supported && match.Profile.AutoSelect && match.Supports(profiles.FeatureVideo),
			Video:    model.VideoStatus{State: model.VideoUnsupported, Reason: model.ErrorUnsupportedModel},
			Control:  model.ControlStatus{State: model.ControlUnsupported},
		}
		if supported && match.Supports(profiles.FeatureVideo) {
			camera.Capabilities.Video = match.Supports(profiles.FeatureVideo)
			camera.Capabilities.Snapshot = match.Supports(profiles.FeatureSnapshot)
			camera.Support = profileSupportEvidence(match)
			source := p.sources[id]
			if source == nil || source.device.Did != device.Did {
				source = &cameraSource{provider: p, device: device, id: id}
			}
			camera.Video = source.videoStatus()
			sources[id] = source
		} else {
			camera.Support = unsupportedModelSupportEvidence()
		}
		cameras = append(cameras, camera)
	}
	p.sources = sources
	p.devices = devices
	p.loaded = time.Now()
	p.mu.Unlock()
	return cameras, nil
}

func c201SupportEvidence() *model.CameraSupport {
	return &model.CameraSupport{
		SchemaVersion: 1,
		Features: map[string]model.FeatureSupport{
			"video": {
				Implementation: "implemented",
				Validation:     "device",
				Path:           "camera_lan",
				Availability:   "unknown",
				Reason:         model.ErrorUnverifiedFirmware,
				RequiresCloud:  []string{"account_auth", "inventory", "new_session_negotiation"},
				EvidenceRef:    "TEST_REPORT.md#latest-c201-l2-decode-7-nov-2026",
				TestedFirmware: "5.3.2_0016",
			},
			"snapshot": {
				Implementation: "implemented",
				Validation:     "offline",
				Path:           "camera_lan",
				Availability:   "unknown",
				Reason:         model.ErrorUnverifiedFirmware,
				RequiresCloud:  []string{"account_auth", "inventory", "new_session_negotiation"},
				EvidenceRef:    ".test-env/evidence/test-local-20261112-162635-f49fca83.log#ha-camera-proxy",
			},
			"ptz": {
				Implementation: "implemented",
				Validation:     "device",
				Path:           "camera_lan",
				Availability:   "unknown",
				Reason:         model.ErrorUnverifiedFirmware,
				RequiresCloud:  []string{"account_auth", "inventory", "new_session_negotiation"},
				EvidenceRef:    "TEST_REPORT.md#four-physical-directions-12-nov-2026",
				TestedFirmware: "5.3.2_0016",
			},
			"camera_settings_read": {
				Implementation: "implemented",
				Validation:     "offline",
				Path:           "cloud",
				Availability:   "unknown",
				Reason:         model.ErrorUnverifiedFirmware,
				RequiresCloud:  []string{"account_auth", "inventory", "settings_read"},
				EvidenceRef:    ".test-env/evidence/test-local-20261112-162635-f49fca83.log#late-settings",
			},
		},
	}
}

func profileSupportEvidence(match profiles.Match) *model.CameraSupport {
	if match.Profile.ID == profiles.C201ID {
		return c201SupportEvidence()
	}
	if !match.Profile.Experimental {
		return unsupportedModelSupportEvidence()
	}
	features := make(map[string]model.FeatureSupport)
	for _, feature := range []profiles.Feature{
		profiles.FeatureVideo, profiles.FeatureSnapshot, profiles.FeaturePTZ,
		profiles.FeatureSettingsRead, profiles.FeatureSessionDiagnostics,
	} {
		evidence := model.FeatureSupport{
			Implementation: "research", Validation: "none", Path: "unknown",
			Availability: "unavailable", Reason: model.ErrorUnsupportedModel,
			RequiresCloud: []string{},
		}
		if match.Supports(feature) {
			evidence.Availability = "unknown"
			evidence.Reason = model.ErrorUnverifiedFirmware
		}
		features[string(feature)] = evidence
	}
	return &model.CameraSupport{SchemaVersion: 1, Features: features}
}

func unsupportedModelSupportEvidence() *model.CameraSupport {
	features := make(map[string]model.FeatureSupport)
	for _, name := range []string{"video", "snapshot", "audio", "ptz", "presets", "camera_settings_read"} {
		features[name] = model.FeatureSupport{
			Implementation: "research",
			Validation:     "none",
			Path:           "unknown",
			Availability:   "unavailable",
			Reason:         model.ErrorUnsupportedModel,
			RequiresCloud:  []string{},
		}
	}
	return &model.CameraSupport{SchemaVersion: 1, Features: features}
}

// profileForDevice resolves only from model ID and the configured account
// region. The current Xiaomi device-list response does not include firmware,
// so firmware-gated features stay disabled and the profile exposes only its
// explicitly listed baseline behavior.
func (p *Provider) profileForDevice(device *cloud.Device) (profiles.Match, bool) {
	if device == nil {
		return profiles.Match{}, false
	}
	region := ""
	registry := cameraProfiles
	if p != nil {
		region = p.cfg.Region
		if p.profileRegistry != nil {
			registry = p.profileRegistry
		}
	}
	match, err := registry.Resolve(device.Model, "", region)
	if err != nil || match.Profile.Experimental && (p == nil || !p.cfg.TestExperimentalProfiles) {
		return profiles.Match{}, false
	}
	return match, true
}

func (p *Provider) cameraDevices() ([]*cloud.Device, error) {
	p.mu.RLock()
	if time.Since(p.loaded) < 30*time.Second {
		devices := append([]*cloud.Device(nil), p.devices...)
		p.mu.RUnlock()
		return devices, nil
	}
	p.mu.RUnlock()
	devices, err := p.account.Devices()
	if err != nil {
		log.Printf("xiaomi_backend event=inventory result=error")
		return nil, err
	}
	log.Printf("xiaomi_backend event=inventory result=ok camera_count=%d", len(devices))
	return devices, nil
}

var c201ReadOnlyProperties = []struct {
	name string
	siid int
	piid int
	kind string
}{
	{name: "night_shot", siid: 2, piid: 3, kind: "uint8"},
	{name: "indicator_light", siid: 4, piid: 1, kind: "bool"},
	{name: "motion_detection", siid: 5, piid: 1, kind: "bool"},
	{name: "human_detection", siid: 8, piid: 6, kind: "bool"},
	{name: "move_detection", siid: 8, piid: 7, kind: "bool"},
	{name: "pet_detection", siid: 8, piid: 3, kind: "bool"},
}

type miotPropertyResult struct {
	Siid  int             `json:"siid"`
	Piid  int             `json:"piid"`
	Code  *int            `json:"code"`
	Value json.RawMessage `json:"value"`
}

// CameraSettings reads only the fixed C201 property allowlist. It uses the
// already-loaded account and inventory, never writes MIoT properties, and
// leaves returned enum values raw for the caller to interpret by the model
// specification.
func (p *Provider) CameraSettings(ctx context.Context, id model.CameraID) (backend.CameraSettings, error) {
	if err := ctx.Err(); err != nil {
		return backend.CameraSettings{}, err
	}
	p.mu.RLock()
	devices := append([]*cloud.Device(nil), p.devices...)
	loaded := !p.loaded.IsZero()
	closed := p.closed
	p.mu.RUnlock()
	if closed || !loaded {
		return backend.CameraSettings{}, ErrCameraUnavailable
	}
	var device *cloud.Device
	for _, candidate := range devices {
		if candidate != nil && candidate.IsCamera() && cameraID(candidate.Did) == id {
			device = candidate
			break
		}
	}
	if device == nil {
		return backend.CameraSettings{}, ErrCameraNotFound
	}
	match, supported := p.profileForDevice(device)
	if !supported || !match.Supports(profiles.FeatureSettingsRead) {
		return backend.CameraSettings{}, ErrUnsupportedCamera
	}
	props := make([]cloud.MiotProp, 0, len(c201ReadOnlyProperties))
	for _, property := range c201ReadOnlyProperties {
		props = append(props, cloud.MiotProp{Siid: property.siid, Piid: property.piid})
	}
	raw, err := p.account.MiotGetContext(ctx, device.Did, props)
	if err != nil {
		return backend.CameraSettings{}, ErrCameraUnavailable
	}
	var response struct {
		Result []miotPropertyResult `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Result == nil {
		return backend.CameraSettings{}, ErrInvalidCameraSettings
	}
	propertySpecs := make(map[[2]int]struct {
		name string
		kind string
	}, len(c201ReadOnlyProperties))
	for _, property := range c201ReadOnlyProperties {
		propertySpecs[[2]int{property.siid, property.piid}] = struct {
			name string
			kind string
		}{name: property.name, kind: property.kind}
	}
	values := make(map[string]backend.SettingValue, len(c201ReadOnlyProperties))
	for _, result := range response.Result {
		property, requested := propertySpecs[[2]int{result.Siid, result.Piid}]
		if !requested {
			continue
		}
		if result.Code == nil {
			return backend.CameraSettings{}, ErrInvalidCameraSettings
		}
		if _, duplicate := values[property.name]; duplicate {
			return backend.CameraSettings{}, ErrInvalidCameraSettings
		}
		value := append(json.RawMessage(nil), result.Value...)
		if *result.Code != 0 {
			value = nil
		} else if len(value) > 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			switch property.kind {
			case "bool":
				var boolValue bool
				if err := json.Unmarshal(value, &boolValue); err != nil {
					return backend.CameraSettings{}, ErrInvalidCameraSettings
				}
			case "uint8":
				var numericValue int
				if err := json.Unmarshal(value, &numericValue); err != nil || numericValue < 0 || numericValue > 255 {
					return backend.CameraSettings{}, ErrInvalidCameraSettings
				}
			default:
				return backend.CameraSettings{}, ErrInvalidCameraSettings
			}
		}
		values[property.name] = backend.SettingValue{Code: *result.Code, Value: value}
	}
	if _, present := values["night_shot"]; !present {
		return backend.CameraSettings{}, ErrInvalidCameraSettings
	}
	return backend.CameraSettings{
		ObservedAt: time.Now().UTC(),
		Properties: values,
	}, nil
}

func (p *Provider) Streams(ctx context.Context, id model.CameraID) ([]backend.StreamEndpoint, error) {
	source := p.source(id)
	if source == nil {
		return nil, ErrUnsupportedCamera
	}
	match, supported := p.profileForDevice(source.device)
	if !supported || !match.Supports(profiles.FeatureVideo) {
		return nil, ErrUnsupportedCamera
	}
	path, err := source.ensurePath(ctx)
	if err != nil {
		return nil, ErrCameraUnavailable
	}
	url, err := path.URL(p.cfg.AdvertiseHost, p.cfg.RTSPPort)
	if err != nil {
		return nil, errors.New("invalid RTSP endpoint configuration")
	}
	return []backend.StreamEndpoint{{ID: "live", Protocol: "rtsp", URL: url}}, nil
}

// Snapshot asks the local RTSP path for one decoded JPEG. FFmpeg runs with a
// bounded lifetime and its diagnostics are discarded to prevent URL leakage.
func (p *Provider) Snapshot(ctx context.Context, id model.CameraID) ([]byte, error) {
	source := p.source(id)
	if source == nil {
		return nil, ErrUnsupportedCamera
	}
	match, supported := p.profileForDevice(source.device)
	if !supported || !match.Supports(profiles.FeatureSnapshot) {
		return nil, ErrUnsupportedCamera
	}
	path, err := source.ensureReady(ctx)
	if err != nil {
		return nil, ErrCameraUnavailable
	}
	localURL, err := path.URL("127.0.0.1", p.cfg.RTSPPort)
	if err != nil {
		return nil, errors.New("invalid RTSP endpoint configuration")
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	command := exec.CommandContext(snapshotCtx, p.cfg.FFmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp",
		"-i", localURL, "-map", "0:v:0", "-frames:v", "1", "-an", "-c:v", "mjpeg",
		"-f", "image2pipe", "pipe:1")
	command.Stderr = io.Discard
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("could not start snapshot decoder")
	}
	if err := command.Start(); err != nil {
		return nil, errors.New("snapshot decoder is unavailable")
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, maxSnapshotBytes+1))
	if len(data) > maxSnapshotBytes {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if readErr != nil || waitErr != nil || snapshotCtx.Err() != nil || len(data) > maxSnapshotBytes || !isJPEG(data) {
		log.Printf("xiaomi_backend event=snapshot_decode result=error")
		return nil, errors.New("could not decode a camera snapshot")
	}
	log.Printf("xiaomi_backend event=snapshot_decode result=ok bytes=%d", len(data))
	return data, nil
}

// Step sends a short camera-directed step through the same live CS2 session.
// It is deliberately not advertised to Home Assistant until C201 movement is
// physically verified.
func (p *Provider) Step(ctx context.Context, id model.CameraID, direction string) error {
	p.mu.RLock()
	source := p.sources[id]
	inventoryLoaded := !p.loaded.IsZero()
	var knownCamera *cloud.Device
	if source == nil {
		for _, device := range p.devices {
			if device != nil && device.IsCamera() && cameraID(device.Did) == id {
				knownCamera = device
				break
			}
		}
	}
	p.mu.RUnlock()
	if source == nil {
		if !inventoryLoaded {
			return ErrCameraUnavailable
		}
		if knownCamera == nil {
			return ErrCameraNotFound
		}
		match, supported := p.profileForDevice(knownCamera)
		if !supported || !match.Supports(profiles.FeaturePTZ) {
			return ErrUnsupportedCamera
		}
		return ErrCameraUnavailable
	}
	if source.device == nil {
		return ErrUnsupportedCamera
	}
	match, supported := p.profileForDevice(source.device)
	if !supported || !match.Supports(profiles.FeaturePTZ) {
		return ErrUnsupportedCamera
	}
	if direction != "left" && direction != "right" && direction != "up" && direction != "down" {
		return ErrUnsupportedDirection
	}
	source.lifecycleMu.Lock()
	defer source.lifecycleMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return ErrCameraUnavailable
	}
	source.mu.Lock()
	if err := ctx.Err(); err != nil {
		source.mu.Unlock()
		p.mu.RUnlock()
		return err
	}
	run := source.session
	if source.closed || run == nil || run.isEnding() || run.session == nil || run.path == nil || source.path != run.path || !run.path.Ready() {
		source.mu.Unlock()
		p.mu.RUnlock()
		return ErrCameraUnavailable
	}
	// This check under both state locks is the acceptance point. Provider.Close
	// publishes p.closed under p.mu, while readFrames marks this run under mu.
	source.mu.Unlock()
	p.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	writeSucceeded, sanitizedLine := run.session.DiagnosticMotorStepObserved(direction, nil)
	log.Print(sanitizedLine)
	if !writeSucceeded {
		return errors.New("camera control failed")
	}
	return nil
}

func (p *Provider) source(id model.CameraID) *cameraSource {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sources[id]
}

// ensurePath returns the stable URL promptly and starts a session in the
// background. Home Assistant can add the camera before the first media packet
// arrives, while snapshots use ensureReady to wait for an actual stream.
func (s *cameraSource) ensurePath(ctx context.Context) (cameraPath, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, ErrCameraUnavailable
		}
		if s.path != nil {
			path := s.path
			s.mu.Unlock()
			return path, nil
		}
		if s.starting != nil {
			wait := s.starting
			s.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		path, err := s.provider.relay.NewPath()
		if err != nil {
			s.mu.Unlock()
			return nil, errors.New("local RTSP path could not be created")
		}
		wait := make(chan struct{})
		done := make(chan struct{})
		s.path, s.sessionDone, s.starting = path, done, wait
		s.startErr = nil
		s.mu.Unlock()
		go s.start(path, done, wait)
		return path, nil
	}
}

func (s *cameraSource) ensureReady(ctx context.Context) (cameraPath, error) {
	path, err := s.ensurePath(ctx)
	if err != nil {
		return nil, err
	}
	if path.Ready() {
		return path, nil
	}
	s.mu.Lock()
	done := s.sessionDone
	s.mu.Unlock()
	if done == nil {
		return nil, ErrCameraUnavailable
	}
	timer := time.NewTimer(frameReadyTimeout)
	defer timer.Stop()
	select {
	case <-path.ReadySignal():
		return path, nil
	case <-done:
		return nil, ErrCameraUnavailable
	case <-timer.C:
		return nil, errors.New("camera stream did not become ready")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *cameraSource) videoStatus() model.VideoStatus {
	s.mu.Lock()
	path, lastErr := s.path, s.lastErr
	s.mu.Unlock()
	if path != nil && path.Ready() {
		return model.VideoStatus{State: model.VideoStreaming}
	}
	if lastErr != nil {
		return model.VideoStatus{State: model.VideoRetrying, Reason: model.ErrorStreamTimeout}
	}
	return model.VideoStatus{State: model.VideoIdle}
}

func (s *cameraSource) start(path cameraPath, done, starting chan struct{}) {
	defer s.finishStart(starting)
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		s.failStart(path, done, ErrCameraUnavailable)
		return
	}
	match, supported := s.provider.profileForDevice(s.device)
	if !supported || !match.Supports(profiles.FeatureVideo) {
		s.failStart(path, done, ErrUnsupportedCamera)
		return
	}
	if match.Profile.ApplicationProtocol != "MISS" || match.Profile.MediaProtocol != "CS2" || match.Profile.VideoCodec != "H265" {
		s.failStart(path, done, ErrUnsupportedCamera)
		return
	}
	missSession, err := s.provider.account.MissVendor(s.device.Did)
	if err != nil || missSession == nil {
		log.Printf("xiaomi_backend event=session_negotiate result=error")
		s.failStart(path, done, errors.New("camera session negotiation failed"))
		return
	}
	if missSession.Vendor != match.Profile.Vendor {
		log.Printf("xiaomi_backend event=session_negotiate result=error")
		s.failStart(path, done, errors.New("camera session negotiation failed"))
		return
	}
	transport, supported := mediaTransportPreference(missSession.Vendor)
	if !supported {
		log.Printf("xiaomi_backend event=session_negotiate result=error")
		s.failStart(path, done, errors.New("camera session negotiation failed"))
		return
	}
	transportLabel := transport
	if transportLabel == "" {
		transportLabel = "auto"
	}
	log.Printf("xiaomi_backend event=session_negotiate result=ok vendor=%s media_transport=%s", missSession.Vendor, transportLabel)
	generation, err := newSessionGeneration()
	if err != nil {
		s.failStart(path, done, errors.New("camera session could not be identified"))
		return
	}
	host := s.device.IP
	if net.ParseIP(host) == nil {
		log.Printf("xiaomi_backend event=camera_address result=unavailable")
		s.failStart(path, done, errors.New("camera local address is unavailable"))
		return
	}
	opened, err := stream.Open(stream.Config{
		Host:          host,
		Transport:     transport,
		Model:         match.Profile.ModelID,
		Channel:       match.Profile.Channel,
		Quality:       match.Profile.Quality,
		Audio:         match.Profile.Audio,
		DevicePublic:  missSession.DevicePublic,
		ClientPublic:  missSession.ClientPublic,
		ClientPrivate: missSession.ClientPrivate,
		Sign:          missSession.Sign,
	})
	if err != nil {
		log.Printf("xiaomi_backend event=session_open result=error reason=%s", sessionFailureClass(err))
		s.failStart(path, done, errors.New("camera video session could not be opened"))
		return
	}
	session := &liveCameraSession{session: opened}
	log.Printf("xiaomi_backend event=session_open result=ok transport=%s", session.Protocol())
	s.mu.Lock()
	if s.closed || s.path != path {
		s.mu.Unlock()
		session.Close()
		s.failStart(path, done, ErrCameraUnavailable)
		return
	}
	run := &cameraSessionRun{session: session, path: path, generation: generation, ending: make(chan struct{})}
	s.session = run
	s.lastErr = nil
	s.recordDiagnosticEvent(backend.CameraDiagnosticRequest{}, "session_started", "ok", &backend.CameraSessionDiagnostics{
		Generation: generation, State: backend.SessionStarting, Protocol: safeSessionProtocol(session.Protocol()),
	})
	s.mu.Unlock()
	go s.readFrames(run, done)
}

func newSessionGeneration() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "gen_" + hex.EncodeToString(value[:]), nil
}

func mediaTransportPreference(vendor string) (string, bool) {
	if vendor != "cs2" {
		return "", false
	}
	// The MISS vendor selects the CS2 protocol family; an empty media
	// transport asks its handshake to negotiate UDP or TCP with the camera.
	return "", true
}

func sessionFailureClass(err error) string {
	if err == nil {
		return "unknown"
	}
	message := err.Error()
	// Keep the diagnostic at the protocol-phase level. Wrapped errors contain
	// the camera address and must never be logged.
	switch {
	case strings.Contains(message, "auth rejected"):
		return "camera_auth"
	case strings.Contains(message, "cs2: lan search"):
		if isTimeout(err) {
			return "cs2_lan_search_timeout"
		}
		return "cs2_lan_search"
	case strings.Contains(message, "cs2: punch"):
		if isTimeout(err) {
			return "cs2_punch_timeout"
		}
		return "cs2_punch"
	case strings.Contains(message, "miss: login"):
		if isTimeout(err) {
			return "auth_timeout"
		}
		return "auth_protocol"
	case strings.Contains(message, "stream: start media"):
		if isTimeout(err) {
			return "media_command_timeout"
		}
		return "media_command"
	case strings.Contains(message, "encrypted payload shorter than its nonce"):
		return "short_encrypted_payload"
	case strings.Contains(message, "cs2: short media packet"):
		return "short_media_packet"
	case strings.Contains(message, "miss: packet header too small"):
		return "short_packet_header"
	case strings.Contains(message, "cs2: tcp buffer too small"):
		return "transport_frame_too_large"
	case strings.Contains(message, "pop buffer is full"):
		return "media_buffer_full"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return "timeout"
		}
		return "network"
	}
	switch {
	case strings.Contains(message, "shared key"):
		return "key_exchange"
	default:
		return "protocol"
	}
}

func isTimeout(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr) && networkErr.Timeout()
}

func (s *cameraSource) finishStart(starting chan struct{}) {
	s.mu.Lock()
	if s.starting == starting {
		s.starting = nil
	}
	s.mu.Unlock()
	close(starting)
}

func (s *cameraSource) failStart(path cameraPath, done chan struct{}, err error) {
	path.Close()
	close(done)
	s.mu.Lock()
	if s.path == path {
		s.session, s.path, s.sessionDone = nil, nil, nil
		s.lastErr = err
	}
	s.startErr = err
	s.mu.Unlock()
}

func (s *cameraSource) readFrames(run *cameraSessionRun, done chan struct{}) {
	defer close(done)
	var terminal error
	firstFrameLogged := false
	for {
		frame, err := run.session.Read()
		if err != nil {
			terminal = err
			break
		}
		if frame == nil || frame.Kind != stream.KindVideo {
			continue
		}
		if frame.Codec != stream.CodecH265 {
			log.Printf("xiaomi_backend event=media_codec result=unsupported")
			terminal = errors.New("unsupported codec")
			break
		}
		wasReady := run.path.Ready()
		if err := run.path.WriteAccessUnit(frame.Data, frame.PTS, frame.Keyframe); err != nil {
			log.Printf("xiaomi_backend event=rtsp_publish result=error")
			terminal = err
			break
		}
		if !firstFrameLogged {
			firstFrameLogged = true
			log.Printf("xiaomi_backend event=first_media result=ok codec=h265")
		}
		if !wasReady && run.path.Ready() {
			log.Printf("xiaomi_backend event=rtsp_description result=ready")
		}
	}
	// Publish the end of frame reception before logging or waiting for the
	// lifecycle lock so a queued Step cannot use a stale Ready path.
	s.markSessionEnding(run)
	if terminal != nil {
		if errors.Is(terminal, stream.ErrClosed) {
			log.Printf("xiaomi_backend event=session_end result=closed transport=%s", run.session.Protocol())
		} else {
			log.Printf("xiaomi_backend event=media_read result=error reason=%s transport=%s", sessionFailureClass(terminal), run.session.Protocol())
			log.Printf("xiaomi_backend event=session_end result=error transport=%s", run.session.Protocol())
		}
	}
	s.retireSession(run, terminal)
}

func (s *cameraSource) markSessionEnding(run *cameraSessionRun) {
	s.mu.Lock()
	s.markSessionEndingLocked(run)
	s.mu.Unlock()
}

func (s *cameraSource) markSessionEndingLocked(run *cameraSessionRun) {
	if run != nil && s.session == run {
		if run.ending == nil {
			run.ending = make(chan struct{})
		}
		select {
		case <-run.ending:
		default:
			close(run.ending)
		}
	}
}

func (s *cameraSource) retireSession(run *cameraSessionRun, terminal error) {
	// The reader publishes termination before waiting for lifecycleMu. Step
	// and retirement then use the same lock to pin or close this exact run.
	s.markSessionEnding(run)
	s.lifecycleMu.Lock()
	s.mu.Lock()
	owned := s.session == run
	s.mu.Unlock()
	if owned {
		run.session.Close()
		run.path.Close()
	}
	s.mu.Lock()
	if owned && s.session == run {
		s.session, s.path = nil, nil
		s.sessionDone = nil
		s.lastErr = terminal
	}
	s.mu.Unlock()
	if owned {
		s.recordDiagnosticEvent(backend.CameraDiagnosticRequest{}, "session_ended", "ended", &backend.CameraSessionDiagnostics{
			Generation: run.generation, State: backend.SessionUnavailable, Protocol: safeSessionProtocol(run.session.Protocol()),
		})
	}
	s.lifecycleMu.Unlock()
}

func (p *Provider) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		sources := make([]*cameraSource, 0, len(p.sources))
		for _, source := range p.sources {
			sources = append(sources, source)
		}
		p.mu.Unlock()
		for _, source := range sources {
			source.mu.Lock()
			source.closed = true
			run := source.session
			source.markSessionEndingLocked(run)
			source.mu.Unlock()
			if run != nil {
				source.retireSession(run, ErrCameraUnavailable)
			}
			source.lifecycleMu.Lock()
			source.mu.Lock()
			path := source.path
			source.path = nil
			source.sessionDone = nil
			source.mu.Unlock()
			if path != nil {
				path.Close()
			}
			source.lifecycleMu.Unlock()
		}
		if p.relay != nil {
			p.relay.Close()
		}
	})
}

func cameraID(did string) model.CameraID {
	digest := sha256.Sum256([]byte(did))
	return model.CameraID("xiaomi_" + hex.EncodeToString(digest[:8]))
}

func cameraName(device *cloud.Device) string {
	if strings.TrimSpace(device.Name) != "" {
		return device.Name
	}
	return "Xiaomi Camera"
}

func isJPEG(data []byte) bool {
	return len(data) >= 4 && data[0] == 0xff && data[1] == 0xd8 && data[len(data)-2] == 0xff && data[len(data)-1] == 0xd9
}

// ErrorText returns a stable, non-sensitive summary suitable for operator logs.
func ErrorText(err error) string {
	switch {
	case errors.Is(err, ErrCredentialsIncomplete):
		return "Xiaomi account configuration is incomplete"
	case errors.Is(err, ErrCredentialsInvalid):
		return "Xiaomi account authorization failed"
	case errors.Is(err, ErrCameraUnavailable):
		return "Xiaomi camera is unavailable"
	case errors.Is(err, ErrUnsupportedCamera):
		return "Xiaomi camera model is unsupported"
	case err == nil:
		return ""
	default:
		return fmt.Sprintf("Xiaomi camera backend unavailable (%T)", err)
	}
}
