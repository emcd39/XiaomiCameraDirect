package model

import "testing"

func TestCameraStatusValidation(t *testing.T) {
	base := Camera{
		ID:       "cam_a1b2",
		Name:     "Hallway",
		Selected: true,
		Capabilities: Capabilities{
			Video: true,
			PTZ:   true,
		},
		Video:   VideoStatus{State: VideoStreaming},
		Control: ControlStatus{State: ControlReady},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid camera rejected: %v", err)
	}

	tests := []struct {
		name   string
		change func(*Camera)
	}{
		{"missing id", func(c *Camera) { c.ID = " " }},
		{"missing name", func(c *Camera) { c.Name = "" }},
		{"unknown video state", func(c *Camera) { c.Video.State = "maybe" }},
		{"unknown control state", func(c *Camera) { c.Control.State = "moving" }},
		{"stream without video capability", func(c *Camera) { c.Capabilities.Video = false }},
		{"ready PTZ without capability", func(c *Camera) { c.Capabilities.PTZ = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			camera := base
			test.change(&camera)
			if err := camera.Validate(); err == nil {
				t.Fatal("expected invalid state to be rejected")
			}
		})
	}
}

func TestIdleIsNotStreaming(t *testing.T) {
	camera := Camera{
		ID:       "cam_a1b2",
		Name:     "Hallway",
		Selected: true,
		Video:    VideoStatus{State: VideoIdle},
		Control:  ControlStatus{State: ControlUnsupported},
	}
	if err := camera.Validate(); err != nil {
		t.Fatalf("idle camera rejected: %v", err)
	}
}
