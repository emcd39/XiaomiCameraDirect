// Package profiles contains explicit, model-scoped protocol and feature
// declarations. Profiles do not carry device IDs, addresses, or credentials.
package profiles

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const ModelC201 = "xiaomi.camera.c201"

const C201ID = "xiaomi-c201-cs2-miss-v1"

type Feature string

const (
	FeatureVideo              Feature = "video"
	FeatureSnapshot           Feature = "snapshot"
	FeaturePTZ                Feature = "ptz"
	FeatureSettingsRead       Feature = "camera_settings_read"
	FeatureSessionDiagnostics Feature = "session_diagnostics"
)

var (
	ErrInvalidProfile   = errors.New("invalid camera profile")
	ErrProfileConflict  = errors.New("conflicting camera profiles")
	ErrProfileAmbiguous = errors.New("camera profile match is ambiguous")
	ErrNoProfile        = errors.New("no camera profile matches model")
)

// Profile describes protocol defaults and separates behavior already present
// in the product from features that require a verified firmware/region match.
type Profile struct {
	ID                  string
	Revision            uint32
	ModelID             string
	Regions             []string
	FirmwareVersions    []string
	Vendor              string
	ApplicationProtocol string
	MediaProtocol       string
	VideoCodec          string
	Channel             string
	Quality             string
	Audio               bool
	Experimental        bool
	// AutoSelect preserves only an explicitly chosen legacy default. New model
	// profiles remain unselected unless they opt in deliberately.
	AutoSelect       bool
	BaselineFeatures []Feature
	VerifiedFeatures []Feature
}

type Match struct {
	Profile       Profile
	FirmwareKnown bool
	RegionKnown   bool
}

func (m Match) Supports(feature Feature) bool {
	if slices.Contains(m.Profile.BaselineFeatures, feature) {
		return true
	}
	return m.FirmwareKnown && m.RegionKnown && slices.Contains(m.Profile.VerifiedFeatures, feature)
}

type Registry struct {
	profiles []Profile
}

func NewRegistry(profiles ...Profile) (*Registry, error) {
	if len(profiles) == 0 {
		return nil, ErrInvalidProfile
	}
	cloned := make([]Profile, len(profiles))
	ids := make(map[string]struct{}, len(profiles))
	for i, profile := range profiles {
		if err := validate(profile); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidProfile, profile.ID)
		}
		if _, exists := ids[profile.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate profile ID", ErrProfileConflict)
		}
		ids[profile.ID] = struct{}{}
		cloned[i] = clone(profile)
	}
	for i := range cloned {
		for j := i + 1; j < len(cloned); j++ {
			if overlaps(cloned[i], cloned[j]) {
				return nil, fmt.Errorf("%w: %s and %s", ErrProfileConflict, cloned[i].ID, cloned[j].ID)
			}
		}
	}
	return &Registry{profiles: cloned}, nil
}

// Resolve prefers a profile whose firmware and applicable region both match.
// Exact region entries take precedence over a wildcard profile for the same
// firmware. Missing or unknown data may select a unique safe fallback, but
// only baseline features remain enabled until firmware and region are verified.
func (r *Registry) Resolve(modelID, firmware, region string) (Match, error) {
	if r == nil {
		return Match{}, ErrNoProfile
	}
	modelID = strings.TrimSpace(modelID)
	firmware = strings.TrimSpace(firmware)
	region = strings.ToLower(strings.TrimSpace(region))
	var candidates []Profile
	for _, profile := range r.profiles {
		if profile.ModelID == modelID {
			candidates = append(candidates, profile)
		}
	}
	if len(candidates) == 0 {
		return Match{}, ErrNoProfile
	}

	var regional, wildcard []Profile
	for _, profile := range candidates {
		if region != "" && region != "*" && slices.Contains(profile.Regions, region) {
			regional = append(regional, profile)
		} else if slices.Contains(profile.Regions, "*") {
			wildcard = append(wildcard, profile)
		}
	}

	if firmware != "" {
		var regionalFirmware, wildcardFirmware, unspecifiedRegionFirmware []Profile
		for _, profile := range candidates {
			if !slices.Contains(profile.FirmwareVersions, firmware) {
				continue
			}
			switch {
			case region == "":
				unspecifiedRegionFirmware = append(unspecifiedRegionFirmware, profile)
			case region != "*" && slices.Contains(profile.Regions, region):
				regionalFirmware = append(regionalFirmware, profile)
			case slices.Contains(profile.Regions, "*"):
				wildcardFirmware = append(wildcardFirmware, profile)
			}
		}
		if len(regionalFirmware) > 0 {
			return uniqueMatch(regionalFirmware, true, true, modelID)
		}
		if len(wildcardFirmware) > 0 {
			return uniqueMatch(wildcardFirmware, true, false, modelID)
		}
		if region == "" && len(unspecifiedRegionFirmware) > 0 {
			return uniqueMatch(unspecifiedRegionFirmware, true, false, modelID)
		}
	}

	// Preserve the existing model behavior as a baseline when firmware is
	// missing/unknown. Prefer an exact region fallback, then a unique wildcard,
	// and only then a unique model fallback with no region verification.
	if len(regional) > 0 {
		return uniqueMatch(regional, false, true, modelID)
	}
	if len(wildcard) > 0 {
		return uniqueMatch(wildcard, false, false, modelID)
	}
	return uniqueMatch(candidates, false, false, modelID)
}

