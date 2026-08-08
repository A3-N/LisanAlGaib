package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisanalgaib/internal/appconfig"
)

func TestConnectorRunIsPrivateAndRestricted(t *testing.T) {
	connector := appconfig.ConnectorConfig{ID: "ixian-proving-ground", Container: "lisan-ixian-proving-ground", User: "10001:10001", Network: "arrakis-shield-wall", Image: "fixture:1", Managed: true}
	joined := strings.Join(connectorRunArguments(connector), " ")
	for _, expected := range []string{"--network arrakis-shield-wall", "--user 10001:10001", "--read-only", "--cap-drop ALL", "no-new-privileges", "io.lisanalgaib.connector=ixian-proving-ground", "io.lisanalgaib.connector-config="} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("connector is missing restriction %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "--publish") || strings.Contains(joined, "-p ") {
		t.Fatalf("connector unexpectedly publishes a host port: %s", joined)
	}
	if strings.Count(joined, "--tmpfs") != 1 || !strings.Contains(joined, "/tmp:rw,noexec,nosuid,size=64m") {
		t.Fatalf("extension did not receive exactly the restricted default tmpfs: %s", joined)
	}

	connector.Tmpfs = []string{"/tmp:rw,nosuid,size=16m"}
	if configured := strings.Join(connectorRunArguments(connector), " "); !strings.Contains(configured, connector.Tmpfs[0]) {
		t.Fatalf("configured extension tmpfs was not applied: %s", configured)
	}
	before := connectorRuntimeSignature(connector)
	connector.Tmpfs = []string{"/tmp:rw,nosuid,size=32m"}
	if after := connectorRuntimeSignature(connector); after == before {
		t.Fatal("runtime-affecting extension change did not alter its container signature")
	}
}

func TestConnectorNamesAndRuntimeBoundary(t *testing.T) {
	if err := validateConnector(appconfig.ConnectorConfig{ID: "bad name", Container: "safe", Network: "safe", Managed: false}); err == nil {
		t.Fatal("invalid connector id was accepted")
	}
	if err := validateConnector(appconfig.ConnectorConfig{ID: "safe", Container: "safe", Network: "safe", Image: "fixture:1", User: "", Managed: true}); err == nil {
		t.Fatal("managed connector without an explicit user was accepted")
	}
	root := t.TempDir()
	if _, err := resolveRuntimePath(root, "../escape"); err == nil {
		t.Fatal("connector build context escaped runtime root")
	}
	outside := filepath.Join(t.TempDir(), "extension.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "extension.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntimePath(root, "extension.json"); err == nil {
		t.Fatal("connector runtime path escaped through a symbolic link")
	}
}
