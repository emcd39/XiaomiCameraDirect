package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestC201MissingOrUnknownFirmwareKeepsOnlyExistingBaseline(t *testing.T) {
	registry := DefaultRegistry()
	for _, firmware := range []string{"", "5.3.2_9999"} {
		match, err := registry.Resolve(ModelC201, firmware, "de")
		if err != nil {
			t.Fatalf("Resolve firmware %q: %v", firmware, err)
		}
		if match.Profile.ID != C201ID || match.FirmwareKnown || !match.RegionKnown {
			t.Fatalf("match for firmware %q = %#v", firmware, match)
		}
		for _, feature := range []Feature{FeatureVideo, FeatureSnapshot, FeaturePTZ, FeatureSettingsRead, FeatureStorageRead, FeatureSessionDiagnostics} {
			if !match.Supports(feature) {
				t.Errorf("C201 baseline feature %q was lost for firmware %q", feature, firmware)
			}
		}
		if match.Supports(Feature("audio")) {
			t.Errorf("unknown firmware %q acquired an unverified feature", firmware)
		}
	}
}

func TestC201ExactFirmwareAndRegionCanEnableOnlyExplicitVerifiedFeatures(t *testing.T) {
	profile := C201()
	profile.VerifiedFeatures = []Feature{"experimental_profile_feature"}
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	match, err := registry.Resolve(ModelC201, "5.3.2_0016", "de")
	if err != nil {
		t.Fatal(err)
	}
	if !match.FirmwareKnown || !match.RegionKnown || !match.Supports("experimental_profile_feature") {
		t.Fatalf("verified match did not enable explicitly verified feature: %#v", match)
	}
	match, err = registry.Resolve(ModelC201, "5.3.2_0016", "us")
	if err != nil {
		t.Fatal(err)
	}
	if match.RegionKnown || match.Supports("experimental_profile_feature") {
		t.Fatalf("region mismatch enabled a gated feature: %#v", match)
	}
}

func TestRegistryRejectsConflictingAndAmbiguousProfiles(t *testing.T) {
	one := C201()
	duplicate := C201()
	duplicate.ID = "duplicate-profile"
	if _, err := NewRegistry(one, duplicate); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("overlapping profiles error = %v, want conflict", err)
	}

	first := C201()
	first.FirmwareVersions = []string{"5.3.2_0016"}
	second := C201()
	second.ID = "c201-new-revision"
	second.FirmwareVersions = []string{"5.3.2_0017"}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatalf("disjoint firmware profiles rejected: %v", err)
	}
	if _, err := registry.Resolve(ModelC201, "", "de"); !errors.Is(err, ErrProfileAmbiguous) {
		t.Fatalf("missing-firmware profile resolution error = %v, want ambiguity", err)
	}
	if _, err := registry.Resolve(ModelC201, "5.3.2_0017", "de"); err != nil {
		t.Fatalf("exact firmware did not select unique profile: %v", err)
	}
}

func TestRegistryRejectsFeaturePresentInBaselineAndVerifiedSets(t *testing.T) {
	profile := C201()
	profile.VerifiedFeatures = []Feature{FeatureVideo}
	if _, err := NewRegistry(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("duplicate feature across trust levels error = %v, want invalid profile", err)
	}
}

func TestExperimentalProfileCannotAutoSelectCamera(t *testing.T) {
	profile := C201()
	profile.ID = "experimental-c201-profile"
	profile.Experimental = true
	if _, err := NewRegistry(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("experimental auto-select profile error = %v, want invalid profile", err)
	}
}

