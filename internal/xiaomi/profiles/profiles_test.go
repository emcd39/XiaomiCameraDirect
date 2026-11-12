package profiles

import (
	"errors"
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
		for _, feature := range []Feature{FeatureVideo, FeatureSnapshot, FeaturePTZ, FeatureSettingsRead, FeatureSessionDiagnostics} {
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

func TestWildcardRegionDoesNotCountAsVerifiedRegion(t *testing.T) {
	profile := C201()
	profile.Regions = []string{"*"}
	profile.VerifiedFeatures = []Feature{"region_gated_feature"}
	registry, err := NewRegistry(profile)
	if err != nil {
		t.Fatal(err)
	}
	match, err := registry.Resolve(ModelC201, "5.3.2_0016", "us")
	if err != nil {
		t.Fatal(err)
	}
	if match.RegionKnown || match.Supports("region_gated_feature") {
		t.Fatalf("wildcard region improperly verified region-specific feature: %#v", match)
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

func TestC201ProfilePreservesTransportDefaults(t *testing.T) {
	profile := C201()
	if profile.ModelID != ModelC201 || profile.Vendor != "cs2" || profile.ApplicationProtocol != "MISS" || profile.MediaProtocol != "CS2" || profile.VideoCodec != "H265" || profile.Channel != "0" || profile.Quality != "auto" || profile.Audio || !profile.AutoSelect {
		t.Fatalf("C201 protocol defaults changed: %#v", profile)
	}
}
