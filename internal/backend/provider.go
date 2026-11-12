// Package backend defines provider-independent interfaces between the API and
// camera transports.
package backend

import (
	"context"
	"errors"
	"net/url"
	"strings"

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

// SnapshotProvider returns one decoded JPEG for the Home Assistant camera
// image endpoint. A provider can omit snapshots while still supporting RTSP.
type SnapshotProvider interface {
	Snapshot(context.Context, model.CameraID) ([]byte, error)
}

// PTZProvider is optional. A video-only provider only needs to implement
// CameraProvider and should report PTZ as unsupported for its cameras.
type PTZProvider interface {
	Step(context.Context, model.CameraID, string) error
	Stop(context.Context, model.CameraID) error
	Preset(context.Context, model.CameraID, string) error
}
