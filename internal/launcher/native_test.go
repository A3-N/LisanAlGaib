package launcher

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/connectors"
)

func TestNativeExtensionsUseSourceExecutablesAndStop(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "extensions", "test-extension", "cmd", "test-extension")
	if err := os.MkdirAll(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module nativefixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const source = `package main

import (
	"encoding/json"
	"flag"
	"net/http"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "listen address")
	flag.Parse()
	http.HandleFunc("/v3/manifest", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"protocol_version": 3, "id": "test-extension", "name": "Test Extension", "version": "3.0.0",
			"views": []map[string]any{{"id": "status", "title": "Status", "default": true}},
		})
	})
	_ = http.ListenAndServe(*listen, nil)
}
`
	if err := os.WriteFile(filepath.Join(sourceDirectory, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.ID = "native-test"
	connector := appconfig.ConnectorConfig{
		ID: "test-extension", Name: "Test Extension", Enabled: true, Managed: true,
		Bundle: "extensions/test-extension", Version: "3.0.0", Image: "fixture:3", BuildContext: ".",
		Dockerfile: "extensions/test-extension/Dockerfile", NativeExecutable: "extensions/test-extension/bin/missing",
		NativePackage: "extensions/test-extension/cmd/test-extension", Container: "lisan-test-extension",
		User: "10001:10001", Network: appconfig.ExtensionControlNetworkName("test-extension"), Endpoint: "http://lisan-test-extension:7777",
	}
	profile.Connectors = []appconfig.ConnectorConfig{connector}
	originalEndpoint := connector.Endpoint
	ctx, cancel := context.WithCancel(context.Background())
	active, runtimeState, err := StartNativeConnectors(ctx, root, profile, io.Discard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	endpoint := active.Connectors[0].Endpoint
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") || endpoint == originalEndpoint {
		t.Fatalf("vm profile did not receive a native loopback endpoint: %q", endpoint)
	}
	if active.Connectors[0].Network != "native-loopback" || profile.Connectors[0].Endpoint != originalEndpoint {
		t.Fatal("native launch mutated the saved Docker lifecycle configuration")
	}
	manifest, err := connectors.FetchManifest(context.Background(), endpoint)
	if err != nil || manifest.ID != connector.ID {
		t.Fatalf("native extension was not reachable: manifest=%#v err=%v", manifest, err)
	}

	cancel()
	if err := runtimeState.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := connectors.FetchManifest(context.Background(), endpoint); err == nil {
		t.Fatal("native extension remained reachable after cleanup")
	}
}
