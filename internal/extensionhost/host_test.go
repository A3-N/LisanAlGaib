package extensionhost

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/connectors"
)

func TestBundledHostCheckConfigIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "docker", "connectors", "host-check", "extension.json")
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Manifest.ID != "host-check" || len(config.Manifest.UI.Sidebar) != 2 {
		t.Fatalf("unexpected Ornithopter config: %#v", config.Manifest)
	}
	if config.Manifest.UI.Sidebar[0].Kind != connectors.PanelTools || config.Manifest.UI.Sidebar[1].Kind != connectors.PanelActions {
		t.Fatalf("Ornithopter does not expose tools and actions panels: %#v", config.Manifest.UI.Sidebar)
	}
	if len(config.Actions) == 0 {
		t.Fatal("Ornithopter config has no actions")
	}
}

func TestExtensionConfigIsMandatoryAndStrict(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing mandatory extension config was accepted")
	}
	path := writeConfig(t, testConfiguration())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "multiple JSON") {
		t.Fatalf("multiple extension config values were accepted: %v", err)
	}
}

func TestExtensionConfigSizeLimitCannotHideTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extension.json")
	data := append([]byte(`{"manifest":{}}`), bytes.Repeat([]byte(" "), 1<<20)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized extension config was accepted: %v", err)
	}
}

func TestRunEndpointAcceptsOnlyAdvertisedActions(t *testing.T) {
	config := testConfiguration()
	request := httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(`{"action_id":"unknown"}`))
	response := httptest.NewRecorder()
	handler(config).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown action returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/run", bytes.NewBufferString(`{"action_id":"check"} {}`))
	response = httptest.NewRecorder()
	handler(config).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous action request returned %d", response.Code)
	}
}

func TestNativeHostStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	host, err := StartNative(ctx, writeConfig(t, testConfiguration()))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := host.Close(shutdown); err != nil {
		t.Fatal(err)
	}
}

func testConfiguration() Configuration {
	return Configuration{
		Manifest: connectors.Manifest{
			ProtocolVersion: connectors.ProtocolVersion,
			ID:              "test",
			Name:            "Test",
			UI: connectors.UIConfig{
				Sidebar: []connectors.Panel{{ID: "actions", Title: "Actions", Kind: connectors.PanelActions}},
				Main:    []connectors.Panel{{ID: "summary", Title: "Summary", Kind: connectors.PanelSummary}},
			},
		},
		Actions: []ActionSpec{{Action: connectors.Action{ID: "check", Name: "Check"}, Command: "unused"}},
	}
}

func writeConfig(t *testing.T, config Configuration) string {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "extension.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
