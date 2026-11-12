package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestOpenCreatesStableIdentityAndPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "managed-state.json")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	initial := first.Snapshot()
	if !validInstanceID(initial.InstanceID) {
		t.Fatalf("invalid generated instance ID %q", initial.InstanceID)
	}
	if initial.SchemaVersion != currentSchemaVersion || initial.SelectionRevision != 0 || len(initial.SelectedCameraIDs) != 0 {
		t.Fatalf("unexpected initial state: %#v", initial)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen state error = %v", err)
	}
	if got := second.Snapshot().InstanceID; got != initial.InstanceID {
		t.Fatalf("instance ID changed after reopening: got %q, want %q", got, initial.InstanceID)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat state file: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("state file mode = %04o, want 0600", got)
		}
	}
}

func TestReplaceSelectionPersistsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	instanceID := state.Snapshot().InstanceID

	updated, err := state.ReplaceSelection(0, []string{"cam_b", "cam_a"})
	if err != nil {
		t.Fatalf("ReplaceSelection() error = %v", err)
	}
	if updated.SelectionRevision != 1 || !reflect.DeepEqual(updated.SelectedCameraIDs, []string{"cam_a", "cam_b"}) {
		t.Fatalf("unexpected updated state: %#v", updated)
	}

	idempotent, err := state.ReplaceSelection(1, []string{"cam_a", "cam_b"})
	if err != nil {
		t.Fatalf("idempotent ReplaceSelection() error = %v", err)
	}
	if idempotent.SelectionRevision != 1 {
		t.Fatalf("idempotent update incremented revision to %d", idempotent.SelectionRevision)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen updated store: %v", err)
	}
	got := reopened.Snapshot()
	if got.InstanceID != instanceID || got.SelectionRevision != 1 || !reflect.DeepEqual(got.SelectedCameraIDs, []string{"cam_a", "cam_b"}) {
		t.Fatalf("reopened state = %#v", got)
	}
}

func TestRemovingLastCameraPersistsEmptyArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	instanceID := state.Snapshot().InstanceID
	if _, err := state.ReplaceSelection(0, []string{"cam_a"}); err != nil {
		t.Fatal(err)
	}
	updated, err := state.ReplaceSelection(1, []string{})
	if err != nil {
		t.Fatalf("remove final camera: %v", err)
	}
	if updated.InstanceID != instanceID || updated.SelectionRevision != 2 || updated.SelectedCameraIDs == nil || len(updated.SelectedCameraIDs) != 0 {
		t.Fatalf("unexpected empty-selection state: %#v", updated)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte(`"selected_camera_ids": []`)) {
		t.Fatalf("empty selection was not written as a JSON array: %s", contents)
	}
	if bytes.Contains(contents, []byte(`"selected_camera_ids": null`)) {
		t.Fatalf("empty selection was written as null: %s", contents)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after removing final camera: %v", err)
	}
	got := reopened.Snapshot()
	if got.InstanceID != instanceID || got.SelectionRevision != 2 || got.SelectedCameraIDs == nil || len(got.SelectedCameraIDs) != 0 {
		t.Fatalf("reopened empty-selection state: %#v", got)
	}
}

func TestOversizedSelectionLeavesPersistedAndInMemoryStateUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before := state.Snapshot()
	originalFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	const selectionSize = 5000
	largeSelection := make([]string, 0, selectionSize)
	for i := 0; i < selectionSize; i++ {
		largeSelection = append(largeSelection, fmt.Sprintf("cam-%04d-%s", i, strings.Repeat("a", 247)))
	}
	_, err = state.ReplaceSelection(0, largeSelection)
	if !errors.Is(err, ErrStateTooLarge) {
		t.Fatalf("ReplaceSelection() error = %v, want ErrStateTooLarge", err)
	}
	if got := state.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("oversized update changed in-memory state: got %#v, want %#v", got, before)
	}
	afterFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFile, originalFile) {
		t.Fatal("oversized update replaced the valid state file")
	}
}

func TestReplaceSelectionRejectsStaleRevision(t *testing.T) {
	state := openTestStore(t)
	if _, err := state.ReplaceSelection(0, []string{"cam_a"}); err != nil {
		t.Fatal(err)
	}
	before := state.Snapshot()
	got, err := state.ReplaceSelection(0, []string{"cam_b"})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error = %v, want ErrRevisionConflict", err)
	}
	if !reflect.DeepEqual(got, before) || !reflect.DeepEqual(state.Snapshot(), before) {
		t.Fatalf("stale update changed state: returned %#v, stored %#v, want %#v", got, state.Snapshot(), before)
	}
}

func TestReplaceSelectionRejectsDuplicateIDs(t *testing.T) {
	state := openTestStore(t)
	before := state.Snapshot()
	if _, err := state.ReplaceSelection(0, []string{"cam_a", "cam_a"}); !errors.Is(err, ErrDuplicateCameraID) {
		t.Fatalf("error = %v, want ErrDuplicateCameraID", err)
	}
	if got := state.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("duplicate input changed state: %#v", got)
	}
}

func TestOpenCorruptStateDoesNotOverwriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	original := []byte(`{"schema_version":1,broken`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("Open() error = %v, want ErrCorruptState", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, original) {
		t.Fatalf("Open() changed corrupt file: got %q, want %q", after, original)
	}
}

func TestOpenRejectsUnsupportedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-state.json")
	contents := `{"schema_version":99,"instance_id":"inst_0123456789abcdef0123456789abcdef","selection_revision":0,"selected_camera_ids":[]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("Open() error = %v, want ErrCorruptState", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != contents {
		t.Fatalf("unsupported state was modified: data=%q err=%v", got, err)
	}
}

func TestOpenRejectsInsecureFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "managed-state.json")
	contents := `{"schema_version":1,"instance_id":"inst_0123456789abcdef0123456789abcdef","selection_revision":0,"selected_camera_ids":[]}`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrInsecureState) {
		t.Fatalf("Open() error = %v, want ErrInsecureState", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != contents {
		t.Fatalf("insecure state was modified: data=%q err=%v", got, err)
	}
}

func TestConcurrentSelectionUpdatesAreSerialized(t *testing.T) {
	state := openTestStore(t)
	const attempts = 24
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	conflicts := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := state.ReplaceSelection(0, []string{fmt.Sprintf("cam-%02d", index)})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrRevisionConflict):
				conflicts++
			default:
				t.Errorf("ReplaceSelection() error = %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 || conflicts != attempts-1 {
		t.Fatalf("successful updates = %d, conflicts = %d; want 1 and %d", successes, conflicts, attempts-1)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	state, err := Open(filepath.Join(t.TempDir(), "managed-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return state
}
