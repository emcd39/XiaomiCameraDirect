package xiaomi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/profiles"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/protocol/cloud"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/xiaomi/protocol/stream"
)

func TestConfigFromEnvLeavesProviderDisabledWithoutCredentials(t *testing.T) {
	for _, key := range []string{
		"XLG_XIAOMI_USER_ID", "XLG_XIAOMI_REGION", "XLG_XIAOMI_TOKEN",
		"XLG_RTSP_ADVERTISE_HOST", "XLG_RTSP_LISTEN", "XLG_FFMPEG_PATH", "XLG_XIAOMI_PROFILE_TEST_MODE",
	} {
		t.Setenv(key, "")
	}
	cfg, configured, err := ConfigFromEnv()
	if err != nil || configured || cfg != (Config{}) {
		t.Fatalf("ConfigFromEnv() = %#v, %t, %v; want zero config, disabled, nil", cfg, configured, err)
	}
}

func TestConfigFromEnvRequiresExplicitExperimentalProfileTestMode(t *testing.T) {
	t.Setenv("XLG_XIAOMI_USER_ID", "test-user")
	t.Setenv("XLG_XIAOMI_REGION", "de")
	t.Setenv("XLG_XIAOMI_TOKEN", "test-token")
	t.Setenv("XLG_RTSP_ADVERTISE_HOST", "127.0.0.1")
	t.Setenv("XLG_XIAOMI_PROFILE_TEST_MODE", "")

	cfg, configured, err := ConfigFromEnv()
	if err != nil || !configured || cfg.TestExperimentalProfiles {
		t.Fatalf("default profile test mode = %#v, configured=%t, err=%v", cfg, configured, err)
	}
	t.Setenv("XLG_XIAOMI_PROFILE_TEST_MODE", "true")
	cfg, configured, err = ConfigFromEnv()
	if err != nil || !configured || !cfg.TestExperimentalProfiles {
		t.Fatalf("explicit profile test mode = %#v, configured=%t, err=%v", cfg, configured, err)
	}
}

