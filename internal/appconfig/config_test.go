package appconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultAndSavedSelectionHistory(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	document := DefaultDocument(now)
	active, ok := document.Active()
	if !ok || !active.Feature("terminal") || !active.Agent("codex") {
		t.Fatalf("unexpected default active profile: %#v", active)
	}

	selection := ProfileFromPreset(Presets[3], now)
	saved := document.SaveSelection(selection, now.Add(time.Minute))
	if saved.ID == active.ID || len(document.Profiles) != 2 || document.ActiveProfileID != saved.ID {
		t.Fatalf("selection was not appended and activated: %#v", document)
	}

	again := document.SaveSelection(selection, now.Add(2*time.Minute))
	if again.ID != saved.ID || len(document.Profiles) != 2 {
		t.Fatalf("identical selection should reuse history entry: %#v", document)
	}
}

func TestGoldenPathDefaultsToPythonAndCoreNetworkTools(t *testing.T) {
	active, ok := DefaultDocument(time.Now()).Active()
	if !ok {
		t.Fatal("default document has no active profile")
	}
	for _, id := range []string{"node", "go", "rust", "java", "clang", "ruby", "php", "lua"} {
		if active.Tool(id) {
			t.Fatalf("language %q unexpectedly starts selected", id)
		}
	}
	for _, id := range []string{"python", "pip", "curl", "zip", "ip", "ping", "dns", "net-tools", "traceroute", "netcat"} {
		if !active.Tool(id) {
			t.Fatalf("default tool %q did not start selected", id)
		}
	}
	for _, id := range []string{"jq", "wget", "fd", "fzf", "bat", "tree", "shellcheck", "nmap", "mtr", "tcpdump", "whois"} {
		if active.Tool(id) {
			t.Fatalf("optional tool %q unexpectedly starts selected", id)
		}
	}
}

func TestNamedPresetRefreshesWhenToolCatalogChanges(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Tools["node"] = true
	profile.Tools["go"] = true
	delete(profile.Tools, "pip")
	delete(profile.Tools, "curl")
	normalizeProfile(&profile)
	if profile.Tool("node") || profile.Tool("go") || !profile.Tool("pip") || !profile.Tool("curl") {
		t.Fatalf("named preset retained stale tool defaults: %#v", profile.Tools)
	}
}

func TestRoundTripAndEnvironmentProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	document := DefaultDocument(time.Now())
	if err := Save(path, document); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	active, ok := loaded.Active()
	if !ok || active.Signature() == "" {
		t.Fatalf("profile did not round trip: %#v", loaded)
	}
	encoded, err := EncodeProfile(active)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProfile(encoded)
	if err != nil || decoded.Signature() != active.Signature() {
		t.Fatalf("environment profile did not round trip: %#v, %v", decoded, err)
	}
}

func TestEveryPresetOnlyUsesKnownOptions(t *testing.T) {
	known := map[string]bool{}
	for _, option := range Options {
		known[option.ID] = true
	}
	for _, preset := range Presets {
		for id := range preset.Enabled {
			if !known[id] {
				t.Fatalf("preset %q references unknown option %q", preset.ID, id)
			}
		}
	}
}

func TestAgentCatalogIncludesOhMyPi(t *testing.T) {
	want := []string{"codex", "opencode", "claude", "kimi", "omp"}
	if got := AgentIDs(); !slices.Equal(got, want) {
		t.Fatalf("agent catalog = %#v, want %#v", got, want)
	}
	for _, presetIndex := range []int{0, 2} {
		profile := ProfileFromPreset(Presets[presetIndex], time.Now())
		if !profile.Agent("omp") {
			t.Fatalf("agent preset %q does not select Oh My Pi", Presets[presetIndex].ID)
		}
	}
}

