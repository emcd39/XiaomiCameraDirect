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
	ErrorLANUnreachable            ErrorCode = "lan_unreachable"
	ErrorStreamTimeout             ErrorCode = "stream_timeout"
	ErrorCodecUnsupported          ErrorCode = "codec_unsupported"
	ErrorBackendUnavailable        ErrorCode = "backend_unavailable"
	ErrorStorage                   ErrorCode = "storage_error"
	ErrorAPIIncompatible           ErrorCode = "api_incompatible"
)

type VideoStatus struct {
	State       VideoState `json:"state"`
	Reason      ErrorCode  `json:"reason,omitempty"`
	LastFrameAt *time.Time `json:"last_frame_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type ControlStatus struct {
	State  ControlState `json:"state"`
	Reason ErrorCode    `json:"reason,omitempty"`
}

// Camera is the public managed-mode view. Provider-specific credentials,
// network addresses and raw Xiaomi identifiers do not belong here.
type Camera struct {
	ID           CameraID      `json:"id"`
	Name         string        `json:"name"`
	Model        string        `json:"model,omitempty"`
	ModelID      string        `json:"model_id,omitempty"`
	Firmware     string        `json:"firmware,omitempty"`
	Selected     bool          `json:"selected"`
	Capabilities Capabilities  `json:"capabilities"`
	Video        VideoStatus   `json:"video"`
	Control      ControlStatus `json:"control"`
}

func (c Camera) Validate() error {
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