func TestObservedCandidateProfilesRequireTestModeAndKeepFirmwareUnverified(t *testing.T) {
	for _, test := range []struct {
		profile   Profile
		quality   string
		transport string
		ptz       bool
	}{
		{profile: C400Experimental(), quality: "3", ptz: true},
		{profile: Mi360Experimental(), quality: "2", transport: "udp", ptz: true},
		{profile: AW300Experimental(), quality: "auto"},
	} {
		profile := test.profile
		registry, err := NewRegistry(profile)
		if err != nil {
			t.Fatalf("NewRegistry(%s): %v", profile.ID, err)
		}
		match, err := registry.Resolve(profile.ModelID, "", "de")
		if err != nil {
			t.Fatalf("Resolve(%s): %v", profile.ID, err)
		}
		if !match.Profile.Experimental || match.FirmwareKnown || !match.RegionKnown || match.Profile.AutoSelect {
			t.Errorf("candidate profile trust state = %#v", match)
		}
		if !match.Supports(FeatureVideo) || match.Supports(FeatureSnapshot) || match.Supports(FeaturePTZ) != test.ptz || match.Profile.HasPTZMapping() != test.ptz || match.Supports(FeatureSettingsRead) {
			t.Errorf("candidate profile granted untested features: %#v", match.Profile)
		}
		if match.Profile.Vendor != "cs2" || match.Profile.ApplicationProtocol != "MISS" || match.Profile.MediaProtocol != "CS2" || match.Profile.Transport != test.transport || match.Profile.VideoCodec != "H265" || match.Profile.Channel != "0" || match.Profile.Quality != test.quality || match.Profile.Audio || match.Profile.AutoSelect {
			t.Errorf("candidate stream profile differs from the live read-only test: %#v", match.Profile)
		}
	}
	unknownFirmware := C400Experimental()
	knownFirmware := C400Experimental()
	knownFirmware.ID = "xiaomi-c400-known-firmware-test"
	knownFirmware.FirmwareVersions = []string{"test-fw"}
	if _, err := NewRegistry(unknownFirmware, knownFirmware); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("unversioned experimental profile overlap error = %v, want conflict", err)
	}
}

func TestProfileRejectsUnknownMediaTransport(t *testing.T) {
	profile := C400Experimental()
	profile.Transport = "sctp"
	if _, err := NewRegistry(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("invalid media transport error = %v, want ErrInvalidProfile", err)
	}
}

func TestWildcardRegionDoesNotCountAsVerifiedRegion(t *testing.T) {
	profile := C201()
	profile.Regions = []string{"*"}
	profile.VerifiedFeatures = []Feature{"region_gated_feature"}
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"us", "*"} {
		match, err := registry.Resolve(ModelC201, "5.3.2_0016", region)
		if err != nil {
			t.Fatalf("Resolve(region %q): %v", region, err)
		}
		if !match.FirmwareKnown || match.RegionKnown || match.Supports("region_gated_feature") {
			t.Fatalf("wildcard region %q improperly verified a region-specific feature: %#v", region, match)
		}
	}
}

func TestResolveCombinesFirmwareAndRegionBeforeSelectingFallback(t *testing.T) {
	regional := C201()
	regional.ID = "c201-de-fw-a"
	regional.FirmwareVersions = []string{"firmware-a"}
	regional.VerifiedFeatures = []Feature{"de_firmware_feature"}
	wildcard := C201()
	wildcard.ID = "c201-any-fw-b"
	wildcard.Regions = []string{"*"}
	wildcard.FirmwareVersions = []string{"firmware-b"}
	wildcard.VerifiedFeatures = []Feature{"wildcard_firmware_feature"}
	registry, err := NewRegistry(regional, wildcard)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	match, err := registry.Resolve(ModelC201, "firmware-b", "de")
	if err != nil {
		t.Fatalf("Resolve(de, firmware-b): %v", err)
	}
	if match.Profile.ID != wildcard.ID || !match.FirmwareKnown || match.RegionKnown {
		t.Fatalf("de/firmware-b selected %#v; want wildcard firmware-b with unverified region", match)
	}
	if match.Supports("wildcard_firmware_feature") || match.Supports("de_firmware_feature") {
		t.Fatalf("wildcard profile enabled region-gated features: %#v", match)
	}

	match, err = registry.Resolve(ModelC201, "firmware-a", "de")
	if err != nil {
		t.Fatalf("Resolve(de, firmware-a): %v", err)
	}
	if match.Profile.ID != regional.ID || !match.FirmwareKnown || !match.RegionKnown || !match.Supports("de_firmware_feature") {
		t.Fatalf("exact regional profile was not preferred: %#v", match)
	}

	match, err = registry.Resolve(ModelC201, "", "de")
	if err != nil {
		t.Fatalf("Resolve(de, missing firmware): %v", err)
	}
	if match.Profile.ID != regional.ID || match.FirmwareKnown || !match.RegionKnown || match.Supports("de_firmware_feature") {
		t.Fatalf("missing firmware did not keep safe regional baseline: %#v", match)
	}
}