func uniqueMatch(candidates []Profile, firmwareKnown, regionKnown bool, modelID string) (Match, error) {
	if len(candidates) != 1 {
		return Match{}, fmt.Errorf("%w: %s", ErrProfileAmbiguous, modelID)
	}
	return Match{Profile: clone(candidates[0]), FirmwareKnown: firmwareKnown, RegionKnown: regionKnown}, nil
}

func C201() Profile {
	return Profile{
		ID: C201ID, Revision: 1, ModelID: ModelC201,
		Regions: []string{"de"}, FirmwareVersions: []string{"5.3.2_0016"},
		Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		VideoCodec: "H265", Channel: "0", Quality: "auto", Audio: false,
		AutoSelect:       true,
		BaselineFeatures: []Feature{FeatureVideo, FeatureSnapshot, FeaturePTZ, FeatureSettingsRead, FeatureSessionDiagnostics},
	}
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(C201())
	if err != nil {
		panic(err)
	}
	return registry
}

func validate(profile Profile) error {
	if profile.Experimental && profile.AutoSelect {
		return ErrInvalidProfile
	}
	for _, value := range []string{profile.ID, profile.ModelID, profile.Vendor, profile.ApplicationProtocol, profile.MediaProtocol, profile.VideoCodec, profile.Channel, profile.Quality} {
		if value == "" || strings.TrimSpace(value) != value {
			return ErrInvalidProfile
		}
	}
	if profile.Revision == 0 ||
		len(profile.Regions) == 0 || len(profile.FirmwareVersions) == 0 ||
		len(profile.BaselineFeatures)+len(profile.VerifiedFeatures) == 0 {
		return ErrInvalidProfile
	}
	regions := make(map[string]struct{}, len(profile.Regions))
	for _, region := range profile.Regions {
		if region == "" || strings.TrimSpace(region) != region || strings.ToLower(region) != region {
			return ErrInvalidProfile
		}
		if _, duplicate := regions[region]; duplicate {
			return ErrInvalidProfile
		}
		regions[region] = struct{}{}
	}
	if _, wildcard := regions["*"]; wildcard && len(regions) != 1 {
		return ErrInvalidProfile
	}
	firmwareVersions := make(map[string]struct{}, len(profile.FirmwareVersions))
	for _, firmware := range profile.FirmwareVersions {
		if firmware == "" || strings.TrimSpace(firmware) != firmware || firmware == "*" {
			return ErrInvalidProfile
		}
		if _, duplicate := firmwareVersions[firmware]; duplicate {
			return ErrInvalidProfile
		}
		firmwareVersions[firmware] = struct{}{}
	}
	features := make(map[Feature]struct{}, len(profile.BaselineFeatures)+len(profile.VerifiedFeatures))
	for _, feature := range append(append([]Feature(nil), profile.BaselineFeatures...), profile.VerifiedFeatures...) {
		if feature == "" || strings.TrimSpace(string(feature)) != string(feature) {
			return ErrInvalidProfile
		}
		if _, duplicate := features[feature]; duplicate {
			return ErrInvalidProfile
		}
		features[feature] = struct{}{}
	}
	return nil
}

func overlaps(a, b Profile) bool {
	if a.ModelID != b.ModelID || !intersects(a.FirmwareVersions, b.FirmwareVersions, false) {
		return false
	}
	for _, left := range a.Regions {
		for _, right := range b.Regions {
			// A concrete regional profile is a deterministic override for a
			// wildcard fallback, so that pair is intentional rather than a
			// conflict. Equal concrete regions or two wildcard profiles overlap.
			if left == right {
				return true
			}
		}
	}
	return false
}

func intersects(a, b []string, wildcard bool) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right || wildcard && (left == "*" || right == "*") {
				return true
			}
		}
	}
	return false
}

func clone(profile Profile) Profile {
	profile.Regions = append([]string(nil), profile.Regions...)
	profile.FirmwareVersions = append([]string(nil), profile.FirmwareVersions...)
	profile.BaselineFeatures = append([]Feature(nil), profile.BaselineFeatures...)
	profile.VerifiedFeatures = append([]Feature(nil), profile.VerifiedFeatures...)
	return profile
}
