// Package store persists managed-mode identity and camera selection.
// Xiaomi credentials and media credentials do not belong in this store.
package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	currentSchemaVersion = 1
	maxStateFileBytes    = 1 << 20
	maxCameraIDBytes     = 256
)

var (
	ErrRevisionConflict  = errors.New("camera selection revision conflict")
	ErrDuplicateCameraID = errors.New("duplicate camera id")
	ErrInvalidCameraID   = errors.New("invalid camera id")
	ErrCorruptState      = errors.New("managed state is corrupt or unsupported")
	ErrInsecureState     = errors.New("managed state file permissions are too broad")
	ErrStateTooLarge     = errors.New("managed state exceeds size limit")
)

// State is the persisted, provider-independent managed state. InstanceID is
// stable for the lifetime of this runtime data directory. Camera IDs are
// opaque values and are not used as file paths.
type State struct {
	SchemaVersion     int      `json:"schema_version"`
	InstanceID        string   `json:"instance_id"`
	SelectionRevision uint64   `json:"selection_revision"`
	SelectedCameraIDs []string `json:"selected_camera_ids"`
}

// Store is safe for concurrent use within one gateway process. A data file
// must have exactly one owning runtime process.
type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

// Open loads an existing store or creates a fresh instance identity. Invalid
// or unsupported state is returned as an error and is never replaced.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("managed state path is required")
	}
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create managed state directory: %w", err)
	}

	state, err := readState(path)
	if err == nil {
		return &Store{path: path, state: state}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	instanceID, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("create instance identity: %w", err)
	}
	state = State{
		SchemaVersion:     currentSchemaVersion,
		InstanceID:        instanceID,
		SelectionRevision: 0,
		SelectedCameraIDs: []string{},
	}
	if err := writeState(path, state); err != nil {
		return nil, err
	}
	return &Store{path: path, state: state}, nil
}

// Snapshot returns a detached copy of the current persisted state.
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneState(s.state)
}

// ReplaceSelection replaces the selected cameras only if expectedRevision
// matches. Repeating the same selection does not increment the revision.
func (s *Store) ReplaceSelection(expectedRevision uint64, cameraIDs []string) (State, error) {
	canonical, err := canonicalCameraIDs(cameraIDs)
	if err != nil {
		return State{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedRevision != s.state.SelectionRevision {
		return cloneState(s.state), ErrRevisionConflict
	}
	if equalIDs(canonical, s.state.SelectedCameraIDs) {
		return cloneState(s.state), nil
	}
	if s.state.SelectionRevision == ^uint64(0) {
		return State{}, errors.New("camera selection revision exhausted")
	}

	updated := cloneState(s.state)
	updated.SelectionRevision++
	updated.SelectedCameraIDs = canonical
	if err := writeState(s.path, updated); err != nil {
		return State{}, err
	}
	s.state = updated
	return cloneState(s.state), nil
}

func readState(path string) (State, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
		return State{}, fmt.Errorf("inspect managed state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return State{}, fmt.Errorf("%w: state path is not a regular file", ErrCorruptState)
	}
	if err := checkStatePermissions(path, info); err != nil {
		return State{}, err
	}
	if info.Size() > maxStateFileBytes {
		return State{}, ErrStateTooLarge
	}

	file, err := os.Open(path)
	if err != nil {
		return State{}, fmt.Errorf("open managed state: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxStateFileBytes+1))
	if err != nil {
		return State{}, fmt.Errorf("read managed state: %w", err)
	}
	if len(data) > maxStateFileBytes {
		return State{}, ErrStateTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("%w: decode state: %v", ErrCorruptState, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return State{}, fmt.Errorf("%w: trailing data", ErrCorruptState)
	}
	if err := validateState(state); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrCorruptState, err)
	}
	return state, nil
}

func writeState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed state: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxStateFileBytes {
		return ErrStateTooLarge
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".managed-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create managed state temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure managed state temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write managed state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync managed state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close managed state: %w", err)
	}
	if err := secureStateFile(tempPath); err != nil {
		return fmt.Errorf("secure managed state file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace managed state: %w", err)
	}
	return nil
}

func newInstanceID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	// UUID v4 version and RFC 4122 variant bits.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return "inst_" + hex.EncodeToString(raw[:]), nil
}

func validateState(state State) error {
	if state.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", state.SchemaVersion)
	}
	if !validInstanceID(state.InstanceID) {
		return errors.New("invalid instance identity")
	}
	if state.SelectedCameraIDs == nil {
		return errors.New("selected camera IDs must be present")
	}
	canonical, err := canonicalCameraIDs(state.SelectedCameraIDs)
	if err != nil {
		return err
	}
	if !equalIDs(canonical, state.SelectedCameraIDs) {
		return errors.New("camera IDs are not in canonical order")
	}
	return nil
}

func validInstanceID(value string) bool {
	if len(value) != len("inst_")+32 || !strings.HasPrefix(value, "inst_") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "inst_"))
	return err == nil && len(decoded) == 16
}

func canonicalCameraIDs(cameraIDs []string) ([]string, error) {
	canonical := make([]string, len(cameraIDs))
	copy(canonical, cameraIDs)
	seen := make(map[string]struct{}, len(canonical))
	for _, cameraID := range canonical {
		if cameraID == "" || strings.TrimSpace(cameraID) != cameraID || len(cameraID) > maxCameraIDBytes {
			return nil, ErrInvalidCameraID
		}
		for _, char := range cameraID {
			if unicode.IsControl(char) {
				return nil, ErrInvalidCameraID
			}
		}
		if _, exists := seen[cameraID]; exists {
			return nil, ErrDuplicateCameraID
		}
		seen[cameraID] = struct{}{}
	}
	sort.Strings(canonical)
	return canonical, nil
}

func equalIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func cloneState(source State) State {
	cloned := source
	cloned.SelectedCameraIDs = append([]string(nil), source.SelectedCameraIDs...)
	if cloned.SelectedCameraIDs == nil {
		cloned.SelectedCameraIDs = []string{}
	}
	return cloned
}
