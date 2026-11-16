// Package model defines provider-independent camera state exposed to Home Assistant.
package model

import (
	"errors"
	"strings"
	"time"
)

// CameraID is an opaque, installation-scoped identifier. It must not contain a
// camera's IP address or expose the Xiaomi account/device identifier.
type CameraID string

type Capabilities struct {
	Video    bool `json:"video"`
	Snapshot bool `json:"snapshot"`
	Audio    bool `json:"audio"`
	PTZ      bool `json:"ptz"`
	Presets  bool `json:"presets"`
}

type VideoState string

const (
	VideoNotSelected  VideoState = "not_selected"
	VideoUnsupported  VideoState = "unsupported"
	VideoIdle         VideoState = "idle"
	VideoConnecting   VideoState = "connecting"
	VideoStreaming    VideoState = "streaming"
	VideoRetrying     VideoState = "retrying"
	VideoOffline      VideoState = "offline"
	VideoAuthRequired VideoState = "auth_required"
	VideoPrivacy      VideoState = "privacy"
	VideoError        VideoState = "error"
)

type ControlState string

const (
	ControlUnsupported  ControlState = "unsupported"
	ControlReady        ControlState = "ready"
	ControlAuthRequired ControlState = "auth_required"
	ControlError        ControlState = "error"
)

type ErrorCode string

const (
	ErrorAuthRequired              ErrorCode = "auth_required"
	ErrorWrongRegionOrEmptyAccount ErrorCode = "wrong_region_or_empty_account"
	ErrorUnsupportedModel          ErrorCode = "unsupported_model"
	ErrorUnsupportedTransport      ErrorCode = "unsupported_transport"
	ErrorUnverifiedFirmware        ErrorCode = "unverified_firmware"
	ErrorChannelUnverified         ErrorCode = "channel_unverified"
	ErrorTimeout                   ErrorCode = "timeout"
	ErrorStale                     ErrorCode = "stale"
	ErrorLANUnreachable            ErrorCode = "lan_unreachable"
	ErrorStreamTimeout             ErrorCode = "stream_timeout"
	ErrorCodecUnsupported          ErrorCode = "codec_unsupported"
	ErrorBackendUnavailable        ErrorCode = "backend_unavailable"
	ErrorStorage                   ErrorCode = "storage_error"
	ErrorAPIIncompatible           ErrorCode = "api_incompatible"
)

type VideoStatus struct {
	State         VideoState   `json:"state"`
	Reason        ErrorCode    `json:"reason,omitempty"`
	SessionActive bool         `json:"session_active,omitempty"`
	SourceFormat  *VideoFormat `json:"source_format,omitempty"`
	LastFrameAt   *time.Time   `json:"last_frame_at,omitempty"`
	UpdatedAt     *time.Time   `json:"updated_at,omitempty"`
}

