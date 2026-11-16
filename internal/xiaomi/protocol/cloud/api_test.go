package cloud

import (
	"reflect"
	"testing"
)

func TestFilterCameraDevicesIncludesOnlyStandardOrExplicitlyProfiledModels(t *testing.T) {
	devices := []*Device{
		{Did: "standard-camera", Model: "vendor.camera.standard"},
		{Did: "standard-doorbell", Model: "vendor.cateye.doorbell"},
		{Did: "profiled-camera", Model: "vendor.future.ptz-box"},
		{Did: "unprofiled-camera", Model: "vendor.unknown.ptz-box"},
		{Did: "appliance", Model: "vendor.appliance.plug"},
		nil,
	}

	got := filterCameraDevices(devices, map[string]struct{}{"vendor.future.ptz-box": {}})
	var ids []string
	for _, device := range got {
		ids = append(ids, device.Did)
	}
	want := []string{"standard-camera", "standard-doorbell", "profiled-camera"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("camera IDs = %v, want %v", ids, want)
	}

	standardOnly := filterCameraDevices(devices, nil)
	ids = ids[:0]
	for _, device := range standardOnly {
		ids = append(ids, device.Did)
	}
	want = []string{"standard-camera", "standard-doorbell"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("default camera IDs = %v, want %v", ids, want)
	}
}
