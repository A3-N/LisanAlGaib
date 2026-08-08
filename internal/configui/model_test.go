package configui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lisanalgaib/internal/appconfig"
)

func TestPresetToggleAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := appconfig.DefaultDocument(time.Now())
	model := New(document, path)
	model.applyPreset(3)
	model.expanded["features"] = true
	if model.working.Feature("terminal") {
		t.Fatal("minimal preset must disable terminal")
	}
	optionIndex := -1
	for index, option := range appconfig.Options {
		if option.Category == appconfig.Features && option.ID == "terminal" {
			optionIndex = index
		}
	}
	for index, candidate := range model.rows() {
		if candidate.kind == rowOption && candidate.option == optionIndex {
			model.activate(index)
			break
		}
	}
	if !model.working.Feature("terminal") {
		t.Fatal("checkbox activation did not toggle terminal")
	}
	model.save()
	loaded, err := appconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := loaded.Active()
	if !active.Feature("terminal") || len(loaded.Profiles) != 2 {
		t.Fatalf("custom selection not saved: %#v", loaded)
	}
}

func TestDropdownDefaultsAndOrdering(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	for _, id := range dropdowns {
		if model.expanded[id] != expandedByDefault[id] {
			t.Fatalf("dropdown %q expanded = %t, want %t", id, model.expanded[id], expandedByDefault[id])
		}
	}

	var order []string
	for _, candidate := range model.rows() {
		if candidate.kind == rowDropdown {
			order = append(order, candidate.value)
		}
	}
	want := []string{"agents", "presets", "profiles", "features", "connectors", "terminal-docker-shell", "tools"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("dropdown order = %v, want %v", order, want)
	}
}

func TestEnterAndSpaceBothActivateFocusedRow(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	for index, candidate := range model.rows() {
		if candidate.kind == rowDropdown && candidate.value == "features" {
			model.cursor = index
			break
		}
	}

	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !model.expanded["features"] {
		t.Fatal("Enter did not expand the focused dropdown")
	}
	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Text: " "}))
	if model.expanded["features"] {
		t.Fatal("Space did not collapse the focused dropdown")
	}
}

func TestOnlyDockerShellIsConfigurable(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	model.expanded["terminal-docker-shell"] = true
	choices := map[string]bool{}
	for _, candidate := range model.rows() {
		if candidate.kind != rowTerminal {
			continue
		}
		if candidate.setting != "docker-shell" {
			t.Fatalf("non-Docker terminal setting remains configurable: %#v", candidate)
		}
		choices[candidate.value] = true
	}
	for _, shell := range []string{"fish", "bash", "zsh", "sh"} {
		if !choices[shell] {
			t.Fatalf("Docker shell choice %q is missing: %#v", shell, choices)
		}
	}
	if len(choices) != 4 {
		t.Fatalf("unexpected terminal choices remain: %#v", choices)
	}
}

func TestConfigUsesSquareCheckboxesAndCircularRadioChoices(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	model.applyPreset(3)
	model.expanded["presets"] = true

	for index, candidate := range model.rows() {
		if candidate.kind == rowOption {
			model.activate(index)
			break
		}
	}
	content := model.View().Content
	for _, mark := range []string{"■", "□", "●", "○", "▾"} {
		if !strings.Contains(content, mark) {
			t.Fatalf("config view is missing %q selection mark", mark)
		}
	}
	for _, oldMark := range []string{"☑", "☐", "◇"} {
		if strings.Contains(content, oldMark) {
			t.Fatalf("config view still contains old selection mark %q", oldMark)
		}
	}
}

func TestSavedProfileEnterLoadsAndCtrlSSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := appconfig.DefaultDocument(time.Now())
	selection, _ := document.Active()
	selection.Set(appconfig.Features, "terminal", false)
	document.SaveSelection(selection, time.Now())
	initialActive := document.ActiveProfileID
	model := New(document, path)
	model.expanded["profiles"] = true

	var target appconfig.Profile
	for index, candidate := range model.rows() {
		if candidate.kind != rowProfile {
			continue
		}
		profile := model.document.Profiles[candidate.profile]
		if profile.ID != initialActive {
			target = profile
			model.cursor = index
			model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			break
		}
	}
	if target.ID == "" {
		t.Fatal("test did not find an inactive saved profile")
	}
	if model.document.ActiveProfileID != initialActive {
		t.Fatal("Enter activated a saved profile before Ctrl-S")
	}
	if model.working.ID != target.ID {
		t.Fatal("Enter did not load the saved profile as the working selection")
	}
	beforeSignature := model.working.Signature()
	var beforeIDs []string
	for _, profile := range model.document.Profiles {
		beforeIDs = append(beforeIDs, profile.ID+":"+profile.Signature())
	}

	model.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	if !model.saved || model.document.ActiveProfileID != target.ID {
		t.Fatalf("Ctrl-S did not save and activate the working profile: active=%q target=%q before=%q profiles=%v target-signature=%q working-signature=%q err=%v status=%q", model.document.ActiveProfileID, target.ID, beforeSignature, beforeIDs, target.Signature(), model.working.Signature(), model.err, model.status)
	}
	loaded, err := appconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveProfileID != target.ID {
		t.Fatalf("saved active profile = %q, want %q", loaded.ActiveProfileID, target.ID)
	}
}

func TestPresetPreservesThirdPartyExtensionDefinitions(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	model.working.Connectors = append(model.working.Connectors, appconfig.ConnectorConfig{
		ID: "custom", Name: "Custom", Enabled: true, Managed: false,
		Container: "custom", Network: "arrakis-shield-wall", Endpoint: "http://custom:7777",
	})
	model.applyPreset(3)
	if len(model.working.Connectors) != 2 || model.working.Connectors[1].ID != "custom" {
		t.Fatalf("preset discarded custom extension: %#v", model.working.Connectors)
	}
	if model.working.Connectors[1].Enabled {
		t.Fatal("minimal preset left a custom extension running")
	}
	model.applyPreset(0)
	if len(model.working.Connectors) != 2 || !model.working.Connectors[1].Enabled {
		t.Fatalf("full preset did not restore custom extension: %#v", model.working.Connectors)
	}
}

func TestConfigViewPaintsEveryTerminalRow(t *testing.T) {
	model := New(appconfig.DefaultDocument(time.Now()), filepath.Join(t.TempDir(), "config.json"))
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	lines := strings.Split(model.View().Content, "\n")
	if len(lines) != 18 {
		t.Fatalf("config view has %d rows, want 18", len(lines))
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width != 80 {
			t.Fatalf("config row %d has width %d, want 80", index, width)
		}
	}
}