func TestConfigFromEnvRequiresAllAccountFields(t *testing.T) {
	for key, value := range map[string]string{
		"XLG_XIAOMI_USER_ID":      "private-user",
		"XLG_XIAOMI_REGION":       "de",
		"XLG_XIAOMI_TOKEN":        "private-token",
		"XLG_RTSP_ADVERTISE_HOST": "xlg-gateway",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("XLG_XIAOMI_TOKEN", "")
	_, configured, err := ConfigFromEnv()
	if !configured || err != ErrCredentialsIncomplete {
		t.Fatalf("ConfigFromEnv() = configured %t, error %v; want incomplete credentials", configured, err)
	}
	if strings.Contains(ErrorText(err), "private") {
		t.Fatal("sanitized error included a credential marker")
	}
}

func TestCameraIDDoesNotExposeXiaomiDeviceID(t *testing.T) {
	const did = "1213911322"
	got := string(cameraID(did))
	if strings.Contains(got, did) || !strings.HasPrefix(got, "xiaomi_") {
		t.Fatalf("camera ID %q exposed source identity or has wrong prefix", got)
	}
}

func TestMediaTransportPreferenceMapsCS2VendorToNegotiation(t *testing.T) {
	transport, ok := mediaTransportPreference("cs2")
	if !ok || transport != "" {
		t.Fatalf("mediaTransportPreference(cs2) = %q, %t; want automatic negotiation", transport, ok)
	}
	if _, ok := mediaTransportPreference("unknown"); ok {
		t.Fatal("unknown MISS vendor was accepted")
	}
}

func TestProviderProfilesPreserveC201IdentityAndBaselineWithoutInventoryFirmware(t *testing.T) {
	firstDevice := &cloud.Device{Did: "c201-device-a", Name: "C201", Model: supportedModel}
	secondDevice := &cloud.Device{Did: "c201-device-b", Name: "C201", Model: supportedModel}
	provider := &Provider{
		cfg:     Config{Region: "de"},
		sources: make(map[model.CameraID]*cameraSource),
		devices: []*cloud.Device{firstDevice, secondDevice},
		loaded:  time.Now(),
	}

	cameras, err := provider.Cameras(context.Background())
	if err != nil {
		t.Fatalf("Cameras() error = %v", err)
	}
	if len(cameras) != 2 {
		t.Fatalf("Cameras() returned %d entries, want two", len(cameras))
	}
	if cameras[0].ID == cameras[1].ID || cameras[0].ID != cameraID(firstDevice.Did) || cameras[1].ID != cameraID(secondDevice.Did) {
		t.Fatalf("camera identities = %q, %q; want distinct IDs derived from each DID", cameras[0].ID, cameras[1].ID)
	}
	for _, camera := range cameras {
		if camera.ModelID != supportedModel || camera.Firmware != "" || !camera.Selected || !camera.Capabilities.Video || !camera.Capabilities.Snapshot {
			t.Errorf("C201 baseline changed without inventory firmware: %#v", camera)
		}
		if camera.Support == nil || camera.Support.Features["video"].Reason != model.ErrorUnverifiedFirmware {
			t.Errorf("missing firmware was not represented as unverified: %#v", camera.Support)
		}
	}
	if len(provider.sources) != 2 {
		t.Fatalf("source count = %d, want separate source per camera identity", len(provider.sources))
	}
	match, ok := provider.profileForDevice(firstDevice)
	if !ok || match.FirmwareKnown || !match.RegionKnown || !match.Supports("video") || match.Supports("audio") {
		t.Fatalf("missing-firmware profile match = %#v, supported=%t", match, ok)
	}
}

func TestProviderDoesNotAdvertiseUnprofiledCandidateModels(t *testing.T) {
	provider := &Provider{
		cfg:     Config{Region: "de"},
		sources: make(map[model.CameraID]*cameraSource),
		devices: []*cloud.Device{
			{Did: "candidate-c400", Name: "C400", Model: "chuangmi.camera.039a04"},
			// The Mi 360's exact model ID has not been identified; this placeholder
			// deliberately exercises unknown-model behavior without guessing it.
			{Did: "candidate-mi360", Name: "Mi 360", Model: "xiaomi.camera.unidentified"},
		},
		loaded: time.Now(),
	}

	cameras, err := provider.Cameras(context.Background())
	if err != nil {
		t.Fatalf("Cameras() error = %v", err)
	}
	if len(cameras) != 2 {
		t.Fatalf("Cameras() returned %d entries, want both candidates", len(cameras))
	}
	for _, camera := range cameras {
		if camera.Selected || camera.Capabilities.Video || camera.Capabilities.Snapshot || camera.Video.State != model.VideoUnsupported {
			t.Errorf("unprofiled candidate acquired capabilities: %#v", camera)
		}
		if camera.Support == nil || camera.Support.Features["video"].Reason != model.ErrorUnsupportedModel {
			t.Errorf("unprofiled candidate lacks explicit unsupported state: %#v", camera.Support)
		}
	}
}

func TestProviderExperimentalProfileRequiresExplicitTestMode(t *testing.T) {
	const experimentalModel = "test.camera.experimental"
	registry, err := profiles.NewRegistry(profiles.Profile{
		ID: "test-experimental-profile", Revision: 1, ModelID: experimentalModel,
		Regions: []string{"de"}, FirmwareVersions: []string{"test-fw"},
		Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		VideoCodec: "H265", Channel: "0", Quality: "auto", Experimental: true,
		BaselineFeatures: []profiles.Feature{profiles.FeatureVideo, profiles.FeatureSnapshot, profiles.FeaturePTZ},
	})
	if err != nil {
		t.Fatal(err)
	}
	device := &cloud.Device{Did: "experimental-camera", Name: "Experimental", Model: experimentalModel}
	id := cameraID(device.Did)

	provider := &Provider{
		cfg:             Config{Region: "de"},
		profileRegistry: registry,
		sources:         make(map[model.CameraID]*cameraSource),
		devices:         []*cloud.Device{device},
		loaded:          time.Now(),
	}
	cameras, err := provider.Cameras(context.Background())
	if err != nil || len(cameras) != 1 {
		t.Fatalf("default-mode Cameras() = %#v, err=%v", cameras, err)
	}
	if cameras[0].Selected || cameras[0].Capabilities.Video || cameras[0].Capabilities.Snapshot || cameras[0].Video.State != model.VideoUnsupported {
		t.Fatalf("default mode exposed experimental capabilities: %#v", cameras[0])
	}
	if len(provider.sources) != 0 {
		t.Fatalf("default mode created %d media sources for experimental model", len(provider.sources))
	}
	if _, err := provider.Streams(context.Background(), id); !errors.Is(err, ErrUnsupportedCamera) {
		t.Fatalf("default mode Streams() error = %v, want ErrUnsupportedCamera", err)
	}
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrUnsupportedCamera) {
		t.Fatalf("default mode Step() error = %v, want ErrUnsupportedCamera", err)
	}
	if len(provider.sources) != 0 {
		t.Fatalf("default-mode requests created a media source")
	}

	testProvider := &Provider{
		cfg:             Config{Region: "de", TestExperimentalProfiles: true},
		profileRegistry: registry,
		sources:         make(map[model.CameraID]*cameraSource),
		devices:         []*cloud.Device{device},
		loaded:          time.Now(),
	}
	testCameras, err := testProvider.Cameras(context.Background())
	if err != nil || len(testCameras) != 1 {
		t.Fatalf("test-mode Cameras() = %#v, err=%v", testCameras, err)
	}
	if !testCameras[0].Capabilities.Video || !testCameras[0].Capabilities.Snapshot || testCameras[0].Selected {
		t.Fatalf("explicit test mode did not expose only declared non-autoselected capabilities: %#v", testCameras[0])
	}
	if testCameras[0].Support == nil || testCameras[0].Support.Features["video"].Implementation != "research" {
		t.Fatalf("test-mode profile evidence was not marked experimental: %#v", testCameras[0].Support)
	}

	ptzProvider, ptzID, source, _, session, _ := newProviderStepFixture(true, true)
	ptzProvider.cfg = Config{Region: "de", TestExperimentalProfiles: true}
	ptzProvider.profileRegistry = registry
	source.device.Model = experimentalModel
	if err := ptzProvider.Step(context.Background(), ptzID, "left"); err != nil {
		t.Fatalf("test-mode Step() error = %v", err)
	}
	<-session.diagnosticEntered
	if diagnostics, writes, _, directions := session.stats(); diagnostics != 1 || writes != 1 || !reflect.DeepEqual(directions, []string{"left"}) {
		t.Fatalf("test-mode diagnostics/writes/directions = %d/%d/%v, want one declared PTZ write", diagnostics, writes, directions)
	}
}

