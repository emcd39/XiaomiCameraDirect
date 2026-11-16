package backend

import (
	"context"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/model"
)

// ConfigProvider adapts the existing static RTSP/ONVIF mode to the managed
// camera read contract. It keeps the legacy configuration path working while
// Home Assistant moves to capability-aware camera records.
type ConfigProvider struct {
	cameras []config.Camera
}

func NewConfigProvider(cameras []config.Camera) *ConfigProvider {
	copyOfCameras := append([]config.Camera(nil), cameras...)
	return &ConfigProvider{cameras: copyOfCameras}
}

func (p *ConfigProvider) Cameras(context.Context) ([]model.Camera, error) {
	cameras := make([]model.Camera, 0, len(p.cameras))
	for _, configured := range p.cameras {
		camera := model.Camera{
			ID:       model.CameraID(configured.ID),
			Name:     configured.Name,
			Model:    configured.Model,
			Selected: true,
			Capabilities: model.Capabilities{
				Video: true,
				PTZ:   configured.PTZ,
			},
			Video:   model.VideoStatus{State: model.VideoIdle},
			Control: model.ControlStatus{State: model.ControlUnsupported},
		}
		if configured.PTZ {
			camera.Control = model.ControlStatus{State: model.ControlReady}
		}
		cameras = append(cameras, camera)
	}
	return cameras, nil
}

func (p *ConfigProvider) Streams(_ context.Context, id model.CameraID) ([]StreamEndpoint, error) {
	for _, configured := range p.cameras {
		if configured.ID == string(id) {
			return []StreamEndpoint{{ID: "main", Protocol: "rtsp", URL: configured.RTSPURL}}, nil
		}
	}
	return nil, nil
}

// A configured RTSP source needs no session preparation by the gateway.
func (p *ConfigProvider) ReadyStreams(ctx context.Context, id model.CameraID) ([]StreamEndpoint, error) {
	return p.Streams(ctx, id)
}