func TestEveryPresetExcludesRemovedHostOptions(t *testing.T) {
	for _, preset := range Presets {
		profile := ProfileFromPreset(preset, time.Now())
		for _, removed := range []string{"bwrap", "docker", "nerd-font"} {
			if _, exists := profile.Tools[removed]; exists {
				t.Fatalf("preset %q includes removed host option %q: %#v", preset.ID, removed, profile)
			}
		}
	}
}

func TestLegacySkillsFeatureIsRemovedDuringNormalization(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.ID = "legacy-skills"
	profile.Features["skills"] = true
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		t.Fatalf("legacy skills feature prevented profile loading: %v", err)
	}
	if _, exists := normalized.Features["skills"]; exists {
		t.Fatalf("removed skills feature survived normalization: %#v", normalized.Features)
	}
}

func TestDockerShellChoicesAreValidated(t *testing.T) {
	for _, shell := range []string{"fish", "bash", "zsh", "sh"} {
		profile := ProfileFromPreset(Presets[0], time.Now())
		profile.ID = "shell-test"
		profile.Terminal.DockerShell = shell
		if _, err := NormalizeProfile(profile); err != nil {
			t.Fatalf("Docker shell %q was rejected: %v", shell, err)
		}
	}
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.ID = "shell-test"
	profile.Terminal.DockerShell = "powershell"
	if _, err := NormalizeProfile(profile); err == nil || !strings.Contains(err.Error(), "unsupported Docker shell") {
		t.Fatalf("unsupported Docker shell was accepted: %v", err)
	}
}

