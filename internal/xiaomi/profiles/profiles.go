// Package profiles contains explicit, model-scoped protocol and feature
// declarations. Profiles do not carry device IDs, addresses, or credentials.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

const (
	ModelC201  = "xiaomi.camera.c201"
	ModelC400  = "chuangmi.camera.039a04"
	ModelMi360 = "chuangmi.camera.026c02"
	ModelAW300 = "mxiang.camera.mod13"
)

const (
	C201ID      = "xiaomi-c201-cs2-miss-v1"
	C400TestID  = "xiaomi-c400-cs2-test-v1"
	Mi360TestID = "xiaomi-mi360-cs2-test-v1"
	AW300TestID = "xiaomi-aw300-cs2-test-v1"
)

type Feature string

const (
	FeatureVideo              Feature = "video"
	FeatureSnapshot           Feature = "snapshot"
	FeaturePTZ                Feature = "ptz"
	FeatureSettingsRead       Feature = "camera_settings_read"
	FeatureStorageRead        Feature = "sd_capacity"
	FeatureSessionDiagnostics Feature = "session_diagnostics"
)

var (
	ErrInvalidProfile      = errors.New("invalid camera profile")
	ErrProfileConflict     = errors.New("conflicting camera profiles")
	ErrProfileAmbiguous    = errors.New("camera profile match is ambiguous")
	ErrNoProfile           = errors.New("no camera profile matches model")
	ErrPTZSessionStack     = errors.New("local Xiaomi PTZ profile requires the provider-supported cs2/MISS/CS2/H265 session stack")
	ErrPTZFirmware         = errors.New("local Xiaomi PTZ profile cannot restrict firmware because the current camera inventory does not expose firmware")
	ErrPTZFirmwareIdentity = errors.New("local Xiaomi PTZ profile cannot declare PTZ as verified because the camera inventory does not expose firmware identity")
)

// Profile describes protocol defaults and separates behavior already present
// in the product from features that require a verified firmware/region match.
type Profile struct {
	ID       string   `json:"id"`
	Revision uint32   `json:"revision"`
	ModelID  string   `json:"model_id"`
	Regions  []string `json:"regions"`
	// FirmwareVersions is an exact allowlist. It may be empty only for an
	// Experimental profile whose firmware has not been verified.
	FirmwareVersions    []string `json:"firmware_versions"`
	Vendor              string   `json:"vendor"`
	ApplicationProtocol string   `json:"application_protocol"`
	MediaProtocol       string   `json:"media_protocol"`
	// Transport pins the CS2 media transport when a model profile has a
	// verified preference. Empty preserves handshake auto-negotiation.
	Transport    string `json:"transport,omitempty"`
	VideoCodec   string `json:"video_codec"`
	Channel      string `json:"channel"`
	Quality      string `json:"quality"`
	Audio        bool   `json:"audio"`
	Experimental bool   `json:"experimental,omitempty"`
	// AutoSelect preserves only an explicitly chosen legacy default. New model
	// profiles remain unselected unless they opt in deliberately.
	AutoSelect       bool        `json:"auto_select,omitempty"`
	BaselineFeatures []Feature   `json:"baseline_features"`
	VerifiedFeatures []Feature   `json:"verified_features,omitempty"`
	PTZControl       *PTZControl `json:"ptz_control,omitempty"`
}

// PTZControl declares a wire protocol already implemented by the provider and
// a model-specific mapping from UI directions to its bounded operation IDs.
// Externally supplied profiles must declare this explicitly before PTZ appears.
type PTZControl struct {
	Protocol   string         `json:"protocol"`
	Operations map[string]int `json:"operations"`
}

const PTZProtocolMISSOperation = "miss-0x112-operation"

func DefaultMISSOperationPTZ() *PTZControl {
	return &PTZControl{
		Protocol: PTZProtocolMISSOperation,
		Operations: map[string]int{
			"left": 1, "right": 2, "up": 3, "down": 4,
		},
	}
}

func (p Profile) PTZOperation(direction string) (int, bool) {
	control := p.PTZControl
	if control == nil {
		return 0, false
	}
	if control.Protocol != PTZProtocolMISSOperation {
		return 0, false
	}
	operation, ok := control.Operations[direction]
	return operation, ok && operation >= 1 && operation <= 4
}

