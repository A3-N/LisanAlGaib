package appconfig

import (
	"os"
	"path/filepath"
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
	legacy := strings.Replace(string(data), `"docker_shell": "fish",`, `"outer": "kitty", "host_shell": "auto", "docker_shell": "fish",`, 1)
	legacy = strings.Replace(legacy, `"git": true,`, `"fish": true, "kitty": true, "nerd-font": true, "bwrap": true, "docker": true, "git": true,`, 1)
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
	if strings.Contains(string(data), `"outer"`) || strings.Contains(string(data), `"host_shell"`) || strings.Contains(string(data), `"kitty": true`) || strings.Contains(string(data), `"fish": true`) || strings.Contains(string(data), `"nerd-font": true`) || strings.Contains(string(data), `"bwrap": true`) || strings.Contains(string(data), `"docker": true`) {
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

func TestDefaultExtensionIsOptInAndExplicitFullPresetEnablesIt(t *testing.T) {
	now := time.Now()
	document := DefaultDocument(now)
	active, _ := document.Active()
	if len(active.Connectors) != 1 || active.Connectors[0].ID != "host-check" || active.Connectors[0].Enabled || active.Connectors[0].Network != "arrakis-shield-wall" {
		t.Fatalf("new config did not expose disabled Ornithopter: %#v", active.Connectors)
	}
	for _, connector := range active.Connectors {
		if connector.Managed && connector.NativeConfig == "" {
			t.Fatalf("managed extension has no Wormsign native config: %#v", connector)
		}
	}
	changed := ProfileFromPreset(Presets[0], now.Add(time.Second))
	if !changed.Connectors[0].Enabled {
		t.Fatalf("explicit Golden Path did not enable Ornithopter: %#v", changed.Connectors)
	}
	saved := document.SaveSelection(changed, now.Add(time.Second))
	if saved.ID == active.ID || len(document.Profiles) != 2 {
		t.Fatalf("connector toggle was not versioned: %#v", document)
	}
}

func TestBundledExtensionMigrationReplacesLegacyExamples(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Connectors = []ConnectorConfig{
		{ID: "mobile-lab", Enabled: true},
		{ID: "runtime-scout", Enabled: true},
		{ID: "third-party", Name: "Third Party", Enabled: true},
	}
	normalizeProfile(&profile)
	if len(profile.Connectors) != 2 {
		t.Fatalf("legacy examples were not replaced cleanly: %#v", profile.Connectors)
	}
	if profile.Connectors[0].ID != "third-party" {
		t.Fatalf("third-party extension was not preserved: %#v", profile.Connectors)
	}
	if profile.Connectors[1].ID != "host-check" || !profile.Connectors[1].Enabled {
		t.Fatalf("Ornithopter example was not added: %#v", profile.Connectors)
	}
}

func TestLegacyRuntimeNamesMigrateToArrakis(t *testing.T) {
	profile := ProfileFromPreset(Presets[0], time.Now())
	profile.Name = "Dune Full"
	profile.Terminal.DockerUser = "dune"
	profile.Terminal.DockerWorkdir = "/home/dune/projects/example"
	profile.Connectors[0].Name = "Host Check"
	profile.Connectors[0].Network = "lisan-al-gaib"
	normalizeProfile(&profile)

	if profile.Name != "Golden Path" || profile.Terminal.DockerUser != "fremen" || profile.Terminal.DockerWorkdir != "/home/fremen/projects/example" {
		t.Fatalf("legacy runtime identity was not migrated: %#v", profile)
	}
	if profile.Connectors[0].Name != "Ornithopter" || profile.Connectors[0].Network != "arrakis-shield-wall" {
		t.Fatalf("legacy connector vocabulary was not migrated: %#v", profile.Connectors[0])
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
	duplicate := document.Profiles[0].Connectors[0]
	document.Profiles[0].Connectors = append(document.Profiles[0].Connectors, duplicate)
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "duplicate extension") {
		t.Fatalf("duplicate extension id was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	document.Profiles[0].Connectors[0].Enabled = true
	document.Profiles[0].Connectors[0].Endpoint = "http://user:placeholder@host-check:7777"
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "without credentials") {
		t.Fatalf("credential-bearing extension URL was accepted: %v", err)
	}

	document = DefaultDocument(time.Now())
	document.Profiles[0].Connectors[0].User = "root;unsafe"
	if err := Save(path, document); err == nil || !strings.Contains(err.Error(), "container user") {
		t.Fatalf("invalid extension user was accepted: %v", err)
	}
}

func TestConfigTextCannotInjectTerminalControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := DefaultDocument(time.Now())
	document.Profiles[0].Name = "\x1b[31mDune\x07"
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
