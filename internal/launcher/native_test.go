package launcher

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/connectors"
)

func TestNativeConnectorsUseEphemeralEndpointsAndStop(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.ID = "native-test"
	profile.Connectors = profile.Connectors[:1]
	originalEndpoint := profile.Connectors[0].Endpoint
	ctx, cancel := context.WithCancel(context.Background())
	active, runtime, err := StartNativeConnectors(ctx, root, profile, io.Discard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	endpoint := active.Connectors[0].Endpoint
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") || endpoint == originalEndpoint {
		t.Fatalf("Wormsign profile did not receive a native loopback endpoint: %q", endpoint)
	}
	if active.Connectors[0].Network != "native-loopback" {
		t.Fatalf("Wormsign profile still advertises a Docker network: %q", active.Connectors[0].Network)
	}
	if profile.Connectors[0].Endpoint != originalEndpoint {
		t.Fatal("native endpoint mutated the saved Docker profile")
	}
	manifest, err := connectors.FetchManifest(context.Background(), endpoint)
	if err != nil || manifest.ID != "host-check" {
		t.Fatalf("native extension was not reachable: manifest=%#v err=%v", manifest, err)
	}

	cancel()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := connectors.FetchManifest(context.Background(), endpoint); err == nil {
		t.Fatal("native extension remained reachable after cleanup")
	}
}