func (p Profile) HasPTZMapping() bool {
	operations := map[int]bool{}
	for _, direction := range []string{"left", "right", "up", "down"} {
		operation, ok := p.PTZOperation(direction)
		if !ok || operations[operation] {
			return false
		}
		operations[operation] = true
	}
	return true
}

// SupportsManagedSession mirrors the protocol stack accepted by the Xiaomi
// provider's media-session opener. Keep profile validation and capability
// advertising aligned with this check so unusable profiles cannot claim PTZ.
func (p Profile) SupportsManagedSession() bool {
	return p.Vendor == "cs2" && p.ApplicationProtocol == "MISS" && p.MediaProtocol == "CS2" &&
		(p.VideoCodec == "H264" || p.VideoCodec == "H265")
}

func (p Profile) SupportsManagedPTZ() bool {
	return p.SupportsManagedSession() && p.VideoCodec == "H265" && p.HasPTZMapping()
}

type Match struct {
	Profile       Profile
	FirmwareKnown bool
	RegionKnown   bool
}

func (m Match) Supports(feature Feature) bool {
	if slices.Contains(m.Profile.BaselineFeatures, feature) {
		// Experimental PTZ declarations with an exact firmware allowlist are
		// not safe to advertise while the caller cannot prove that firmware.
		// This also protects registries constructed in code that bypass
		// LoadFile's stricter local-profile validation.
		if feature == FeaturePTZ && m.Profile.Experimental && len(m.Profile.FirmwareVersions) > 0 {
			return m.FirmwareKnown && m.RegionKnown
		}
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

// Extend returns a new registry containing the current profiles and additions.
// The original registry remains immutable and all conflicts are revalidated.
func (r *Registry) Extend(additions ...Profile) (*Registry, error) {
	if r == nil {
		return NewRegistry(additions...)
	}
	combined := make([]Profile, 0, len(r.profiles)+len(additions))
	combined = append(combined, r.profiles...)
	combined = append(combined, additions...)
	return NewRegistry(combined...)
}

// ExperimentalModelIDs returns the exact IDs that the account inventory may
// include in addition to its built-in camera-name classification. Providers
// should use this only while experimental profile mode is explicitly enabled.
func (r *Registry) ExperimentalModelIDs() []string {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.profiles))
	seen := make(map[string]struct{}, len(r.profiles))
	for _, profile := range r.profiles {
		if !profile.Experimental {
			continue
		}
		if _, ok := seen[profile.ModelID]; ok {
			continue
		}
		seen[profile.ModelID] = struct{}{}
		ids = append(ids, profile.ModelID)
	}
	slices.Sort(ids)
	return ids
}

// LoadFile adds local model profiles to a built-in registry. Local profiles
// must be explicitly experimental, cannot auto-select a camera, and must
// declare their PTZ wire mapping before those controls are exposed.
func LoadFile(base *Registry, path string) (*Registry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open Xiaomi camera profile file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return nil, errors.New("invalid Xiaomi camera profile file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var document struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := decoder.Decode(&document); err != nil || len(document.Profiles) == 0 {
		return nil, errors.New("invalid Xiaomi camera profile file")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("invalid Xiaomi camera profile file")
	}
	for _, profile := range document.Profiles {
		if !profile.Experimental || profile.AutoSelect {
			return nil, errors.New("local Xiaomi camera profiles must be experimental and not auto-selected")
		}
		if slices.Contains(profile.VerifiedFeatures, FeaturePTZ) && !slices.Contains(profile.BaselineFeatures, FeaturePTZ) {
			return nil, ErrPTZFirmwareIdentity
		}
		if slices.Contains(profile.BaselineFeatures, FeaturePTZ) || slices.Contains(profile.VerifiedFeatures, FeaturePTZ) {
			if !slices.Contains(profile.BaselineFeatures, FeatureVideo) || !validPTZControl(profile.PTZControl) {
				return nil, errors.New("local Xiaomi PTZ profile must include baseline video and a supported control mapping")
			}
			if !profile.SupportsManagedPTZ() {
				return nil, ErrPTZSessionStack
			}
			if len(profile.FirmwareVersions) > 0 {
				return nil, ErrPTZFirmware
			}
		}
	}
	return base.Extend(document.Profiles...)
}

