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
	"lisanalgaib/internal/extensionbundle"
)

func TestNativeExtensionsUsePackedOrSourceExecutablesAndStop(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := extensionbundle.Discover(root)
	if err != nil || len(bundles) == 0 {
		t.Fatalf("discover extension bundles: %v %#v", err, bundles)
	}
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.ID = "native-test"
	connector := bundles[0].ConnectorConfig()
	connector.Enabled = true
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
	job, err := connectors.StartJob(context.Background(), endpoint, connectors.StartJobRequest{ActionID: "record-note", Inputs: map[string]string{"note": "native protocol test"}})
	if err != nil || job.Status != connectors.JobSucceeded {
		t.Fatalf("native v3 action failed: job=%#v err=%v", job, err)
	}
	job, err = connectors.StartJob(context.Background(), endpoint, connectors.StartJobRequest{ActionID: "survey", Inputs: map[string]string{"subject": "runtime boundary", "samples": "3", "detail": "field", "environment": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !job.Terminal() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		job, err = connectors.FetchJob(context.Background(), endpoint, job.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.Status != connectors.JobSucceeded || len(job.Artifacts) != 1 {
		t.Fatalf("showcase survey did not finish with an artifact: %#v", job)
	}
	artifact, err := connectors.FetchArtifact(context.Background(), endpoint, job.ID, job.Artifacts[0])
	if err != nil || !strings.Contains(string(artifact), "runtime boundary") {
		t.Fatalf("showcase artifact was not usable: %q %v", artifact, err)
	}
	session, err := connectors.OpenSession(context.Background(), endpoint, connectors.OpenSessionRequest{SessionID: "field-console", Rows: 20, Columns: 80})
	if err != nil {
		t.Fatal(err)
	}
	session, err = connectors.SendSessionInput(context.Background(), endpoint, session.ID, "help\n")
	if err != nil || !strings.Contains(session.Output, "commands: help") {
		t.Fatalf("restricted field console was not usable: %#v %v", session, err)
	}

	cancel()
	if err := runtimeState.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := connectors.FetchManifest(context.Background(), endpoint); err == nil {
		t.Fatal("native extension remained reachable after cleanup")
	}
}