func TestSpecificRegionOverridesWildcardForSameFirmware(t *testing.T) {
	regional := C201()
	regional.ID = "c201-de-specific"
	regional.VerifiedFeatures = []Feature{"de_specific_feature"}
	wildcard := C201()
	wildcard.ID = "c201-wildcard-fallback"
	wildcard.Regions = []string{"*"}
	wildcard.VerifiedFeatures = []Feature{"wildcard_feature"}
	registry, err := NewRegistry(regional, wildcard)
	if err != nil {
		t.Fatalf("NewRegistry() rejected a regional override plus wildcard fallback: %v", err)
	}

	match, err := registry.Resolve(ModelC201, "5.3.2_0016", "de")
	if err != nil {
		t.Fatal(err)
	}
	if match.Profile.ID != regional.ID || !match.RegionKnown || !match.Supports("de_specific_feature") {
		t.Fatalf("specific region did not override wildcard: %#v", match)
	}
	match, err = registry.Resolve(ModelC201, "5.3.2_0016", "us")
	if err != nil {
		t.Fatal(err)
	}
	if match.Profile.ID != wildcard.ID || match.RegionKnown || match.Supports("wildcard_feature") {
		t.Fatalf("wildcard fallback was not kept region-unverified: %#v", match)
	}
}

func TestUnknownModelHasNoProfile(t *testing.T) {
	if _, err := DefaultRegistry().Resolve("xiaomi.camera.unknown", "", "de"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("unknown model error = %v, want no profile", err)
	}
}

func TestLoadFileAcceptsUnknownExperimentalModelWithExplicitAlternativePTZMap(t *testing.T) {
	const modelID = "vendor.camera.future-ptz"
	path := filepath.Join(t.TempDir(), "profiles.json")
	contents := `{"profiles":[{"id":"future-ptz-test-v1","revision":1,"model_id":"` + modelID + `","regions":["de"],"firmware_versions":[],"vendor":"cs2","application_protocol":"MISS","media_protocol":"CS2","transport":"tcp","video_codec":"H265","channel":"0","quality":"3","audio":false,"experimental":true,"baseline_features":["video","ptz"],"ptz_control":{"protocol":"miss-0x112-operation","operations":{"left":4,"right":3,"up":2,"down":1}}}]}`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadFile(DefaultRegistry(), path)
	if err != nil {
		t.Fatalf("LoadFile() rejected a valid local profile: %v", err)
	}
	match, err := registry.Resolve(modelID, "", "de")
	if err != nil {
		t.Fatalf("Resolve() did not find local model: %v", err)
	}
	if !match.Profile.Experimental || match.FirmwareKnown || !match.RegionKnown || !match.Supports(FeatureVideo) || !match.Supports(FeaturePTZ) {
		t.Fatalf("local profile trust/capability state = %#v", match)
	}
	operation, ok := match.Profile.PTZOperation("left")
	if !ok || operation != 4 || operation == 1 {
		t.Fatalf("custom left operation = %d, %t; want operation 4 distinct from C201", operation, ok)
	}
}

func TestLoadFileRejectsPTZProfilesOutsideTheProviderSessionStack(t *testing.T) {
	const valid = `{"profiles":[{"id":"future-ptz-test-v1","revision":1,"model_id":"vendor.camera.future-ptz","regions":["de"],"firmware_versions":[],"vendor":"cs2","application_protocol":"MISS","media_protocol":"CS2","video_codec":"H265","channel":"0","quality":"3","audio":false,"experimental":true,"baseline_features":["video","ptz"],"ptz_control":{"protocol":"miss-0x112-operation","operations":{"left":4,"right":3,"up":2,"down":1}}}]}`
	cases := []struct {
		name string
		from string
		to   string
	}{
		{name: "vendor", from: `"vendor":"cs2"`, to: `"vendor":"other"`},
		{name: "application protocol", from: `"application_protocol":"MISS"`, to: `"application_protocol":"RTSP"`},
		{name: "media protocol", from: `"media_protocol":"CS2"`, to: `"media_protocol":"RTP"`},
		{name: "codec", from: `"video_codec":"H265"`, to: `"video_codec":"H264"`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profiles.json")
			contents := strings.Replace(valid, test.from, test.to, 1)
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(DefaultRegistry(), path); !errors.Is(err, ErrPTZSessionStack) {
				t.Fatalf("LoadFile error = %v, want ErrPTZSessionStack", err)
			}
		})
	}

	profile := C400Experimental()
	profile.MediaProtocol = "RTP"
	if _, err := NewRegistry(profile); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("NewRegistry accepted PTZ on an unsupported media protocol: %v", err)
	}
}