func TestLegacyTerminalChoicesLoadButAreNotSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, DefaultDocument(time.Now())); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(data), `"docker_shell": "fish",`, `"outer": "deprecated", "host_shell": "auto", "docker_shell": "fish",`, 1)
	legacy = strings.Replace(legacy, `"git": true,`, `"retired-emulator": true, "retired-shell": true, "git": true,`, 1)
	if legacy == string(data) {
		t.Fatal("could not create legacy terminal fixture")
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatalf("legacy terminal settings did not load: %v", err)
	}
	if err := Save(path, document); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"outer"`) || strings.Contains(string(data), `"host_shell"`) || strings.Contains(string(data), `"retired-emulator": true`) || strings.Contains(string(data), `"retired-shell": true`) {
		t.Fatalf("removed terminal settings survived save: %s", data)
	}
}

func TestTerminalSettingsCreateDistinctProfileRevision(t *testing.T) {
	now := time.Now()
	document := DefaultDocument(now)
	active, _ := document.Active()
	changed := active.Clone()
	changed.Terminal.DockerShell = "bash"
	saved := document.SaveSelection(changed, now.Add(time.Second))
	if saved.ID == active.ID || len(document.Profiles) != 2 {
		t.Fatalf("terminal runtime change was not versioned: %#v", document)
	}
}

func TestWorkspaceContainerUserIsKnown(t *testing.T) {
	document := DefaultDocument(time.Now())
	document.Profiles[0].Terminal.DockerUser = "missing-user"
	if err := Save(filepath.Join(t.TempDir(), "config.json"), document); err == nil || !strings.Contains(err.Error(), "fremen or root") {
		t.Fatalf("unknown workspace container user was accepted: %v", err)
	}
}

func TestCorePresetsContainNoHardcodedExtensions(t *testing.T) {
	now := time.Now()
	document := DefaultDocument(now)
	active, _ := document.Active()
	if len(active.Connectors) != 0 {
		t.Fatalf("core preset hardcoded an extension: %#v", active.Connectors)
	}
	for _, preset := range Presets {
		profile := ProfileFromPreset(preset, now.Add(time.Second))
		if len(profile.Connectors) != 0 {
			t.Fatalf("preset %q hardcoded an extension: %#v", preset.ID, profile.Connectors)
		}
	}
	changed := active.Clone()
	changed.Connectors = []ConnectorConfig{testManagedExtension()}
	saved := document.SaveSelection(changed, now.Add(time.Second))
	if saved.ID == active.ID || len(document.Profiles) != 2 {
		t.Fatalf("connector toggle was not versioned: %#v", document)
	}
}

func TestProfileNormalizationDoesNotInventOrDiscardExtensions(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Connectors = []ConnectorConfig{
		{ID: "third-party", Name: "Third Party", Enabled: true, External: true, Container: "third-party", Network: "external", Endpoint: "http://third-party:7777"},
	}
	normalizeProfile(&profile)
	if len(profile.Connectors) != 1 || profile.Connectors[0].ID != "third-party" {
		t.Fatalf("third-party extension was not preserved: %#v", profile.Connectors)
	}
}

func TestLegacyRuntimeNamesMigrateToArrakis(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Name = "Dune Full"
	profile.Terminal.DockerUser = "dune"
	profile.Terminal.DockerWorkdir = "/home/dune/projects/example"
	profile.Connectors = []ConnectorConfig{testManagedExtension()}
	profile.Connectors[0].Network = "lisan-al-gaib"
	normalizeProfile(&profile)

	if profile.Name != "Golden Path" || profile.Terminal.DockerUser != "fremen" || profile.Terminal.DockerWorkdir != "/home/fremen/projects/example" {
		t.Fatalf("legacy runtime identity was not migrated: %#v", profile)
	}
	if profile.Connectors[0].Network != ExtensionControlNetworkName("test-extension") {
		t.Fatalf("legacy connector vocabulary was not migrated: %#v", profile.Connectors[0])
	}
}

func TestManagedExtensionIsolationValuesAreStrict(t *testing.T) {
	for _, user := range []string{"", "root", "0", "0:0", "name:10001", "10001:0"} {
		if ValidExtensionContainerUser(user) {
			t.Fatalf("unsafe extension user %q was accepted", user)
		}
	}
	for _, user := range []string{"10001", "10001:10001", "65532:65532"} {
		if !ValidExtensionContainerUser(user) {
			t.Fatalf("safe numeric extension user %q was rejected", user)
		}
	}
	valid := "/run:rw,noexec,nosuid,nodev,size=8m"
	if err := ValidateExtensionTmpfs(valid); err != nil {
		t.Fatalf("safe tmpfs was rejected: %v", err)
	}
	for _, value := range []string{
		"/run", "/run:rw,size=8m", "/run:rw,noexec,nosuid,nodev", "/tmp:rw,noexec,nosuid,nodev,size=8m",
		"/proc/work:rw,noexec,nosuid,nodev,size=8m", "/run:rw,exec,nosuid,nodev,size=8m",
	} {
		if err := ValidateExtensionTmpfs(value); err == nil {
			t.Fatalf("unsafe tmpfs %q was accepted", value)
		}
	}
	if err := ValidateExtensionEnvironment("REGION=arrakeen"); err != nil {
		t.Fatalf("safe environment was rejected: %v", err)
	}
	for _, value := range []string{"MISSING_VALUE", "1BAD=value", "LISAN_EXTENSION_ID=spoof", "BAD=one\x00two"} {
		if err := ValidateExtensionEnvironment(value); err == nil {
			t.Fatalf("unsafe environment %q was accepted", value)
		}
	}
	for _, value := range []string{"alpine", "registry.example.test:5000/team/image:3", "image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if err := ValidateExtensionImageArgument(value); err != nil {
			t.Fatalf("safe image %q was rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "--privileged", " image:1", "image:1\n--privileged", "image name:1"} {
		if err := ValidateExtensionImageArgument(value); err == nil {
			t.Fatalf("unsafe image %q was accepted", value)
		}
	}
}

func TestLegacyExtensionTmpfsIsHardenedDuringLoad(t *testing.T) {
	document := DefaultDocument(time.Now())
	connector := testManagedExtension()
	connector.Tmpfs = []string{"/run:rw,noexec,nosuid,size=8m"}
	document.Profiles[0].Connectors = []ConnectorConfig{connector}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("legacy tmpfs blocked config load: %v", err)
	}
	active, ok := loaded.Active()
	if !ok || len(active.Connectors) != 1 || len(active.Connectors[0].Tmpfs) != 1 {
		t.Fatalf("loaded connector is incomplete: %#v", active.Connectors)
	}
	if migrated := active.Connectors[0].Tmpfs[0]; migrated != "/run:rw,noexec,nosuid,size=8m,nodev" {
		t.Fatalf("legacy tmpfs was not hardened: %q", migrated)
	}
	if err := ValidateExtensionTmpfs(active.Connectors[0].Tmpfs[0]); err != nil {
		t.Fatalf("migrated tmpfs is invalid: %v", err)
	}
	if migrated := migrateExtensionTmpfs("/run:rw,size=8m"); migrated != "/run:rw,size=8m" {
		t.Fatalf("unsafe tmpfs was silently broadened: %q", migrated)
	}
}

func TestConfigRejectsUnknownFieldsAndDuplicateIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := DefaultDocument(time.Now())
	if err := Save(path, document); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"schema_version": 1,`, `"schema_version": 1, "surprise": true,`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown config field was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	duplicate := document.Profiles[0].Clone()
	document.Profiles = append(document.Profiles, duplicate)
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate profile id was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	document.Profiles[0].Tools["typo-tool"] = true
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "unknown tools option") {
		t.Fatalf("unknown option was accepted: %v", err)
	}
}

