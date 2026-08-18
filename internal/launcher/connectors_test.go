package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lisanalgaib/internal/appconfig"
)

func TestConnectorRunIsPrivateAndRestricted(t *testing.T) {
	connector := appconfig.ConnectorConfig{ID: "test-extension", Container: "lisan-test-extension", User: "65532:65532", Network: appconfig.ExtensionControlNetworkName("test-extension"), Image: "fixture:3", Managed: true, Bundle: "extensions/test-extension"}
	joined := strings.Join(connectorRunArguments(connector, "/safe/shared"), " ")
	for _, expected := range []string{"--network " + appconfig.ExtensionControlNetworkName("test-extension"), "--user 65532:65532", "--restart no", "--read-only", "--cap-drop ALL", "no-new-privileges", "io.lisanalgaib.connector=test-extension", "io.lisanalgaib.connector-config="} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("connector is missing restriction %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "--publish") || strings.Contains(joined, "-p ") {
		t.Fatalf("connector unexpectedly publishes a host port: %s", joined)
	}
	if strings.Count(joined, "--tmpfs") != 1 || !strings.Contains(joined, "/tmp:rw,noexec,nosuid,nodev,size=64m") {
		t.Fatalf("extension did not receive exactly the restricted default tmpfs: %s", joined)
	}

	connector.Tmpfs = []string{"/run:rw,noexec,nosuid,nodev,size=16m"}
	if configured := strings.Join(connectorRunArguments(connector, "/safe/shared"), " "); !strings.Contains(configured, connector.Tmpfs[0]) {
		t.Fatalf("configured extension tmpfs was not applied: %s", configured)
	}
	before := connectorRuntimeSignature(connector)
	connector.Tmpfs = []string{"/run:rw,noexec,nosuid,nodev,size=32m"}
	if after := connectorRuntimeSignature(connector); after == before {
		t.Fatal("runtime-affecting extension change did not alter its container signature")
	}
}

func TestConnectorGrantsMapToOnlyRequestedRuntimeAccess(t *testing.T) {
	connector := appconfig.ConnectorConfig{ID: "extension", Container: "lisan-extension", User: "10001:10001", Network: appconfig.ExtensionControlNetworkName("extension"), Image: "fixture:3", Managed: true, Requests: appconfig.ExtensionGrants{Internet: true, PersistentState: true, SharedRead: true, SharedWrite: true}}
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
	if err := validateConnector(appconfig.ConnectorConfig{ID: "safe", Container: "safe", Network: "shared-control", Image: "fixture:1", User: "10001:10001", Managed: true}); err == nil {
		t.Fatal("managed connector without a dedicated control network was accepted")
	}
	if first, second := appconfig.ExtensionControlNetworkName("first"), appconfig.ExtensionControlNetworkName("second"); first == second {
		t.Fatalf("extension control networks are not segmented: %q", first)
	}
	unsafeImage := appconfig.ConnectorConfig{ID: "safe", Container: "safe", Network: appconfig.ExtensionControlNetworkName("safe"), Image: "--privileged", User: "10001:10001", Managed: true}
	if err := validateConnector(unsafeImage); err == nil {
		t.Fatal("Docker option-shaped image was accepted")
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

func TestOnlyUnconfiguredOwnedExtensionsAreStopped(t *testing.T) {
	output := strings.Join([]string{
		"lisan-keep|keep",
		"lisan-stale|stale",
		"malformed",
		"bad name|other",
		"foreign|bad id",
	}, "\n")
	names := unconfiguredManagedConnectorNames(output, map[string]bool{"keep": true})
	if len(names) != 1 || names[0] != "lisan-stale" {
		t.Fatalf("unexpected stale extension selection: %#v", names)
	}
}

func TestConnectorBuildSignatureTracksCoreAndBundleInputs(t *testing.T) {
	root := t.TempDir()
	for path, data := range map[string]string{
		".dockerignore":                    "dist\n",
		"go.mod":                           "module fixture\n",
		"go.sum":                           "",
		"internal/protocol.go":             "package internal\n",
		"extensions/example/Dockerfile":    "FROM scratch\n",
		"extensions/example/extension.txt": "first\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	connector := appconfig.ConnectorConfig{
		ID: "example", Image: "example:1", BuildContext: ".",
		Dockerfile: "extensions/example/Dockerfile", Bundle: "extensions/example",
	}
	before, err := connectorBuildSignature(root, connector)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extensions", "example", "extension.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := connectorBuildSignature(root, connector)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("extension bundle change did not invalidate its Docker image fingerprint")
	}
}