func TestProviderProfileRejectsUnknownModelBeforePTZWrite(t *testing.T) {
	provider, id, source, _, session, _ := newProviderStepFixture(true, true)
	source.device.Model = "xiaomi.camera.unprofiled"
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrUnsupportedCamera) {
		t.Fatalf("Step() error = %v, want ErrUnsupportedCamera", err)
	}
	if diagnostics, writes, _, _ := session.stats(); diagnostics != 0 || writes != 0 {
		t.Fatalf("unsupported-model diagnostics/writes = %d/%d, want zero", diagnostics, writes)
	}
}

func TestFinishStartClearsClosedStartSignal(t *testing.T) {
	starting := make(chan struct{})
	source := &cameraSource{starting: starting}

	source.finishStart(starting)

	select {
	case <-starting:
	default:
		t.Fatal("finishStart did not wake waiters")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.starting != nil {
		t.Fatal("finishStart left a closed start signal installed")
	}
}

func TestSessionFailureClassReportsSanitizedProtocolPhase(t *testing.T) {
	timeout := &net.OpError{Op: "read", Net: "udp", Err: os.ErrDeadlineExceeded}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "LAN search",
			err:  fmt.Errorf("cs2: lan search to 192.168.3.119: %w", timeout),
			want: "cs2_lan_search_timeout",
		},
		{
			name: "UDP punch",
			err:  fmt.Errorf("cs2: punch to 192.168.3.119: %w", timeout),
			want: "cs2_punch_timeout",
		},
		{
			name: "authentication",
			err:  fmt.Errorf("miss: login: %w", timeout),
			want: "auth_timeout",
		},
		{
			name: "media command",
			err:  fmt.Errorf("stream: start media: %w", timeout),
			want: "media_command_timeout",
		},
		{
			name: "media read timeout",
			err:  timeout,
			want: "timeout",
		},
		{
			name: "media read protocol error",
			err:  fmt.Errorf("cs2: malformed media from 192.168.3.119: private payload"),
			want: "protocol",
		},
		{
			name: "short encrypted media payload",
			err:  fmt.Errorf("miss: xiaomi: encrypted payload shorter than its nonce"),
			want: "short_encrypted_payload",
		},
		{
			name: "short media packet",
			err:  fmt.Errorf("miss: read media: cs2: short media packet"),
			want: "short_media_packet",
		},
		{
			name: "incomplete TCP frame",
			err:  fmt.Errorf("miss: read media: %w", io.ErrUnexpectedEOF),
			want: "unexpected_eof",
		},
		{
			name: "media channel closed with EOF",
			err:  fmt.Errorf("miss: read media: %w", io.EOF),
			want: "eof",
		},
		{
			name: "transport frame too large",
			err:  fmt.Errorf("cs2: tcp buffer too small"),
			want: "transport_frame_too_large",
		},
		{
			name: "media channel buffer full",
			err:  fmt.Errorf("cs2: pop buffer is full"),
			want: "media_buffer_full",
		},
		{
			name: "camera authentication rejection",
			err:  fmt.Errorf("miss: login: miss: auth rejected: private payload"),
			want: "camera_auth",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sessionFailureClass(test.err)
			if got != test.want {
				t.Fatalf("sessionFailureClass() = %q, want %q", got, test.want)
			}
			if strings.Contains(got, "192.168.3.119") || strings.Contains(got, "private") {
				t.Fatalf("sanitized class leaked error detail: %q", got)
			}
		})
	}
}

type fakeCameraPath struct {
	mu          sync.Mutex
	ready       bool
	readySignal chan struct{}
	closeCount  int
}

func newFakeCameraPath(ready bool) *fakeCameraPath {
	readySignal := make(chan struct{})
	if ready {
		close(readySignal)
	}
	return &fakeCameraPath{ready: ready, readySignal: readySignal}
}

func (p *fakeCameraPath) URL(string, int) (string, error) { return "rtsp://127.0.0.1/fake", nil }
func (p *fakeCameraPath) Ready() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ready
}
func (p *fakeCameraPath) ReadySignal() <-chan struct{}              { return p.readySignal }
func (p *fakeCameraPath) WriteAccessUnit([]byte, int64, bool) error { return nil }
func (p *fakeCameraPath) Close() {
	p.mu.Lock()
	p.closeCount++
	p.mu.Unlock()
}
func (p *fakeCameraPath) closeCountValue() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeCount
}

type fakeReadResult struct {
	frame *stream.Frame
	err   error
}

