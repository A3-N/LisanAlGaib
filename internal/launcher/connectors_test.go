package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisanalgaib/internal/appconfig"
)

func TestConnectorRunIsPrivateAndRestricted(t *testing.T) {
	connector := appconfig.ConnectorConfig{ID: "pardot-observatory", Container: "lisan-pardot-observatory", User: "65532:65532", Network: "arrakis-extension-control", Image: "fixture:3", Managed: true, Bundle: "extensions/pardot-observatory"}
	joined := strings.Join(connectorRunArguments(connector, "/safe/shared"), " ")
	for _, expected := range []string{"--network arrakis-extension-control", "--user 65532:65532", "--read-only", "--cap-drop ALL", "no-new-privileges", "io.lisanalgaib.connector=pardot-observatory", "io.lisanalgaib.connector-config="} {
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
	if configured := strings.Join(connectorRunArguments(connector, "/safe/shared"), " "); !strings.Contains(configured, connector.Tmpfs[0]) {
		t.Fatalf("configured extension tmpfs was not applied: %s", configured)
	}
	before := connectorRuntimeSignature(connector)
	connector.Tmpfs = []string{"/tmp:rw,nosuid,size=32m"}
	if after := connectorRuntimeSignature(connector); after == before {
		t.Fatal("runtime-affecting extension change did not alter its container signature")
	}
}

func TestConnectorGrantsMapToOnlyRequestedRuntimeAccess(t *testing.T) {
	connector := appconfig.ConnectorConfig{ID: "extension", Container: "lisan-extension", User: "10001:10001", Network: "control", Image: "fixture:3", Managed: true, Requests: appconfig.ExtensionGrants{Internet: true, PersistentState: true, SharedRead: true, SharedWrite: true}}
	base := strings.Join(connectorRunArguments(connector, "/safe/shared"), " ")
	if strings.Contains(base, "/safe/shared") || strings.Contains(base, "/var/lib/lisan-extension") {
		t.Fatalf("ungranted storage was mounted: %s", base)
	}
	connector.Grants = appconfig.ExtensionGrants{PersistentState: true, SharedRead: true}
	granted := strings.Join(connectorRunArguments(connector, "/safe/shared"), " ")
	for _, expected := range []string{"/var/lib/lisan-extension", "/safe/shared", "readonly"} {
		if !strings.Contains(granted, expected) {
			t.Fatalf("granted runtime omits %q: %s", expected, granted)
		}
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
