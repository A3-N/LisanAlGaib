package extensionbundle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestBundledExtensionIsDiscoveredWithoutCoreRegistration(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].ID != "pardot-observatory" {
		t.Fatalf("unexpected discovered bundles: %#v", bundles)
	}
	connector := bundles[0].ConnectorConfig()
	if connector.Enabled || connector.NativeExecutable == "" || connector.Network != appconfig.ExtensionControlNetworkName(connector.ID) {
		t.Fatalf("discovered extension lifecycle is incomplete or enabled: %#v", connector)
	}
}

func TestReconcilePreservesLifecycleChoiceAndGrants(t *testing.T) {
	bundle := Bundle{
		SchemaVersion: SchemaVersion, ID: "extension", Name: "Extension", Version: "3.0.0", Directory: "extensions/extension",
		Docker:   DockerRuntime{Image: "extension:3", Context: ".", Dockerfile: "extensions/extension/Dockerfile", User: "10001:10001", Port: 7777},
		Native:   NativeRuntime{Executable: "extensions/extension/bin/extension_${os}_${arch}"},
		Requests: Grants{PersistentState: true},
	}
	document := appconfig.DefaultDocument(time.Now())
	document.Profiles[0].Connectors = []appconfig.ConnectorConfig{
		{ID: "extension", Bundle: "extensions/extension", Enabled: true, Grants: appconfig.ExtensionGrants{PersistentState: true}},
		{ID: "external", Name: "External", Enabled: true, External: true, Container: "external", Network: "external", Endpoint: "http://external:7777"},
	}
	Reconcile(&document, []Bundle{bundle})
	connectors := document.Profiles[0].Connectors
	if len(connectors) != 2 || connectors[0].ID != "external" || connectors[1].ID != "extension" || !connectors[1].Enabled || !connectors[1].Grants.PersistentState {
		t.Fatalf("reconcile lost external state or extension choices: %#v", connectors)
	}
}

func TestEveryPresetIncludesNewBundlesDisabled(t *testing.T) {
	bundle := Bundle{
		SchemaVersion: SchemaVersion, ID: "extension", Name: "Extension", Version: "3.0.0", Directory: "extensions/extension",
		Docker: DockerRuntime{Image: "extension:3", Context: ".", Dockerfile: "extensions/extension/Dockerfile", User: "10001:10001", Port: 7777},
		Native: NativeRuntime{Executable: "extensions/extension/bin/extension_${os}_${arch}"},
	}
	document := appconfig.Document{SchemaVersion: appconfig.SchemaVersion}
	for _, preset := range appconfig.Presets {
		profile := appconfig.ProfileFromPreset(preset, time.Now())
		profile.ID = preset.ID
		document.Profiles = append(document.Profiles, profile)
	}
	Reconcile(&document, []Bundle{bundle})
	for _, profile := range document.Profiles {
		if len(profile.Connectors) != 1 || profile.Connectors[0].ID != bundle.ID || profile.Connectors[0].Enabled {
			t.Fatalf("preset %q did not include the extension disabled: %#v", profile.Preset, profile.Connectors)
		}
	}
}

func TestDiscoverRejectsOversizedBundle(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "extensions", "oversized")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, maxBundleFileBytes+1)
	if err := os.WriteFile(filepath.Join(directory, "bundle.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("oversized extension bundle was accepted")
	}
}

func TestDiscoverRejectsMalformedOrEscapingBundles(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "extensions", "bad")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"schema_version":1,"id":"bad","name":"Bad","version":"3","docker":{"image":"bad","context":"../escape","dockerfile":"Dockerfile","user":"1:1","port":7777},"native":{"executable":"bad"}}`
	if err := os.WriteFile(filepath.Join(directory, "bundle.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil {
		t.Fatal("escaping bundle path was accepted")
	}
}