type fakeCameraSession struct {
	mu                sync.Mutex
	reads             chan fakeReadResult
	closed            chan struct{}
	closeOnce         sync.Once
	closeCount        int
	diagnosticCalls   int
	directions        []string
	writes            int
	writeOK           bool
	diagnosticEntered chan struct{}
	releaseDiagnostic <-chan struct{}
	diagnosticsCalls  int
	diagnosticsBlock  <-chan struct{}
}

func newFakeCameraSession(writeOK bool) *fakeCameraSession {
	return &fakeCameraSession{
		reads:             make(chan fakeReadResult, 1),
		closed:            make(chan struct{}),
		writeOK:           writeOK,
		diagnosticEntered: make(chan struct{}, 1),
	}
}

func (s *fakeCameraSession) Read() (*stream.Frame, error) {
	select {
	case result := <-s.reads:
		return result.frame, result.err
	case <-s.closed:
		return nil, stream.ErrClosed
	}
}

func (s *fakeCameraSession) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closeCount++
		s.mu.Unlock()
		close(s.closed)
	})
}

func (s *fakeCameraSession) Protocol() string { return "cs2+tcp" }

func (s *fakeCameraSession) CameraSessionDiagnostics(ctx context.Context) (backend.CameraSessionDiagnostics, error) {
	s.mu.Lock()
	s.diagnosticsCalls++
	block := s.diagnosticsBlock
	s.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return backend.CameraSessionDiagnostics{}, ctx.Err()
		case <-block:
		}
	}
	return backend.CameraSessionDiagnostics{
		Protocol: "cs2+tcp", PeerClass: "private_ip", Frames: 17, Bytes: 4096, Dropped: 2, Replies: 1,
	}, ctx.Err()
}

func (s *fakeCameraSession) DiagnosticMotorStepObserved(direction string, onWrite func()) (bool, string) {
	s.mu.Lock()
	s.diagnosticCalls++
	s.directions = append(s.directions, direction)
	release := s.releaseDiagnostic
	writeOK := s.writeOK
	s.mu.Unlock()
	s.diagnosticEntered <- struct{}{}
	if release != nil {
		<-release
	}
	if writeOK {
		s.mu.Lock()
		s.writes++
		s.mu.Unlock()
		if onWrite != nil {
			onWrite()
		}
	}
	return writeOK, "PTZ_DIAG write_status=sent reply_status=no_reply motion=not_measured"
}

func (s *fakeCameraSession) stats() (diagnosticCalls, writes, closeCount int, directions []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diagnosticCalls, s.writes, s.closeCount, append([]string(nil), s.directions...)
}