func TestLoadFileRejectsFirmwareRestrictedPTZWithoutInventoryFirmware(t *testing.T) {
	const valid = `{"profiles":[{"id":"future-ptz-test-v1","revision":1,"model_id":"vendor.camera.future-ptz","regions":["de"],"firmware_versions":["1.2.3"],"vendor":"cs2","application_protocol":"MISS","media_protocol":"CS2","video_codec":"H265","channel":"0","quality":"3","audio":false,"experimental":true,"baseline_features":["video","ptz"],"ptz_control":{"protocol":"miss-0x112-operation","operations":{"left":4,"right":3,"up":2,"down":1}}}]}`
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(DefaultRegistry(), path); !errors.Is(err, ErrPTZFirmware) {
		t.Fatalf("LoadFile error = %v, want ErrPTZFirmware", err)
	}
}

func TestLoadFileRejectsVerifiedOnlyPTZWithoutFirmwareIdentity(t *testing.T) {
	const profileJSON = `{"profiles":[{"id":"future-verified-ptz-v1","revision":1,"model_id":"vendor.camera.verified-ptz","regions":["de"],"firmware_versions":[],"vendor":"cs2","application_protocol":"MISS","media_protocol":"CS2","video_codec":"H265","channel":"0","quality":"3","audio":false,"experimental":true,"baseline_features":["video"],"verified_features":["ptz"],"ptz_control":{"protocol":"miss-0x112-operation","operations":{"left":4,"right":3,"up":2,"down":1}}}]}`
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(profileJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(DefaultRegistry(), path); !errors.Is(err, ErrPTZFirmwareIdentity) {
		t.Fatalf("LoadFile error = %v, want ErrPTZFirmwareIdentity", err)
	}
}

func TestFirmwareRestrictedExperimentalPTZBaselineNeedsExactFirmware(t *testing.T) {
	profile := C400Experimental()
	profile.ID = "test-camera-firmware-gated-ptz"
	profile.ModelID = "test.camera.firmware-gated-ptz"
	profile.FirmwareVersions = []string{"1.2.3"}
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		firmware string
		wantPTZ  bool
	}{
		{name: "unknown firmware", firmware: "", wantPTZ: false},
		{name: "different firmware", firmware: "9.9.9", wantPTZ: false},
		{name: "exact firmware", firmware: "1.2.3", wantPTZ: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			match, err := registry.Resolve(profile.ModelID, test.firmware, "de")
			if err != nil {
				t.Fatal(err)
			}
			if got := match.Supports(FeatureVideo); !got {
				t.Fatal("baseline video capability was unexpectedly removed")
			}
			if got := match.Supports(FeaturePTZ); got != test.wantPTZ {
				t.Fatalf("PTZ support = %t, want %t (match=%#v)", got, test.wantPTZ, match)
			}
		})
	}

	match, err := registry.Resolve(profile.ModelID, "1.2.3", "us")
	if err != nil {
		t.Fatal(err)
	}
	if match.Supports(FeaturePTZ) {
		t.Fatalf("firmware match with the wrong region enabled PTZ: %#v", match)
	}
}

func TestC201ProfilePreservesTransportDefaults(t *testing.T) {
	profile := C201()
	if profile.ModelID != ModelC201 || profile.Vendor != "cs2" || profile.ApplicationProtocol != "MISS" || profile.MediaProtocol != "CS2" || profile.VideoCodec != "H265" || profile.Channel != "0" || profile.Quality != "auto" || profile.Audio || !profile.AutoSelect {
		t.Fatalf("C201 protocol defaults changed: %#v", profile)
	}
}
