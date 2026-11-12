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

func TestFeatureSupportEvidenceIsVersionedAndValidated(t *testing.T) {
	camera := Camera{
		ID: "cam_a1b2", Name: "C201",
		Video:   VideoStatus{State: VideoIdle},
		Control: ControlStatus{State: ControlUnsupported},
		Support: &CameraSupport{
			SchemaVersion: 1,
			Features: map[string]FeatureSupport{
				"camera_settings_read": {
					Implementation: "implemented",
					Validation:     "offline",
					Path:           "cloud",
					RequiresCloud:  []string{"settings_read"},
					EvidenceRef:    "LOCAL-CAMERA-PROGRESS.md#evidence-a00",
				},
			},
		},
	}
	if err := camera.Validate(); err != nil {
		t.Fatalf("valid support evidence rejected: %v", err)
	}

	cases := []struct {
		name   string
		change func(*Camera)
	}{
		{"unknown schema", func(c *Camera) { c.Support.SchemaVersion = 2 }},
		{"unknown implementation", func(c *Camera) {
			f := c.Support.Features["camera_settings_read"]
			f.Implementation = "maybe"
			c.Support.Features["camera_settings_read"] = f
		}},
		{"unknown validation", func(c *Camera) {
			f := c.Support.Features["camera_settings_read"]
			f.Validation = "mock_pass_means_hardware"
			c.Support.Features["camera_settings_read"] = f
		}},
		{"unknown path", func(c *Camera) {
			f := c.Support.Features["camera_settings_read"]
			f.Path = "cloud_or_local"
			c.Support.Features["camera_settings_read"] = f
		}},
		{"unsafe evidence reference", func(c *Camera) {
			f := c.Support.Features["camera_settings_read"]
			f.EvidenceRef = "https://example.invalid/?token=secret"
			c.Support.Features["camera_settings_read"] = f
		}},
		{"unsafe firmware label", func(c *Camera) {
			f := c.Support.Features["camera_settings_read"]
			f.TestedFirmware = "5.3.2 secret"
			c.Support.Features["camera_settings_read"] = f
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			copy := camera
			support := *camera.Support
			support.Features = make(map[string]FeatureSupport, len(camera.Support.Features))
			for name, feature := range camera.Support.Features {
				support.Features[name] = feature
			}
			copy.Support = &support
			test.change(&copy)
			if err := copy.Validate(); err == nil {
				t.Fatal("invalid support evidence was accepted")
			}
		})
	}
}