func TestCameraDiagnosticsReadsExistingSessionWithoutOpeningOrReading(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	got, err := provider.CameraDiagnostics(context.Background(), id, testTransportDiagnosticsRequest(run.generation))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != backend.SessionStreaming || got.Protocol != "cs2+tcp" || got.PeerClass != "private_ip" || got.Frames != 17 || got.Bytes != 4096 {
		t.Fatalf("session diagnostics = %#v", got)
	}
	source.mu.Lock()
	stillSameSession := source.session == run && source.path == path
	source.mu.Unlock()
	if !stillSameSession {
		t.Fatal("diagnostics replaced or retired the gateway's active session")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 0 || writes != 0 || closes != 0 || session.diagnosticsCalls != 1 || len(session.reads) != 0 {
		t.Fatalf("diagnostics had side effects: ptz=%d writes=%d closes=%d snapshots=%d queued_reads=%d", diagnostics, writes, closes, session.diagnosticsCalls, len(session.reads))
	}
	if path.closeCountValue() != 0 {
		t.Fatal("diagnostics closed the media path")
	}
}

func TestCameraDiagnosticsRejectsStaleGenerationAndBoundsEvents(t *testing.T) {
	provider, id, _, run, session, _ := newProviderStepFixture(true, true)
	request := testTransportDiagnosticsRequest("gen_ffffffffffffffffffffffffffffffff")
	if _, err := provider.CameraDiagnostics(context.Background(), id, request); !errors.Is(err, backend.ErrStaleCameraSessionGeneration) {
		t.Fatalf("stale generation error = %v", err)
	}
	if session.diagnosticsCalls != 0 {
		t.Fatalf("stale generation reached session metadata %d times", session.diagnosticsCalls)
	}

	for i := 1; i <= backend.MaxCameraDiagnosticEvents+8; i++ {
		request := backend.CameraDiagnosticRequest{
			Operation: backend.DiagnosticSessionStatus, CorrelationID: fmt.Sprintf("diag_%032x", i),
		}
		got, err := provider.CameraDiagnostics(context.Background(), id, request)
		if err != nil {
			t.Fatalf("status event %d: %v", i, err)
		}
		if len(got.Events) > backend.MaxCameraDiagnosticEvents {
			t.Fatalf("returned %d events; limit is %d", len(got.Events), backend.MaxCameraDiagnosticEvents)
		}
	}
	last, err := provider.CameraDiagnostics(context.Background(), id, backend.CameraDiagnosticRequest{
		Operation: backend.DiagnosticSessionStatus, CorrelationID: "diag_ffffffffffffffffffffffffffffffff",
	})
	if err != nil || len(last.Events) != backend.MaxCameraDiagnosticEvents {
		t.Fatalf("bounded event snapshot length=%d error=%v", len(last.Events), err)
	}
	if last.Events[0].Sequence <= 1 || last.Events[len(last.Events)-1].Sequence <= last.Events[0].Sequence {
		t.Fatalf("event ring did not retain the newest records: %#v", last.Events)
	}
	if last.Generation != run.generation || last.CorrelationID != "diag_ffffffffffffffffffffffffffffffff" {
		t.Fatalf("status correlation/generation = %#v", last)
	}
	if _, writes, closes, _ := session.stats(); writes != 0 || closes != 0 || session.diagnosticsCalls != 0 {
		t.Fatalf("status/stale operations touched media: stats=%d writes=%d closes=%d", session.diagnosticsCalls, writes, closes)
	}
}

func TestCameraDiagnosticsHonorsTimeoutDuringMetadataRead(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	block := make(chan struct{})
	session.diagnosticsBlock = block
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := provider.CameraDiagnostics(ctx, id, testTransportDiagnosticsRequest(run.generation))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("diagnostics error = %v, want deadline exceeded", err)
	}
	source.mu.Lock()
	stillSameSession := source.session == run && source.path == path
	source.mu.Unlock()
	if !stillSameSession {
		t.Fatal("timed-out diagnostics changed the session")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 0 || writes != 0 || closes != 0 || path.closeCountValue() != 0 {
		t.Fatalf("timed-out diagnostics had side effects: ptz=%d writes=%d closes=%d path_closes=%d", diagnostics, writes, closes, path.closeCountValue())
	}
	close(block)
	if _, err := provider.CameraDiagnostics(context.Background(), id, testTransportDiagnosticsRequest(run.generation)); err != nil {
		t.Fatalf("diagnostic lock remained held after timeout: %v", err)
	}
}

func TestCameraDiagnosticsHonorsCancellationWhileWaitingForSourceLock(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	source.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := provider.CameraDiagnostics(ctx, id, testTransportDiagnosticsRequest(run.generation))
	source.mu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("diagnostics error = %v, want deadline exceeded", err)
	}
	if source.session != run || source.path != path {
		t.Fatal("canceled diagnostics changed the active source")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 0 || writes != 0 || closes != 0 {
		t.Fatalf("canceled diagnostics reached the session: ptz=%d writes=%d closes=%d", diagnostics, writes, closes)
	}
}

func TestCameraDiagnosticsInventoryLockWaitHonorsContextAndReleasesLifecycle(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			provider, id, source, run, session, _ := newProviderStepFixture(true, true)
			provider.mu.Lock()
			inventoryLocked := true
			defer func() {
				if inventoryLocked {
					provider.mu.Unlock()
				}
			}()

			var ctx context.Context
			var cancel context.CancelFunc
			if mode == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()

			done := make(chan error, 1)
			go func() {
				_, err := provider.cameraDiagnosticsForSource(ctx, id, source, testTransportDiagnosticsRequest(run.generation))
				done <- err
			}()

			waitUntil := time.Now().Add(time.Second)
			for {
				if !source.lifecycleMu.TryLock() {
					break
				}
				source.lifecycleMu.Unlock()
				if time.Now().After(waitUntil) {
					t.Fatal("diagnostics did not acquire lifecycleMu before waiting for the inventory lock")
				}
				time.Sleep(time.Millisecond)
			}

			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("diagnostics error = %v, want canceled", err)
				}
				if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("diagnostics error = %v, want deadline exceeded", err)
				}
			case <-time.After(time.Second):
				t.Fatal("diagnostics waited for the inventory lock after cancellation/deadline")
			}

			if !source.lifecycleMu.TryLock() {
				t.Fatal("diagnostics retained lifecycleMu while inventory lock was still held")
			}
			source.lifecycleMu.Unlock()
			provider.mu.Unlock()
			inventoryLocked = false

			if _, err := provider.CameraDiagnostics(context.Background(), id, testTransportDiagnosticsRequest(run.generation)); err != nil {
				t.Fatalf("current generation after inventory unlock: %v", err)
			}
			if session.diagnosticsCalls != 1 {
				t.Fatalf("current generation metadata reads = %d, want 1", session.diagnosticsCalls)
			}
			if _, err := provider.CameraDiagnostics(context.Background(), id, testTransportDiagnosticsRequest("gen_ffffffffffffffffffffffffffffffff")); !errors.Is(err, backend.ErrStaleCameraSessionGeneration) {
				t.Fatalf("stale generation error = %v, want stale generation", err)
			}
			if session.diagnosticsCalls != 1 {
				t.Fatalf("stale generation reached metadata reader; calls=%d", session.diagnosticsCalls)
			}
		})
	}
}

func newProviderStepFixture(pathReady, writeOK bool) (*Provider, model.CameraID, *cameraSource, *cameraSessionRun, *fakeCameraSession, *fakeCameraPath) {
	return newProviderStepFixtureForID("xiaomi_0123456789abcdef", pathReady, writeOK)
}