// VideoFormat describes the source advertised by the active RTSP relay.
type VideoFormat struct {
	Codec  string `json:"codec"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type ControlStatus struct {
	State  ControlState `json:"state"`
	Reason ErrorCode    `json:"reason,omitempty"`
}

// CameraSupport is optional, versioned evidence about implementation and the
// declared path for individual functions. Path describes the implementation
// route known from code; it is not a packet-capture result. Validation and
// EvidenceRef describe the level and source of supporting evidence. It does
// not replace Capabilities: old clients keep using their stable boolean fields.
type CameraSupport struct {
	SchemaVersion int                       `json:"schema_version"`
	Features      map[string]FeatureSupport `json:"features"`
}

// FeatureSupport separates shipping code from evidence collected on hardware
// or in Home Assistant. A successful mock must never imply device support.
type FeatureSupport struct {
	Implementation string    `json:"implementation"`
	Validation     string    `json:"validation"`
	Path           string    `json:"path"`
	Availability   string    `json:"availability,omitempty"`
	Reason         ErrorCode `json:"reason,omitempty"`
	RequiresCloud  []string  `json:"requires_cloud"`
	EvidenceRef    string    `json:"evidence_ref,omitempty"`
	TestedFirmware string    `json:"tested_firmware,omitempty"`
}

// Camera is the public managed-mode view. Provider-specific credentials,
// network addresses and raw Xiaomi identifiers do not belong here.
type Camera struct {
	ID           CameraID       `json:"id"`
	Name         string         `json:"name"`
	Model        string         `json:"model,omitempty"`
	ModelID      string         `json:"model_id,omitempty"`
	Firmware     string         `json:"firmware,omitempty"`
	Selected     bool           `json:"selected"`
	Capabilities Capabilities   `json:"capabilities"`
	Video        VideoStatus    `json:"video"`
	Control      ControlStatus  `json:"control"`
	Support      *CameraSupport `json:"support,omitempty"`
}

func (c Camera) Validate() error {
	if err := c.ValidateCore(); err != nil {
		return err
	}
	return c.ValidateSupport()
}

// ValidateCore validates required camera state without treating optional
// support metadata as a reason to discard a compatible inventory entry.
func (c Camera) ValidateCore() error {
	if strings.TrimSpace(string(c.ID)) == "" {
		return errors.New("camera id is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("camera name is required")
	}
	switch c.Video.State {
	case VideoNotSelected, VideoUnsupported, VideoIdle, VideoConnecting,
		VideoStreaming, VideoRetrying, VideoOffline, VideoAuthRequired,
		VideoPrivacy, VideoError:
	default:
		return errors.New("unknown video state")
	}
	switch c.Control.State {
	case ControlUnsupported, ControlReady, ControlAuthRequired, ControlError:
	default:
		return errors.New("unknown control state")
	}
	if c.Video.State == VideoStreaming && !c.Capabilities.Video {
		return errors.New("streaming camera must support video")
	}
	if c.Control.State == ControlReady && !c.Capabilities.PTZ {
		return errors.New("ready control requires PTZ capability")
	}
	return nil
}

func (c Camera) ValidateSupport() error {
	if c.Support == nil {
		return nil
	}
	if c.Support.SchemaVersion != 1 {
		return errors.New("unknown support schema version")
	}
	if c.Support.Features == nil {
		return errors.New("support feature map is required")
	}
	if len(c.Support.Features) > 32 {
		return errors.New("too many support feature records")
	}
	for name, feature := range c.Support.Features {
		if !validSupportName(name) || !validFeatureSupport(feature) {
			return errors.New("invalid feature support evidence")
		}
	}
	return nil
}

func validSupportName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func validFeatureSupport(feature FeatureSupport) bool {
	switch feature.Implementation {
	case "absent", "research", "implemented":
	default:
		return false
	}
	switch feature.Validation {
	case "none", "offline", "device", "home_assistant":
	default:
		return false
	}
	switch feature.Path {
	case "camera_lan", "cloud", "nvr_local", "unknown":
	default:
		return false
	}
	switch feature.Availability {
	case "", "available", "unavailable", "unknown":
	default:
		return false
	}
	if !validSupportReason(feature.Reason) || (feature.Availability == "available" && feature.Reason != "") || (feature.Availability == "unavailable" && feature.Reason == "") {
		return false
	}
	if len(feature.RequiresCloud) > 8 {
		return false
	}
	for _, requirement := range feature.RequiresCloud {
		if !validSupportName(requirement) {
			return false
		}
	}
	return len(feature.EvidenceRef) <= 160 && validEvidenceRef(feature.EvidenceRef) && len(feature.TestedFirmware) <= 80 && validFirmwareLabel(feature.TestedFirmware)
}

func validSupportReason(reason ErrorCode) bool {
	switch reason {
	case "", ErrorAuthRequired, ErrorWrongRegionOrEmptyAccount, ErrorUnsupportedModel,
		ErrorUnsupportedTransport, ErrorUnverifiedFirmware, ErrorChannelUnverified,
		ErrorTimeout, ErrorStale, ErrorLANUnreachable, ErrorStreamTimeout,
		ErrorCodecUnsupported, ErrorBackendUnavailable, ErrorStorage, ErrorAPIIncompatible:
		return true
	default:
		return false
	}
}

func validEvidenceRef(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != '/' && r != '#' {
			return false
		}
	}
	return true
}

func validFirmwareLabel(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' && r != '+' {
			return false
		}
	}
	return true
}