func validPTZControl(control *PTZControl) bool {
	if control == nil || control.Protocol != PTZProtocolMISSOperation || len(control.Operations) != 4 {
		return false
	}
	seen := map[int]bool{}
	for _, direction := range []string{"left", "right", "up", "down"} {
		operation, ok := control.Operations[direction]
		if !ok || operation < 1 || operation > 4 || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}

func C201() Profile {
	return Profile{
		ID: C201ID, Revision: 1, ModelID: ModelC201,
		Regions: []string{"de"}, FirmwareVersions: []string{"5.3.2_0016"},
		Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		VideoCodec: "H265", Channel: "0", Quality: "auto", Audio: false,
		AutoSelect:       true,
		BaselineFeatures: []Feature{FeatureVideo, FeatureSnapshot, FeaturePTZ, FeatureSettingsRead, FeatureStorageRead, FeatureSessionDiagnostics},
		PTZControl:       DefaultMISSOperationPTZ(),
	}
}

// C400Experimental and Mi360Experimental record stream settings observed in
// the lab and the MISS PTZ map reported by independent implementations. Their
// firmware and physical behavior are not verified by this gateway, so they
// remain behind the explicit test-mode switch.
func C400Experimental() Profile {
	return Profile{
		ID: C400TestID, Revision: 2, ModelID: ModelC400,
		Regions: []string{"de"}, Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		VideoCodec: "H265", Channel: "0", Quality: "3", Experimental: true,
		BaselineFeatures: []Feature{FeatureVideo, FeaturePTZ},
		PTZControl:       DefaultMISSOperationPTZ(),
	}
}

func Mi360Experimental() Profile {
	return Profile{
		ID: Mi360TestID, Revision: 1, ModelID: ModelMi360,
		Regions: []string{"de"}, Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		Transport: "udp", VideoCodec: "H265", Channel: "0", Quality: "2", Experimental: true,
		BaselineFeatures: []Feature{FeatureVideo, FeaturePTZ},
		PTZControl:       DefaultMISSOperationPTZ(),
	}
}

func AW300Experimental() Profile {
	return Profile{
		ID: AW300TestID, Revision: 1, ModelID: ModelAW300,
		Regions: []string{"de"}, Vendor: "cs2", ApplicationProtocol: "MISS", MediaProtocol: "CS2",
		VideoCodec: "H265", Channel: "0", Quality: "auto", Experimental: true,
		BaselineFeatures: []Feature{FeatureVideo},
	}
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(C201(), C400Experimental(), Mi360Experimental(), AW300Experimental())
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
		len(profile.Regions) == 0 || (len(profile.FirmwareVersions) == 0 && !profile.Experimental) ||
		len(profile.BaselineFeatures)+len(profile.VerifiedFeatures) == 0 {
		return ErrInvalidProfile
	}
	if profile.Transport != "" && profile.Transport != "tcp" && profile.Transport != "udp" {
		return ErrInvalidProfile
	}
	hasPTZ := slices.Contains(profile.BaselineFeatures, FeaturePTZ) || slices.Contains(profile.VerifiedFeatures, FeaturePTZ)
	hasVideo := slices.Contains(profile.BaselineFeatures, FeatureVideo) || slices.Contains(profile.VerifiedFeatures, FeatureVideo)
	if hasPTZ && (!hasVideo || !validPTZControl(profile.PTZControl) || !profile.SupportsManagedPTZ()) {
		return ErrInvalidProfile
	}
	if profile.PTZControl != nil && !validPTZControl(profile.PTZControl) {
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
	firmwareOverlap := len(a.FirmwareVersions) == 0 || len(b.FirmwareVersions) == 0 || intersects(a.FirmwareVersions, b.FirmwareVersions, false)
	if a.ModelID != b.ModelID || !firmwareOverlap {
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
	if profile.PTZControl != nil {
		control := *profile.PTZControl
		control.Operations = make(map[string]int, len(profile.PTZControl.Operations))
		for direction, operation := range profile.PTZControl.Operations {
			control.Operations[direction] = operation
		}
		profile.PTZControl = &control
	}
	return profile
}