func newProviderStepFixtureForID(id model.CameraID, pathReady, writeOK bool) (*Provider, model.CameraID, *cameraSource, *cameraSessionRun, *fakeCameraSession, *fakeCameraPath) {
	path := newFakeCameraPath(pathReady)
	session := newFakeCameraSession(writeOK)
	run := &cameraSessionRun{session: session, path: path, generation: "gen_0123456789abcdef0123456789abcdef", ending: make(chan struct{})}
	source := &cameraSource{
		device:  &cloud.Device{Model: supportedModel},
		id:      id,
		session: run,
		path:    path,
	}
	provider := &Provider{
		sources: map[model.CameraID]*cameraSource{id: source},
		devices: []*cloud.Device{source.device},
		loaded:  time.Now(),
	}
	source.provider = provider
	return provider, id, source, run, session, path
}

func testTransportDiagnosticsRequest(generation string) backend.CameraDiagnosticRequest {
	return backend.CameraDiagnosticRequest{
		Operation: backend.DiagnosticTransportMetadata, CorrelationID: "diag_0123456789abcdef0123456789abcdef",
		ExpectedGeneration: generation,
	}
}

func TestProviderStepUsesExactlyTheExistingReadySession(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	wantDirections := []string{"left", "right", "up", "down"}
	for _, direction := range wantDirections {
		if err := provider.Step(context.Background(), id, direction); err != nil {
			t.Fatalf("Step(%q) error = %v", direction, err)
		}
		<-session.diagnosticEntered
	}
	diagnostics, writes, closes, directions := session.stats()
	if diagnostics != 4 || writes != 4 || closes != 0 || !reflect.DeepEqual(directions, wantDirections) {
		t.Fatalf("diagnostic calls/writes/closes/directions = %d/%d/%d/%v", diagnostics, writes, closes, directions)
	}
	source.mu.Lock()
	stillSameRun := source.session == run && source.path == path
	source.mu.Unlock()
	if !stillSameRun || path.closeCountValue() != 0 {
		t.Fatal("Step changed or closed the existing session/path")
	}
}

func TestProviderSerializesStepsOnTheSameCameraSession(t *testing.T) {
	provider, id, _, _, session, _ := newProviderStepFixture(true, true)
	release := make(chan struct{})
	session.releaseDiagnostic = release
	firstResult := make(chan error, 1)
	go func() { firstResult <- provider.Step(context.Background(), id, "left") }()
	select {
	case <-session.diagnosticEntered:
	case <-time.After(time.Second):
		t.Fatal("first step did not enter the camera session")
	}
	secondResult := make(chan error, 1)
	go func() { secondResult <- provider.Step(context.Background(), id, "right") }()
	select {
	case <-session.diagnosticEntered:
		t.Fatal("second step entered while the first response was still being observed")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first step error = %v", err)
	}
	select {
	case <-session.diagnosticEntered:
	case <-time.After(time.Second):
		t.Fatal("second step did not enter after the first completed")
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second step error = %v", err)
	}
	_, writes, _, directions := session.stats()
	if writes != 2 || !reflect.DeepEqual(directions, []string{"left", "right"}) {
		t.Fatalf("serialized writes/directions = %d/%v", writes, directions)
	}
}

func TestProviderStepRejectsUnknownMissingAndUnreadySessions(t *testing.T) {
	provider, id, source, _, session, _ := newProviderStepFixture(false, true)
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("unready Step() error = %v, want ErrCameraUnavailable", err)
	}
	if diagnostics, writes, _, _ := session.stats(); diagnostics != 0 || writes != 0 {
		t.Fatalf("unready session diagnostics/writes = %d/%d", diagnostics, writes)
	}

	// A missing session and path must fail locally. A nil relay makes any
	// accidental ensurePath call fail this test immediately.
	source.mu.Lock()
	source.session, source.path = nil, nil
	source.mu.Unlock()
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("missing session Step() error = %v, want ErrCameraUnavailable", err)
	}
	if err := provider.Step(context.Background(), "xiaomi_ffffffffffffffff", "left"); !errors.Is(err, ErrCameraNotFound) {
		t.Fatalf("unknown ID Step() error = %v, want ErrCameraNotFound", err)
	}
	if err := provider.Step(context.Background(), id, "diagonal"); !errors.Is(err, ErrUnsupportedDirection) {
		t.Fatalf("diagonal Step() error = %v, want ErrUnsupportedDirection", err)
	}
	if diagnostics, writes, _, _ := session.stats(); diagnostics != 0 || writes != 0 {
		t.Fatalf("rejected requests diagnostics/writes = %d/%d", diagnostics, writes)
	}
}

