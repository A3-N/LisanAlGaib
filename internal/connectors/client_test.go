package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestManifestAndActionProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/manifest":
			_ = json.NewEncoder(writer).Encode(Manifest{
				ProtocolVersion: ProtocolVersion, ID: "test", Name: "Test",
				UI: UIConfig{
					Sidebar: []Panel{{ID: "actions", Title: "Actions", Kind: PanelActions, Expanded: true}},
					Main:    []Panel{{ID: "summary", Title: "Summary", Kind: PanelSummary}},
				},
				Actions: []Action{{ID: "ping", Name: "Ping"}},
			})
		case "/v1/run":
			_ = json.NewEncoder(writer).Encode(RunResponse{ActionID: "ping", Output: "pong\x1b", ExitCode: 0})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	manifest, err := FetchManifest(context.Background(), server.URL)
	if err != nil || manifest.ID != "test" {
		t.Fatalf("manifest: %#v %v", manifest, err)
	}
	result, err := RunAction(context.Background(), server.URL, "ping")
	if err != nil || result.Output != "pong" {
		t.Fatalf("action: %#v %v", result, err)
	}
}

func TestManifestRequiresUIConfig(t *testing.T) {
	manifest := Manifest{ProtocolVersion: ProtocolVersion, ID: "test", Name: "Test"}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("manifest without mandatory UI config was accepted")
	}
	manifest.UI = UIConfig{
		Sidebar: []Panel{{ID: "bad", Title: "Bad", Kind: "arbitrary"}},
		Main:    []Panel{{ID: "summary", Title: "Summary", Kind: PanelSummary}},
	}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("manifest with unsupported UI module was accepted")
	}
}

func TestManifestRequiresUniqueIdentifiers(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion: ProtocolVersion,
		ID:              "test",
		Name:            "Test",
		UI: UIConfig{
			Sidebar: []Panel{{ID: "same", Title: "One", Kind: PanelTools}, {ID: "same", Title: "Two", Kind: PanelActions}},
			Main:    []Panel{{ID: "summary", Title: "Summary", Kind: PanelSummary}},
		},
	}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("duplicate panel id was accepted")
	}
	manifest.UI.Sidebar = []Panel{{ID: "tools", Title: "Tools", Kind: PanelTools}}
	manifest.Tools = []Tool{{ID: "same", Name: "One"}, {ID: "same", Name: "Two"}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("duplicate tool id was accepted")
	}
	manifest.Tools = nil
	manifest.Actions = []Action{{ID: "same", Name: "One"}, {ID: "same", Name: "Two"}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("duplicate action id was accepted")
	}
}

func TestManifestRejectsExcessiveModules(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion: ProtocolVersion,
		ID:              "test",
		Name:            "Test",
		UI: UIConfig{
			Sidebar: []Panel{{ID: "tools", Title: "Tools", Kind: PanelTools}},
			Main:    []Panel{{ID: "summary", Title: "Summary", Kind: PanelSummary}},
		},
		Tools: make([]Tool, MaxTools+1),
	}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "limits") {
		t.Fatalf("excessive manifest was accepted: %v", err)
	}
}

func TestScanExcludesDisabledConnectors(t *testing.T) {
	states := Scan(context.Background(), []appconfig.ConnectorConfig{{ID: "off", Enabled: false, Endpoint: "not-a-url"}})
	if len(states) != 0 {
		t.Fatalf("disabled connector was contacted: %#v", states)
	}
}

func TestManifestTimeoutAndEndpointValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := FetchManifest(ctx, "file:///tmp/socket"); err == nil {
		t.Fatal("non-HTTP connector endpoint was accepted")
	}
}

func TestResponseLimitAndActionIDValidation(t *testing.T) {
	if err := decodeLimited(bytes.NewReader(make([]byte, maxResponseBytes+1)), &Manifest{}); err == nil {
		t.Fatal("oversized extension response was accepted")
	}
	if _, err := RunAction(context.Background(), "http://127.0.0.1", "bad action"); err == nil {
		t.Fatal("invalid action id reached the network client")
	}
}
