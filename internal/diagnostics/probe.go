// Package diagnostics implements a bounded, read-only preflight for a local
// gateway. It intentionally does not create camera sessions or issue controls.
package diagnostics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/backend"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
)

const (
	defaultTimeout  = 8 * time.Second
	maxResponseSize = 1 << 20
	maxCameraCount  = 128
)

var (
	ErrInvalidOptions  = errors.New("invalid diagnostic options")
	ErrUnauthorized    = errors.New("gateway authorization failed")
	ErrUnavailable     = errors.New("gateway is unavailable")
	ErrInvalidResponse = errors.New("gateway returned an invalid diagnostic response")
	ErrCameraNotFound  = errors.New("camera reference was not found")
	ErrUnsafeTarget    = errors.New("gateway target must be a local IP address")
	labelPattern       = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,100}$`)
	featurePattern     = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

type Options struct {
	GatewayURL                string
	Token                     string
	IncludeInventory          bool
	CameraID                  string
	IncludeSessionDiagnostics bool
	DryRun                    bool
}

type Report struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Mode          string         `json:"mode"`
	GatewayTarget string         `json:"gateway_target"`
	Health        string         `json:"health"`
	API           APIReport      `json:"api"`
	Operations    []string       `json:"operations"`
	Cameras       []CameraReport `json:"cameras,omitempty"`
}

type APIReport struct {
	State    string   `json:"state"`
	Version  string   `json:"version,omitempty"`
	Features []string `json:"features,omitempty"`
}

type CameraReport struct {
	Reference               string                            `json:"camera_ref"`
	Model                   string                            `json:"model,omitempty"`
	Firmware                string                            `json:"firmware,omitempty"`
	Selected                bool                              `json:"selected"`
	Capabilities            model.Capabilities                `json:"capabilities"`
	VideoState              string                            `json:"video_state"`
	VideoReason             string                            `json:"video_reason,omitempty"`
	ControlState            string                            `json:"control_state"`
	ControlReason           string                            `json:"control_reason,omitempty"`
	Support                 *model.CameraSupport              `json:"support,omitempty"`
	Streams                 []StreamReport                    `json:"streams,omitempty"`
	SessionDiagnosticsState string                            `json:"session_diagnostics_state,omitempty"`
	Session                 *backend.CameraSessionDiagnostics `json:"session,omitempty"`
}

type StreamReport struct {
	Protocol              string `json:"protocol"`
	DeclaredEndpointClass string `json:"declared_endpoint_class"`
}

type Runner struct {
	client *http.Client
	now    func() time.Time
}

func NewRunner() *Runner {
	return newRunnerWithDialContext(nil)
}

func newRunnerWithDialContext(dialContext func(context.Context, string, string) (net.Conn, error)) *Runner {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
		MaxConnsPerHost:       2,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &Runner{
		client: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

// Collect reads gateway health and API metadata. Camera inventory is queried
// only when IncludeInventory is true because an installed provider may refresh
// the Xiaomi account inventory through the cloud.
func (r *Runner) Collect(ctx context.Context, options Options) (Report, error) {
	baseURL, targetClass, err := validateOptions(options)
	if err != nil {
		return Report{}, err
	}
	if r == nil || r.client == nil || r.now == nil {
		return Report{}, ErrInvalidOptions
	}
	if ctx == nil {
		return Report{}, ErrInvalidOptions
	}
	report := Report{
		SchemaVersion: 1,
		GeneratedAt:   r.now().UTC(),
		Mode:          "read_only",
		GatewayTarget: targetClass,
		API:           APIReport{State: "unknown"},
		Operations:    []string{"GET /healthz", "GET /v2/info"},
	}
	if options.IncludeInventory {
		report.Operations = append(report.Operations, "GET /v2/cameras")
	}
	if options.IncludeSessionDiagnostics {
		report.Operations = append(report.Operations, "GET /v2/cameras/{camera-id}/diagnostics")
	}
	if options.DryRun {
		report.Mode = "dry_run"
		report.Health = "not_checked"
		report.API.State = "not_checked"
		return report, nil
	}

	requestCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	if _, status, callErr := r.get(requestCtx, baseURL+"/healthz", ""); callErr != nil {
		return Report{}, callErr
	} else if status != http.StatusOK {
		return Report{}, ErrUnavailable
	}
	report.Health = "ok"

	infoBody, infoStatus, err := r.get(requestCtx, baseURL+"/v2/info", options.Token)
	if err != nil {
		return Report{}, err
	}
	switch infoStatus {
	case http.StatusOK:
		var info struct {
			APIVersion string   `json:"api_version"`
			Features   []string `json:"features"`
		}
		if decodeBounded(infoBody, &info) != nil {
			return Report{}, ErrInvalidResponse
		}
		report.API = APIReport{State: "available"}
		if labelPattern.MatchString(info.APIVersion) {
			report.API.Version = info.APIVersion
		}
		for _, feature := range info.Features {
			if featurePattern.MatchString(feature) && len(report.API.Features) < 64 {
				report.API.Features = append(report.API.Features, feature)
			}
		}
	case http.StatusNotFound:
		report.API.State = "not_available"
	default:
		return Report{}, ErrUnavailable
	}

	if options.IncludeInventory {
		inventoryBody, status, getErr := r.get(requestCtx, baseURL+"/v2/cameras", options.Token)
		if getErr != nil {
			return Report{}, getErr
		}
		if status != http.StatusOK {
			return Report{}, ErrUnavailable
		}
		cameras, decodeErr := parseInventory(inventoryBody)
		if decodeErr != nil {
			return Report{}, decodeErr
		}
		found := options.CameraID == ""
		for _, camera := range cameras {
			if options.CameraID != "" && string(camera.ID) != options.CameraID {
				continue
			}
			found = true
			cameraReport := summarizeCamera(camera)
			if options.IncludeSessionDiagnostics {
				endpoint := baseURL + "/v2/cameras/" + url.PathEscape(string(camera.ID)) + "/diagnostics"
				body, diagStatus, diagErr := r.get(requestCtx, endpoint, options.Token)
				if diagErr != nil {
					return Report{}, diagErr
				}
				if diagStatus == http.StatusNotFound {
					cameraReport.SessionDiagnosticsState = "disabled"
				} else if diagStatus == http.StatusOK {
					var session backend.CameraSessionDiagnostics
					if decodeBounded(body, &session) != nil {
						return Report{}, ErrInvalidResponse
					}
					cameraReport.SessionDiagnosticsState = "available"
					cameraReport.Session = sanitizeSessionDiagnostics(session)
				} else {
					return Report{}, ErrUnavailable
				}
			}
			report.Cameras = append(report.Cameras, cameraReport)
		}
		if !found {
			return Report{}, ErrCameraNotFound
		}
	}
	return report, nil
}

func validateOptions(options Options) (string, string, error) {
	if (!options.DryRun && strings.TrimSpace(options.Token) == "") ||
		(options.CameraID != "" && !options.IncludeInventory) ||
		(options.IncludeSessionDiagnostics && (!options.IncludeInventory || !validDiagnosticCameraID(options.CameraID))) {
		return "", "", ErrInvalidOptions
	}
	if len(options.Token) > 512 || strings.ContainsAny(options.Token, "\r\n\x00") {
		return "", "", ErrInvalidOptions
	}
	u, err := url.Parse(options.GatewayURL)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", "", ErrInvalidOptions
	}
	targetClass := classifyTarget(u.Hostname())
	if targetClass != "loopback_ip" && targetClass != "private_ip" {
		return "", "", ErrUnsafeTarget
	}
	u.Path = ""
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), targetClass, nil
}

func validDiagnosticCameraID(value string) bool {
	if len(value) != len("xiaomi_")+16 || !strings.HasPrefix(value, "xiaomi_") {
		return false
	}
	for _, char := range value[len("xiaomi_"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func sanitizeSessionDiagnostics(value backend.CameraSessionDiagnostics) *backend.CameraSessionDiagnostics {
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
	return &value
}

func classifyTarget(host string) string {
	address := net.ParseIP(host)
	if address == nil {
		return "hostname_unverified"
	}
	if address.IsLoopback() {
		return "loopback_ip"
	}
	if address.IsPrivate() || address.IsLinkLocalUnicast() {
		return "private_ip"
	}
	return "public_ip"
}

func (r *Runner) get(ctx context.Context, endpoint, token string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, ErrInvalidOptions
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return nil, response.StatusCode, ErrUnauthorized
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return nil, response.StatusCode, ErrInvalidResponse
	}
	if response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound {
		return nil, response.StatusCode, ErrUnavailable
	}
	return data, response.StatusCode, nil
}

func decodeBounded(data []byte, target any) error {
	if len(data) > maxResponseSize {
		return ErrInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidResponse
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalidResponse
	}
	return nil
}

type inventoryEnvelope struct {
	Cameras []json.RawMessage `json:"cameras"`
}

type cameraPayload struct {
	model.Camera
	Support json.RawMessage `json:"support"`
	Streams []struct {
		Protocol string `json:"protocol"`
		URL      string `json:"url"`
	} `json:"streams"`
}

func parseInventory(data []byte) ([]cameraPayload, error) {
	var envelope inventoryEnvelope
	if decodeBounded(data, &envelope) != nil || envelope.Cameras == nil || len(envelope.Cameras) > maxCameraCount {
		return nil, ErrInvalidResponse
	}
	cameras := make([]cameraPayload, 0, len(envelope.Cameras))
	for _, raw := range envelope.Cameras {
		var camera cameraPayload
		if decodeBounded(raw, &camera) != nil {
			return nil, ErrInvalidResponse
		}
		camera.Camera.Support = parseSupport(camera.Support)
		camera.Support = nil
		if camera.Camera.Validate() != nil {
			return nil, ErrInvalidResponse
		}
		cameras = append(cameras, camera)
	}
	return cameras, nil
}

func parseSupport(raw json.RawMessage) *model.CameraSupport {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var support model.CameraSupport
	if json.Unmarshal(raw, &support) != nil {
		return nil
	}
	testCamera := model.Camera{
		ID: "diagnostic", Name: "diagnostic",
		Video:   model.VideoStatus{State: model.VideoIdle},
		Control: model.ControlStatus{State: model.ControlUnsupported},
		Support: &support,
	}
	if testCamera.Validate() != nil {
		return nil
	}
	return &support
}

func summarizeCamera(camera cameraPayload) CameraReport {
	report := CameraReport{
		Reference:     cameraReference(string(camera.ID)),
		Selected:      camera.Selected,
		Capabilities:  camera.Capabilities,
		VideoState:    safeEnum(string(camera.Video.State)),
		VideoReason:   safeReason(string(camera.Video.Reason)),
		ControlState:  safeEnum(string(camera.Control.State)),
		ControlReason: safeReason(string(camera.Control.Reason)),
		Support:       camera.Camera.Support,
		Streams:       make([]StreamReport, 0, len(camera.Streams)),
	}
	if labelPattern.MatchString(camera.ModelID) {
		report.Model = camera.ModelID
	} else if labelPattern.MatchString(camera.Model) {
		report.Model = camera.Model
	}
	if labelPattern.MatchString(camera.Firmware) {
		report.Firmware = camera.Firmware
	}
	for _, stream := range camera.Streams {
		endpointClass := "unknown"
		if parsed, err := url.Parse(stream.URL); err == nil && parsed.Scheme == "rtsp" && parsed.Hostname() != "" {
			endpointClass = classifyTarget(parsed.Hostname())
		}
		report.Streams = append(report.Streams, StreamReport{
			Protocol:              safeProtocol(stream.Protocol),
			DeclaredEndpointClass: endpointClass,
		})
	}
	return report
}

func cameraReference(id string) string {
	digest := sha256.Sum256([]byte(id))
	return "cam_" + hex.EncodeToString(digest[:6])
}

func safeEnum(value string) string {
	if featurePattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func safeReason(value string) string {
	switch model.ErrorCode(value) {
	case "", model.ErrorAuthRequired, model.ErrorWrongRegionOrEmptyAccount,
		model.ErrorUnsupportedModel, model.ErrorUnsupportedTransport,
		model.ErrorLANUnreachable, model.ErrorStreamTimeout,
		model.ErrorCodecUnsupported, model.ErrorBackendUnavailable,
		model.ErrorStorage, model.ErrorAPIIncompatible:
		return value
	}
	return "unknown"
}

func safeProtocol(value string) string {
	if value == "rtsp" {
		return value
	}
	return "unknown"
}