func TestProviderStepDistinguishesKnownUnsupportedModelFromUnknownID(t *testing.T) {
	const did = "camera-with-unsupported-model"
	knownID := cameraID(did)
	provider := &Provider{
		sources: map[model.CameraID]*cameraSource{},
		devices: []*cloud.Device{{Did: did, Model: "xiaomi.camera.other_model"}},
		loaded:  time.Now(),
	}

	if err := provider.Step(context.Background(), knownID, "left"); !errors.Is(err, ErrUnsupportedCamera) {
		t.Fatalf("known unsupported camera Step() error = %v, want ErrUnsupportedCamera", err)
	}
	if err := provider.Step(context.Background(), "xiaomi_ffffffffffffffff", "left"); !errors.Is(err, ErrCameraNotFound) {
		t.Fatalf("unknown ID Step() error = %v, want ErrCameraNotFound", err)
	}

	provider.devices = append(provider.devices, &cloud.Device{Did: "known-c201-without-source", Model: supportedModel})
	if err := provider.Step(context.Background(), cameraID("known-c201-without-source"), "left"); !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("known C201 without a prepared source Step() error = %v, want ErrCameraUnavailable", err)
	}
}

func TestProviderStepDistinguishesUnloadedFromLoadedEmptyInventory(t *testing.T) {
	provider := &Provider{sources: map[model.CameraID]*cameraSource{}}
	id := model.CameraID("xiaomi_0123456789abcdef")
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("Step() before inventory load error = %v, want ErrCameraUnavailable", err)
	}

	// A non-zero loaded timestamp with no devices represents a successful empty
	// inventory. Step must not refresh the inventory or contact Xiaomi.
	provider.mu.Lock()
	provider.loaded = time.Now()
	provider.mu.Unlock()
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrCameraNotFound) {
		t.Fatalf("Step() after loading an empty inventory error = %v, want ErrCameraNotFound", err)
	}
}

func TestProviderStepRejectsSessionMarkedEndingBeforeAcceptance(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	source.markSessionEnding(run)
	if !path.Ready() {
		t.Fatal("fixture path should remain ready until cleanup")
	}
	if err := provider.Step(context.Background(), id, "left"); !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("Step() error = %v, want ErrCameraUnavailable", err)
	}
	if diagnostics, writes, _, _ := session.stats(); diagnostics != 0 || writes != 0 || path.closeCountValue() != 0 {
		t.Fatalf("ending session diagnostics/writes/path closes = %d/%d/%d", diagnostics, writes, path.closeCountValue())
	}
}

func TestProviderStepCanceledWhileWaitingForLifecycleLockDoesNotWrite(t *testing.T) {
	provider, id, source, _, session, _ := newProviderStepFixture(true, true)
	source.lifecycleMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- provider.Step(ctx, id, "left")
	}()
	<-started
	cancel()
	source.lifecycleMu.Unlock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Step() error = %v, want context.Canceled", err)
	}
	if diagnostics, writes, _, _ := session.stats(); diagnostics != 0 || writes != 0 {
		t.Fatalf("canceled request diagnostics/writes = %d/%d", diagnostics, writes)
	}
}