func TestConfigRejectsDuplicateExtensionsAndCredentialEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := DefaultDocument(time.Now())
	document.Profiles[0].Connectors = []ConnectorConfig{testManagedExtension()}
	duplicate := document.Profiles[0].Connectors[0]
	document.Profiles[0].Connectors = append(document.Profiles[0].Connectors, duplicate)
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "duplicate extension") {
		t.Fatalf("duplicate extension id was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	document.Profiles[0].Connectors = []ConnectorConfig{testManagedExtension()}
	document.Profiles[0].Connectors[0].Enabled = true
	document.Profiles[0].Connectors[0].Endpoint = "http://user:placeholder@extension:7777"
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "without credentials") {
		t.Fatalf("credential-bearing extension URL was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	document.Profiles[0].Connectors = []ConnectorConfig{testManagedExtension()}
	document.Profiles[0].Connectors[0].User = "root;unsafe"
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "container user") {
		t.Fatalf("invalid extension user was accepted: %v", err)
	}
}

func TestConfigTextCannotInjectTerminalControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := DefaultDocument(time.Now())
	document.Profiles[0].Name = "\x1b[31mDune\x07"
	document.Profiles[0].Connectors = []ConnectorConfig{testManagedExtension()}
	document.Profiles[0].Connectors[0].Description = "safe\x1b[2J text"
	if err := Save(path, document); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := loaded.Active()
	if strings.ContainsAny(active.Name+active.Connectors[0].Description, "\x1b\x07") {
		t.Fatalf("terminal control characters survived normalization: %#v", active)
	}
}

func TestRemovedDeclarativeExtensionConfigIsOneWayMigrated(t *testing.T) {
	var connector ConnectorConfig
	if err := json.Unmarshal([]byte(`{"id":"ixian-proving-ground","native_config":"old/extension.json"}`), &connector); err != nil {
		t.Fatal(err)
	}
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Connectors = []ConnectorConfig{connector}
	normalizeProfile(&profile)
	if len(profile.Connectors) != 0 {
		t.Fatalf("removed declarative extension survived migration: %#v", profile.Connectors)
	}
}

func testManagedExtension() ConnectorConfig {
	return ConnectorConfig{
		ID: "test-extension", Name: "Test Extension", Managed: true, Bundle: "extensions/test-extension", Version: "3.0.0",
		Image: "test-extension:3", BuildContext: ".", Dockerfile: "extensions/test-extension/Dockerfile",
		NativeExecutable: "extensions/test-extension/bin/test", Container: "lisan-test-extension", User: "10001:10001",
		Network: "arrakis-extension-control", Endpoint: "http://lisan-test-extension:7777",
	}
}
