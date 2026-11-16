// Package backend defines provider-independent interfaces between the API and
// camera transports.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
)

// CameraProvider supplies the selected camera inventory and its latest
// provider-independent video/control state. The returned snapshot is owned by
// the provider and is read-only: consumers must copy before normalization.
// Implementations may share an immutable snapshot between calls, but must be
// safe for concurrent calls and never mutate returned slices, camera values,
// or pointed-to timestamps. Implementations must not expose credentials,
// account IDs, or network addresses in model.Camera.
type CameraProvider interface {
	Cameras(context.Context) ([]model.Camera, error)
}

// StreamEndpoint is an authorized, credential-free local source that a Home
// Assistant camera entity can open. Keep these out of model.Camera so plain
// inventory snapshots never accidentally become stream credentials.
type StreamEndpoint struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	URL      string `json:"url"`
}

func (s StreamEndpoint) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("stream id is required")
	}
	if s.Protocol != "rtsp" {
		return errors.New("unsupported stream protocol")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "rtsp" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("stream URL must be credential-free RTSP")
	}
	return nil
}

// StreamProvider supplies stream sources separately from camera inventory.
// The API returns them only through an authenticated response.
type StreamProvider interface {
	Streams(context.Context, model.CameraID) ([]StreamEndpoint, error)
}

// ReadyStreamProvider waits for a usable RTSP source on explicit demand.
type ReadyStreamProvider interface {
	ReadyStreams(context.Context, model.CameraID) ([]StreamEndpoint, error)
}

// SnapshotProvider returns one decoded JPEG for the Home Assistant camera
// image endpoint. A provider can omit snapshots while still supporting RTSP.
type SnapshotProvider interface {
	Snapshot(context.Context, model.CameraID) ([]byte, error)
}

// SettingValue is one allowlisted, read-only MIoT result. Value is left raw so
// unknown enum values remain visible without being misinterpreted.
type SettingValue struct {
	Code  int             `json:"code"`
	Value json.RawMessage `json:"value,omitempty"`
}

// CameraSettings contains only model-allowlisted, read-only camera properties.
// It must never include device IDs, credentials, or arbitrary client-selected
// MIoT property addresses.
type CameraSettings struct {
	ObservedAt time.Time               `json:"observed_at"`
	Properties map[string]SettingValue `json:"properties"`
}

// CameraSettingsProvider reads a fixed set of model-specific properties.
type CameraSettingsProvider interface {
	CameraSettings(context.Context, model.CameraID) (CameraSettings, error)
}

// StoragePropertyValue is a read-only MIoT result for camera storage. Unit is
// deliberately nullable: until a model and firmware's units are verified, the
// API must not imply bytes, percent, or another interpretation.
type StoragePropertyValue struct {
	Code  int             `json:"code"`
	Value json.RawMessage `json:"value,omitempty"`
	Unit  *string         `json:"unit"`
}

// CameraStorageSnapshot is a raw, model-allowlisted SD status/capacity read.
// Missing properties remain absent and non-zero MIoT codes remain visible.
type CameraStorageSnapshot struct {
	ObservedAt time.Time                       `json:"observed_at"`
	Properties map[string]StoragePropertyValue `json:"properties"`
}

// CameraStorageProvider reads a fixed set of model-specific storage properties.
type CameraStorageProvider interface {
	CameraStorage(context.Context, model.CameraID) (CameraStorageSnapshot, error)
}

// CameraSessionDiagnostics is a metadata-only snapshot of an already-owned
// camera session. It must not create, restart, or probe a media connection.
type CameraSessionDiagnostics struct {
	Operation      CameraDiagnosticOperation `json:"operation"`
	CorrelationID  string                    `json:"correlation_id"`
	Generation     string                    `json:"generation,omitempty"`
	State          string                    `json:"state"`
	Reason         model.ErrorCode           `json:"reason,omitempty"`
	Protocol       string                    `json:"protocol,omitempty"`
	PeerClass      string                    `json:"peer_class,omitempty"`
	Frames         uint64                    `json:"frames,omitempty"`
	Bytes          uint64                    `json:"bytes,omitempty"`
	Dropped        uint64                    `json:"dropped,omitempty"`
	Replies        uint64                    `json:"replies,omitempty"`
	AudioRequested bool                      `json:"audio_requested,omitempty"`
	Events         []CameraDiagnosticEvent   `json:"events"`
}

type CameraDiagnosticOperation string

const (
	DiagnosticSessionStatus     CameraDiagnosticOperation = "session_status"
	DiagnosticTransportMetadata CameraDiagnosticOperation = "transport_metadata"
	MaxCameraDiagnosticEvents                             = 32
)

type CameraDiagnosticRequest struct {
	Operation          CameraDiagnosticOperation
	CorrelationID      string
	ExpectedGeneration string
}

// CameraDiagnosticEvent is a bounded, sanitized event record. It must never
// contain payload bytes, raw errors, camera addresses, or caller supplied text.
type CameraDiagnosticEvent struct {
	Sequence      uint64                    `json:"sequence"`
	ObservedAt    time.Time                 `json:"observed_at"`
	Name          string                    `json:"name"`
	Result        string                    `json:"result"`
	Operation     CameraDiagnosticOperation `json:"operation,omitempty"`
	CorrelationID string                    `json:"correlation_id,omitempty"`
	Generation    string                    `json:"generation,omitempty"`
	State         string                    `json:"state,omitempty"`
	Protocol      string                    `json:"protocol,omitempty"`
	PeerClass     string                    `json:"peer_class,omitempty"`
}

var (
	ErrInvalidCameraDiagnosticOperation = errors.New("invalid camera diagnostic operation")
	ErrStaleCameraSessionGeneration     = errors.New("stale camera session generation")
	ErrNoActiveCameraSession            = errors.New("camera session is not active")
)

const (
	SessionIdle        = "idle"
	SessionStarting    = "starting"
	SessionStreaming   = "streaming"
	SessionEnding      = "ending"
	SessionUnavailable = "unavailable"
	SessionUnsupported = "unsupported"
)

// CameraDiagnosticsProvider reads metadata from an existing camera session.
// It is exposed only through the opt-in, authenticated diagnostics route.
type CameraDiagnosticsProvider interface {
	CameraDiagnostics(context.Context, model.CameraID, CameraDiagnosticRequest) (CameraSessionDiagnostics, error)
}

// PTZProvider is optional. A video-only provider only needs to implement
// CameraProvider and should report PTZ as unsupported for its cameras.
type PTZProvider interface {
	Step(context.Context, model.CameraID, string) error
	Stop(context.Context, model.CameraID) error
	Preset(context.Context, model.CameraID, string) error
}