func TestReadFramesEndingBeforeStepAcceptanceRejectsWithReadyPath(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	source.lifecycleMu.Lock()
	session.reads <- fakeReadResult{err: io.EOF}
	done := make(chan struct{})
	go source.readFrames(run, done)
	waitForEndingSession(t, source, run)
	if !path.Ready() {
		t.Fatal("RTSP path should still be ready while cleanup waits")
	}
	stepStarted := make(chan struct{})
	stepResult := make(chan error, 1)
	go func() {
		close(stepStarted)
		stepResult <- provider.Step(context.Background(), id, "left")
	}()
	<-stepStarted
	source.lifecycleMu.Unlock()
	if err := <-stepResult; !errors.Is(err, ErrCameraUnavailable) {
		t.Fatalf("Step() error = %v, want ErrCameraUnavailable", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readFrames cleanup did not finish")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 0 || writes != 0 || closes != 1 || path.closeCountValue() != 1 {
		t.Fatalf("ending-run diagnostics/writes/session closes/path closes = %d/%d/%d/%d", diagnostics, writes, closes, path.closeCountValue())
	}
}

func TestAcceptedProviderStepFinishesBeforeReadFramesCleanup(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	release := make(chan struct{})
	session.releaseDiagnostic = release
	stepResult := make(chan error, 1)
	go func() { stepResult <- provider.Step(context.Background(), id, "left") }()
	select {
	case <-session.diagnosticEntered:
	case <-time.After(time.Second):
		t.Fatal("Step did not accept and enter the diagnostic call")
	}
	session.reads <- fakeReadResult{err: io.EOF}
	done := make(chan struct{})
	go source.readFrames(run, done)
	waitForEndingSession(t, source, run)
	if _, _, closes, _ := session.stats(); closes != 0 {
		t.Fatal("reader closed the session before the accepted Step released lifecycleMu")
	}
	close(release)
	if err := <-stepResult; err != nil {
		t.Fatalf("accepted Step() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readFrames cleanup did not finish after the accepted Step")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 1 || writes != 1 || closes != 1 || path.closeCountValue() != 1 {
		t.Fatalf("accepted-run diagnostics/writes/session closes/path closes = %d/%d/%d/%d", diagnostics, writes, closes, path.closeCountValue())
	}
}

func TestProviderCloseWaitsForAcceptedStep(t *testing.T) {
	provider, id, source, run, session, path := newProviderStepFixture(true, true)
	release := make(chan struct{})
	session.releaseDiagnostic = release
	stepResult := make(chan error, 1)
	go func() { stepResult <- provider.Step(context.Background(), id, "left") }()
	select {
	case <-session.diagnosticEntered:
	case <-time.After(time.Second):
		t.Fatal("Step did not enter diagnostic call")
	}
	closeDone := make(chan struct{})
	go func() { provider.Close(); close(closeDone) }()
	waitForEndingSession(t, source, run)
	if _, _, closes, _ := session.stats(); closes != 0 {
		t.Fatal("Provider.Close closed the session while an accepted Step was running")
	}
	close(release)
	if err := <-stepResult; err != nil {
		t.Fatalf("accepted Step() error = %v", err)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Provider.Close did not finish after the accepted Step")
	}
	if diagnostics, writes, closes, _ := session.stats(); diagnostics != 1 || writes != 1 || closes != 1 || path.closeCountValue() != 1 {
		t.Fatalf("close-run diagnostics/writes/session closes/path closes = %d/%d/%d/%d", diagnostics, writes, closes, path.closeCountValue())
	}
}

func TestProviderCloseRejectsSecondCameraStepWhileWaitingForFirst(t *testing.T) {
	provider, firstID, _, _, firstSession, _ := newProviderStepFixture(true, true)
	_, secondID, secondSource, secondRun, secondSession, secondPath := newProviderStepFixtureForID("xiaomi_1111111111111111", true, true)
	secondSource.provider = provider
	provider.mu.Lock()
	provider.sources[secondID] = secondSource
	provider.mu.Unlock()
	secondSource.mu.Lock()
	secondSourceLocked := true
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirstStep := func() { releaseFirstOnce.Do(func() { close(releaseFirst) }) }
	defer func() {
		if secondSourceLocked {
			secondSource.mu.Unlock()
		}
		releaseFirstStep()
	}()

	firstSession.releaseDiagnostic = releaseFirst
	firstResult := make(chan error, 1)
	go func() { firstResult <- provider.Step(context.Background(), firstID, "left") }()
	select {
	case <-firstSession.diagnosticEntered:
	case <-time.After(time.Second):
		t.Fatal("first Step did not reach the diagnostic barrier")
	}

	closeDone := make(chan struct{})
	closeStarted := make(chan struct{})
	go func() {
		close(closeStarted)
		provider.Close()
		close(closeDone)
	}()
	<-closeStarted
	waitForProviderClosed(t, provider)
	if secondSource.closed || secondSource.session != secondRun || secondRun.isEnding() || !secondPath.Ready() {
		t.Fatal("second camera must remain ready and unmarked while its source lock is held")
	}
	secondResult := make(chan error, 1)
	go func() { secondResult <- provider.Step(context.Background(), secondID, "left") }()
	select {
	case err := <-secondResult:
		if !errors.Is(err, ErrCameraUnavailable) {
			t.Fatalf("second camera Step() error = %v, want ErrCameraUnavailable after provider close began", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Step did not reject at p.closed while the second source lock was held")
	}
	if diagnostics, writes, _, _ := secondSession.stats(); diagnostics != 0 || writes != 0 {
		t.Fatalf("second camera diagnostics/writes = %d/%d while close waits", diagnostics, writes)
	}
	select {
	case <-closeDone:
		t.Fatal("Provider.Close finished before the already accepted first Step")
	default:
	}
	if _, _, closes, _ := firstSession.stats(); closes != 0 {
		t.Fatal("Provider.Close closed the first session before its accepted Step completed")
	}

	secondSource.mu.Unlock()
	secondSourceLocked = false
	releaseFirstStep()
	if err := <-firstResult; err != nil {
		t.Fatalf("accepted first Step() error = %v", err)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Provider.Close did not finish after the accepted first Step")
	}
	if diagnostics, writes, closes, _ := firstSession.stats(); diagnostics != 1 || writes != 1 || closes != 1 {
		t.Fatalf("first camera diagnostics/writes/closes = %d/%d/%d", diagnostics, writes, closes)
	}
	if diagnostics, writes, closes, _ := secondSession.stats(); diagnostics != 0 || writes != 0 || closes != 1 {
		t.Fatalf("second camera diagnostics/writes/closes = %d/%d/%d", diagnostics, writes, closes)
	}
}

func waitForProviderClosed(t *testing.T, provider *Provider) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		provider.mu.RLock()
		closed := provider.closed
		provider.mu.RUnlock()
		if closed {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("Provider.Close did not publish the global closed state")
		}
	}
}

func waitForEndingSession(t *testing.T, source *cameraSource, run *cameraSessionRun) {
	t.Helper()
	select {
	case <-run.ending:
	case <-time.After(time.Second):
		t.Fatal("session was not marked as ending")
	}
	source.mu.Lock()
	current := source.session == run && run.isEnding()
	source.mu.Unlock()
	if !current {
		t.Fatal("expected the ending run to remain current while cleanup waits")
	}
}
