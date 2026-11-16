package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/config"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/httpapi"
	"github.com/xiaomi-local-gateway/xiaomi-local-gateway/internal/store"
)

func TestManagedConfigAllowsEmptyFirstRun(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "not-created.json"), true, "127.0.0.1:8099")
	if err != nil {
		t.Fatalf("load managed config: %v", err)
	}
	if cfg.Listen != "127.0.0.1:8099" || len(cfg.Cameras) != 0 {
		t.Fatalf("managed config = %#v, want empty inventory on loopback", cfg)
	}
	if _, err := loadConfig("", true, "not-a-listener"); err == nil {
		t.Fatal("managed config accepted an invalid listener")
	}
}

func TestLegacyConfigStillRequiresStaticCamera(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:8099","cameras":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path, false, "127.0.0.1:8099"); err == nil {
		t.Fatal("legacy mode accepted an empty camera list")
	}
}

func TestManagedInfoUsesPersistedIdentityAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	var firstID string
	for restart := 0; restart < 2; restart++ {
		handler, err := newHandler(config.Config{Listen: "127.0.0.1:8099"}, "test-api-token", true, dataDir, nil)
		if err != nil {
			t.Fatalf("start managed handler on run %d: %v", restart, err)
		}
		request := httptest.NewRequest(http.MethodGet, "/v2/info", nil)
		request.Header.Set("Authorization", "Bearer test-api-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("/v2/info status on run %d = %d", restart, response.Code)
		}
		var got httpapi.RuntimeInfo
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatalf("decode /v2/info on run %d: %v", restart, err)
		}
		if got.APIVersion != httpapi.ManagedAPIVersion || !reflect.DeepEqual(got.Features, []string{"camera_selection"}) {
			t.Fatalf("/v2/info on run %d = %#v", restart, got)
		}
		if restart == 0 {
			firstID = got.InstanceID
		} else if got.InstanceID != firstID {
			t.Fatalf("instance ID changed after restart: got %q, want %q", got.InstanceID, firstID)
		}
	}
}

func TestManagedStartupRejectsCorruptStoreWithoutReplacingIt(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "managed-state.json")
	original := []byte(`{"schema_version":1,broken`)
	if _, err := store.Open(path); err != nil {
		t.Fatalf("create secure state fixture: %v", err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newHandler(config.Config{Listen: "127.0.0.1:8099"}, "test-api-token", true, dataDir, nil); !errors.Is(err, store.ErrCorruptState) {
		t.Fatalf("newHandler() error = %v, want ErrCorruptState", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, original) {
		t.Fatalf("corrupt state changed: got %q, want %q", after, original)
	}
}
