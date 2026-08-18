package ui

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"lisanalgaib/internal/appconfig"
	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/nvimconfig"
	terminalhost "lisanalgaib/internal/terminal"
	"lisanalgaib/internal/theme"
)

func TestMouseTopBarAndTheme(t *testing.T) {
	t.Setenv(appconfig.EnvironmentConfig, filepath.Join(t.TempDir(), "config.json"))
	model := NewModel(t.TempDir())
	model.inventory = inventory.Snapshot{Tools: []inventory.Tool{{ID: "codex", Name: "Codex", Category: "Agent CLIs", Agent: true, Installed: true}}}
	model.loading = false
	model.width = 100
	model.height = 30

	overviewX := 0
	for _, span := range navigationSpansFor(model.navigation, model.width) {
		if model.navigation[span.Index].Section == sectionOverview {
			overviewX = span.Start
		}
	}
	model.Update(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if model.section != sectionOverview || !model.sidebar {
		t.Fatalf("repeat Overview click did not reveal tools: %#v", model)
	}
	before := model.themeIndex
	model.Update(tea.MouseClickMsg{X: 99, Y: 0, Button: tea.MouseLeft})
	if model.themeIndex == before {
		t.Fatal("top-right click should cycle theme")
	}
}

func TestRepeatedTopClickTogglesSidebarExceptFullWidthPagesAndExtensions(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	model.width = 120
	model.height = 30

	topX := func(target section) int {
		for _, span := range navigationSpansFor(model.navigation, model.width) {
			if model.navigation[span.Index].Section == target {
				return span.Start
			}
		}
		return -1
	}

	overviewX := topX(sectionOverview)
	model.handleClick(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if model.section != sectionOverview || !model.sidebar {
		t.Fatal("repeat Overview click did not reveal the Tools pane")
	}
	model.handleClick(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if model.sidebar {
		t.Fatal("second repeat Overview click did not collapse the Tools pane")
	}
	model.handleClick(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if !model.sidebar {
		t.Fatal("third repeat Overview click did not restore the Tools pane")
	}

	filesX := topX(sectionExplorer)
	model.section = sectionExplorer
	model.page = pageFile
	model.sidebar = false
	model.handleClick(tea.MouseClickMsg{X: filesX, Y: 1, Button: tea.MouseLeft})
	if model.sidebar {
		t.Fatal("repeated Files click added a wrapper sidebar over NvChad")
	}

	model.handleClick(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if model.sidebar {
		t.Fatal("navigating to Overview must start with its Tools pane collapsed")
	}
	model.handleClick(tea.MouseClickMsg{X: overviewX, Y: 1, Button: tea.MouseLeft})
	if !model.sidebar || !model.sidebarDrawn() {
		t.Fatal("repeated Overview click did not reveal its Tools pane")
	}

	extensionsX := topX(sectionExtensions)
	model.handleClick(tea.MouseClickMsg{X: extensionsX, Y: 1, Button: tea.MouseLeft})
	if !model.extensionsOpen || !model.sidebar {
		t.Fatal("Extensions click did not open its dropdown and contextual sidebar")
	}
	model.handleClick(tea.MouseClickMsg{X: extensionsX, Y: 1, Button: tea.MouseLeft})
	if model.extensionsOpen || !model.sidebar {
		t.Fatal("repeated Extensions click must close only the dropdown")
	}
}

func TestMinimalProfileOnlyLoadsOverview(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	model := NewModelWithProfile(t.TempDir(), profile)
	if len(model.navigation) != 1 || model.navigation[0].Section != sectionOverview {
		t.Fatalf("disabled pages leaked into navigation: %#v", model.navigation)
	}
	message := model.Init()().(refreshMsg)
	if len(message.Inventory.Tools) != 0 || len(message.Inventory.APTManual) != 0 {
		t.Fatalf("minimal profile scanned disabled dependencies: %#v", message)
	}
}

func TestOverviewCanBeRemovedFromNavigation(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.Set(appconfig.Features, "overview", false)
	model := NewModelWithProfile(t.TempDir(), profile)
	if len(model.navigation) == 0 || model.navigation[0].Section != sectionExplorer {
		t.Fatalf("Overview-disabled navigation did not start with Files: %#v", model.navigation)
	}
	if model.section != sectionExplorer || model.page != pageFile || model.sectionName() == "Overview" {
		t.Fatalf("Overview remained the initial page: section=%v page=%v name=%q", model.section, model.page, model.sectionName())
	}
	model.handleKey("esc")
	if model.section == sectionOverview || model.page == pageOverview {
		t.Fatal("Esc reopened a disabled Overview page")
	}
}

func TestAllPagesCanBeDisabledSafely(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "overview", false)
	model := NewModelWithProfile(t.TempDir(), profile)
	if len(model.navigation) != 0 || model.section != sectionDisabled || model.page != pageDisabled {
		t.Fatalf("empty page selection was not represented safely: nav=%#v section=%v page=%v", model.navigation, model.section, model.page)
	}
	if command := model.cycleSection(1); command != nil {
		t.Fatal("empty page cycle returned a command")
	}
	if content := model.View().Content; !strings.Contains(content, "No pages are enabled") {
		t.Fatalf("empty page guidance was not rendered: %q", content)
	}
}

func TestOhMyPiToggleControlsMentatNavigationAndInventory(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "agents", true)
	profile.Set(appconfig.Agents, "omp", true)
	if !anyAgentEnabled(profile) {
		t.Fatal("selected Oh My Pi did not enable Mentat navigation")
	}
	selection := inventorySelection(profile)
	if !selection.IDs["omp"] || selection.IDs["codex"] {
		t.Fatalf("Oh My Pi toggle leaked another Mentat: %#v", selection.IDs)
	}

	profile.Set(appconfig.Agents, "omp", false)
	if anyAgentEnabled(profile) {
		t.Fatal("disabled Oh My Pi kept Mentat navigation enabled")
	}
}

func TestEmbeddedEnvironmentDoesNotLeakOuterTerminalCapabilities(t *testing.T) {
	for key, value := range map[string]string{
		"ALACRITTY_WINDOW_ID":   "4",
		"CMUX_SURFACE_ID":       "outer-cmux",
		"GHOSTTY_RESOURCES_DIR": "/outer/ghostty",
		"HERDR_ENV":             "1",
		"ITERM_SESSION_ID":      "outer-iterm",
		"TERM_FEATURES":         "Sy",
		"TERM_PROGRAM":          "WezTerm",
		"TERM_PROGRAM_VERSION":  "999",
		"VSCODE_PID":            "12",
		"WEZTERM_PANE":          "8",
		"WT_SESSION":            "outer-windows-terminal",
		"ZELLIJ_SESSION_NAME":   "outer-zellij",
	} {
		t.Setenv(key, value)
	}
	environment := environmentMap(childEnvironment())
	for _, key := range []string{
		"ALACRITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "ITERM_SESSION_ID",
		"TERM_FEATURES", "TERM_PROGRAM_VERSION", "VSCODE_PID", "WEZTERM_PANE", "CMUX_SURFACE_ID", "HERDR_ENV",
		"WT_SESSION", "ZELLIJ_SESSION_NAME",
	} {
		if value, ok := environment[key]; ok {
			t.Fatalf("embedded environment leaked %s=%q", key, value)
		}
	}
	if environment["TERM"] != "xterm-256color" || environment["TERM_PROGRAM"] != "LisanAlGaib" {
		t.Fatalf("embedded terminal identity is incomplete: %#v", environment)
	}
}

func TestOhMyPiEnvironmentDisablesUnsupportedVirtualTerminalFeatures(t *testing.T) {
	environment := environmentMap(agentEnvironment([]string{"PATH=/bin"}, "omp"))
	for key, want := range map[string]string{
		"PI_NO_DECCARA":          "1",
		"PI_NO_SYNC_OUTPUT":      "1",
		"PI_TUI_RESIZE_IN_PLACE": "0",
	} {
		if got := environment[key]; got != want {
			t.Fatalf("Oh My Pi compatibility %s=%q, want %q", key, got, want)
		}
	}
	if got := environmentMap(agentEnvironment([]string{"PATH=/bin"}, "codex")); len(got) != 1 || got["PATH"] != "/bin" {
		t.Fatalf("Oh My Pi compatibility leaked to another Mentat: %#v", got)
	}
}

func environmentMap(environment []string) map[string]string {
	result := make(map[string]string, len(environment))
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func TestHostTerminalUsesDefaultShellFromEnvironment(t *testing.T) {
	t.Setenv("LISAN_CONTAINER", "")
	t.Setenv("SHELL", "/bin/sh")
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	path, name := shellForContext(profile)
	if path != "/bin/sh" || name != "sh" {
		t.Fatalf("host shell did not inherit SHELL: path=%q name=%q", path, name)
	}
	selection := inventorySelection(profile)
	if !selection.IDs["sh"] || selection.IDs[profile.Terminal.DockerShell] {
		t.Fatalf("host inventory selected Docker shell instead of host shell: %#v", selection.IDs)
	}
}

func TestContainerTerminalUsesOnlyConfiguredDockerShell(t *testing.T) {
	t.Setenv("LISAN_CONTAINER", "1")
	t.Setenv("SHELL", "/bin/bash")
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.Terminal.DockerShell = "sh"
	path, name := shellForContext(profile)
	if path == "" || name != "sh" {
		t.Fatalf("container shell ignored Docker selection: path=%q name=%q", path, name)
	}
	selection := inventorySelection(profile)
	if !selection.IDs["sh"] || selection.IDs["fish"] {
		t.Fatalf("container inventory did not isolate Docker shell choice: %#v", selection.IDs)
	}
}

func TestOverviewUsesResponsiveASCIIArtwork(t *testing.T) {
	model := NewModel(t.TempDir())
	model.width = 120
	model.height = 40
	artwork, ok := overviewArtworkForViewport(model.mainPaneWidth()-4, model.mainContentHeight()-2)
	if !ok {
		t.Fatal("overview did not select artwork for the default viewport")
	}
	art := artwork.crop(model.mainPaneWidth()-4, model.mainContentHeight()-2)
	if len(art) < 6 {
		t.Fatalf("overview artwork was not cropped: %#v", art)
	}
	if content := model.View().Content; !strings.Contains(content, strings.TrimSpace(art[0])) {
		t.Fatal("Overview does not render the selected responsive artwork")
	}
	if model.sidebarDrawn() || model.mainPaneWidth() != model.wrapSafeWidth() {
		t.Fatalf("Overview is not full width: sidebar=%v main=%d safe=%d", model.sidebarDrawn(), model.mainPaneWidth(), model.wrapSafeWidth())
	}
	viewLines := strings.Split(model.View().Content, "\n")
	for index, line := range viewLines[model.topHeight() : len(viewLines)-footerHeight] {
		if width := lipgloss.Width(line); width != model.wrapSafeWidth() {
			t.Fatalf("Overview body row %d has width %d, want wrap-safe width %d", index, width, model.wrapSafeWidth())
		}
	}
	main := strings.Join(model.overviewLines(theme.All[model.themeIndex], model.mainPaneWidth(), model.mainContentHeight()), "\n")
	for _, redundant := range []string{"Workspace", "Config profile", "Process user", "APT manual"} {
		if strings.Contains(main, redundant) {
			t.Fatalf("Overview main pane still contains redundant metadata %q", redundant)
		}
	}
	if rows := model.sidebarRows(); len(rows) == 0 || rows[0].Kind != rowCategory {
		t.Fatalf("Overview does not expose collapsed Tools rows: %#v", rows)
	}
}

func TestOverviewArtworkSelectionAndCropping(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantVisible   bool
	}{
		{name: "full banner", width: overviewBanner.width, height: overviewBanner.height, wantVisible: true},
		{name: "large crop", width: 150, height: 43, wantVisible: true},
		{name: "small banner crop", width: overviewMinimumWidth, height: overviewMinimumHeight, wantVisible: true},
		{name: "too narrow", width: overviewMinimumWidth - 1, height: overviewMinimumHeight, wantVisible: false},
		{name: "too short", width: overviewMinimumWidth, height: overviewMinimumHeight - 1, wantVisible: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artwork, ok := overviewArtworkForViewport(test.width, test.height)
			if ok != test.wantVisible {
				t.Fatalf("artwork visibility is %v, want %v", ok, test.wantVisible)
			}
			if !ok {
				return
			}
			if artwork.width != overviewBanner.width || artwork.height != overviewBanner.height {
				t.Fatalf("viewport selected a non-banner canvas: %dx%d", artwork.width, artwork.height)
			}

			art := artwork.crop(test.width, test.height)
			wantWidth := min(test.width, artwork.width)
			wantHeight := min(test.height, artwork.height)
			if len(art) != wantHeight {
				t.Fatalf("cropped height is %d, want %d", len(art), wantHeight)
			}
			startX := (artwork.width - wantWidth) / 2
			startY := (artwork.height - wantHeight) * 3 / 4
			wantFirstRow := string(artwork.rows[startY][startX : startX+wantWidth])
			if art[0] != wantFirstRow {
				t.Fatal("crop resampled or selected the wrong source row")
			}
			for index, line := range art {
				if width := lipgloss.Width(line); width != wantWidth {
					t.Fatalf("cropped row %d has width %d, want %d", index, width, wantWidth)
				}
			}
		})
	}
}

func TestOverviewVerticalCropPreservesMoreOfTheBottom(t *testing.T) {
	artwork := newASCIIArtwork("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")
	got := artwork.crop(1, 6)
	want := []string{"3", "4", "5", "6", "7", "8"}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Fatalf("asymmetric vertical crop = %#v, want %#v", got, want)
	}
}

func TestOverviewArtworkUsesOnePositionedCanvas(t *testing.T) {
	const (
		viewportWidth  = 240
		viewportHeight = 64
	)
	artwork, ok := overviewArtworkForViewport(viewportWidth-4, viewportHeight-2)
	if !ok {
		t.Fatalf("large viewport selected %#v, visible=%v", artwork, ok)
	}
	art := artwork.crop(viewportWidth-4, viewportHeight-2)
	lines := (&Model{}).overviewLines(theme.All[0], viewportWidth, viewportHeight)
	top := (viewportHeight - len(art)) / 2
	left := (viewportWidth - lipgloss.Width(art[0])) / 2
	for index, artLine := range art {
		expected := strings.Repeat(" ", left) + artLine
		if lines[top+index] != expected {
			t.Fatalf("large Overview row %d was not positioned as one canvas", index)
		}
	}
}

func TestExtensionsUseOneStickyMenuAndProtocolV3Surfaces(t *testing.T) {
	profile := testExtensionProfile()
	custom := appconfig.ConnectorConfig{
		ID: "custom-probe", Name: "Custom Probe", Icon: "󰒍", Enabled: true,
		Container: "custom-probe", Network: "arrakis-shield-wall", Endpoint: "http://custom-probe:7777",
	}
	profile.Connectors = append(profile.Connectors, custom)
	model := NewModelWithProfile(t.TempDir(), profile)
	extensionTabs := 0
	for _, item := range model.navigation {
		if item.Name == "Extensions" && item.Section == sectionExtensions {
			extensionTabs++
		}
		if item.Name == "Test Observatory" || item.Name == "Custom Probe" {
			t.Fatalf("extension leaked into the top bar as its own tab: %#v", model.navigation)
		}
	}
	if extensionTabs != 1 {
		t.Fatalf("expected exactly one Extensions tab: %#v", model.navigation)
	}
	model.connectors = []connectorapi.State{
		{
			Config: profile.Connectors[0], Online: true,
			Manifest: connectorapi.Manifest{
				ProtocolVersion: connectorapi.ProtocolVersion, ID: "test-observatory", Name: "Test Observatory", Version: "3.0.0",
				Views:    []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview", Default: true}},
				Actions:  []connectorapi.ActionDescriptor{{ID: "hostname", Name: "Host name", Inputs: []connectorapi.InputSpec{{ID: "scope", Label: "Scope", Kind: connectorapi.InputText}}}},
				Sessions: []connectorapi.SessionDescriptor{{ID: "console", Name: "Console"}},
			},
			Views: map[string]connectorapi.View{"overview": {ID: "overview", Title: "Overview"}},
		},
		{
			Config: profile.Connectors[1], Online: true,
			Manifest: connectorapi.Manifest{
				ProtocolVersion: connectorapi.ProtocolVersion, ID: "custom-probe", Name: "Custom Probe", Version: "3.0.0",
				Views:   []connectorapi.ViewDescriptor{{ID: "probe", Title: "Probe", Default: true}},
				Actions: []connectorapi.ActionDescriptor{{ID: "probe", Name: "Probe"}},
			},
			Views: map[string]connectorapi.View{"probe": {ID: "probe", Title: "Probe"}},
		},
	}
	model.extensionsOpen = true
	model.selectSection(sectionExtensions)
	if model.page != pageConnector || model.selectedView != "overview" || model.selectedAction != "" {
		t.Fatalf("extension page did not select its default view: page=%v view=%q action=%q", model.page, model.selectedView, model.selectedAction)
	}
	if firstRowOfKind(model.sidebarRows(), rowConnectorView) < 0 || firstRowOfKind(model.sidebarRows(), rowConnectorAction) < 0 || firstRowOfKind(model.sidebarRows(), rowConnectorSession) < 0 {
		t.Fatal("protocol v3 surfaces were not rendered")
	}

	var customX int
	for _, span := range model.extensionSpans() {
		if span.Config.ID == "custom-probe" {
			customX = span.Start
		}
	}
	model.extensionsOpen = true
	model.handleClick(tea.MouseClickMsg{X: customX, Y: baseTopHeight, Button: tea.MouseLeft})
	if !model.extensionsOpen || model.selectedConnector != "custom-probe" {
		t.Fatal("selecting an extension closed its sticky menu")
	}
	if firstRowOfKind(model.sidebarRows(), rowConnectorView) < 0 || firstRowOfKind(model.sidebarRows(), rowConnectorAction) < 0 || firstRowOfKind(model.sidebarRows(), rowConnectorSession) >= 0 {
		t.Fatal("custom manifest surface was not honored")
	}

	var extensionX int
	for _, span := range navigationSpansFor(model.navigation, model.width) {
		if model.navigation[span.Index].Section == sectionExtensions {
			extensionX = span.Start
		}
	}
	model.handleClick(tea.MouseClickMsg{X: extensionX, Y: 1, Button: tea.MouseLeft})
	if model.extensionsOpen {
		t.Fatal("Extensions menu did not close when its top-level control was clicked again")
	}
}

func TestNavigationSpansFillTopBarEvenly(t *testing.T) {
	spans := navigationSpans(101)
	if len(spans) != len(navigation) || spans[0].Start != 0 || spans[len(spans)-1].End != 101 {
		t.Fatalf("top bar does not cover the width: %#v", spans)
	}
	minimum, maximum := 101, 0
	for _, span := range spans {
		width := span.End - span.Start
		minimum = min(minimum, width)
		maximum = max(maximum, width)
	}
	if maximum-minimum > 1 {
		t.Fatalf("top bar is not evenly spaced: %#v", spans)
	}
}

func TestFilesPageUsesFullWidthNativeNvChadDashboard(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	nvim := filepath.Join(bin, "nvim")
	if runtime.GOOS == "windows" {
		nvim += ".exe"
	}
	if err := os.WriteFile(nvim, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("# Test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := NewModel(root)
	command := model.selectSection(sectionExplorer)
	if command == nil {
		t.Fatal("Files page should start the embedded editor")
	}
	if model.page != pageFile || model.section != sectionExplorer {
		t.Fatalf("unexpected Files page state: page=%v section=%v", model.page, model.section)
	}
	if model.sidebar || len(model.sidebarRows()) != 0 {
		t.Fatal("Files must not render the duplicate Go filesystem sidebar")
	}
	if model.editorPath != "" {
		t.Fatalf("Files must start on the dashboard, got startup path %q", model.editorPath)
	}
	model.handleKey("ctrl+b")
	if model.sidebar {
		t.Fatal("Ctrl-B must not add a Lisan sidebar over native NvimTree")
	}
}

func TestViewContainsDynamicAgentState(t *testing.T) {
	model := NewModel(t.TempDir())
	model.inventory = inventory.Snapshot{Tools: []inventory.Tool{{ID: "codex", Name: "Codex", Category: "Agent CLIs", Agent: true, Installed: true, Version: "test-version"}}}
	model.loading = false
	model.selectedAgent = "codex"
	model.page = pageAgent
	view := model.View()
	if !strings.Contains(view.Content, "Codex") || !strings.Contains(view.Content, "test-version") {
		t.Fatalf("agent page missing dynamic data: %q", view.Content)
	}
}

func TestSectionCycleRetainsIndependentPaneState(t *testing.T) {
	model := NewModel(t.TempDir())
	model.inventory = inventory.Snapshot{Tools: []inventory.Tool{
		{ID: "git", Name: "Git", Category: "Development", Installed: true},
		{ID: "codex", Name: "Codex", Category: "Agent CLIs", Agent: true},
	}}

	model.selectSection(sectionOverview)
	model.selectedTool = "git"
	model.sidebarCursor = 1
	model.sidebarScroll = 1
	model.sidebar = false

	model.selectSection(sectionAgents)
	model.selectedAgent = "codex"
	model.sidebarCursor = 0
	model.sidebar = true

	model.selectSection(sectionOverview)
	if model.selectedTool != "git" || model.selectedAgent != "codex" {
		t.Fatalf("tool and agent selections were not independent: tool=%q agent=%q", model.selectedTool, model.selectedAgent)
	}
	if model.sidebar {
		t.Fatal("Overview Tools pane must return collapsed after cycling pages")
	}
	if model.sidebarCursor != 1 || model.sidebarScroll != 1 {
		t.Fatalf("Tools pane position was lost: cursor=%d scroll=%d", model.sidebarCursor, model.sidebarScroll)
	}
}

func TestToolsCategoriesStartCollapsed(t *testing.T) {
	model := NewModel(t.TempDir())
	model.inventory = inventory.Snapshot{
		Tools: []inventory.Tool{
			{ID: "git", Name: "Git", Category: "Core", Installed: true},
			{ID: "codex", Name: "Codex", Category: "Agent CLIs", Agent: true, Installed: true},
			{ID: "fish", Name: "Fish", Category: "Shell & Terminal", Installed: true},
		},
		APTManual: []inventory.Package{{Name: "curl", Version: "1"}},
	}
	model.section = sectionOverview

	for _, row := range model.sidebarRows() {
		if row.Kind != rowCategory {
			t.Fatalf("collapsed Tools sidebar exposed a child row: %#v", row)
		}
		if row.Expanded {
			t.Fatalf("Tools category starts expanded: %#v", row)
		}
	}
}

func TestExtensionCycleRetainsMenuAndSelectedAction(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	model.connectors = []connectorapi.State{{
		Config: profile.Connectors[0], Online: true,
		Manifest: connectorapi.Manifest{
			ProtocolVersion: connectorapi.ProtocolVersion,
			ID:              "test-observatory",
			Name:            "Test Observatory",
			Version:         "3.0.0",
			Views:           []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview", Default: true}},
			Actions:         []connectorapi.ActionDescriptor{{ID: "hostname", Name: "Host name"}, {ID: "system", Name: "System information"}},
		},
		Views: map[string]connectorapi.View{"overview": {ID: "overview", Title: "Overview"}},
	}}

	model.selectSection(sectionExtensions)
	rows := model.sidebarRows()
	secondAction := -1
	for index, row := range rows {
		if row.ID == "system" {
			secondAction = index
			break
		}
	}
	if secondAction < 0 {
		t.Fatal("test extension did not expose its second action")
	}
	model.selectedAction = "system"
	model.sidebarCursor = secondAction
	model.extensionsOpen = false

	model.selectSection(sectionOverview)
	model.selectSection(sectionExtensions)
	if model.extensionsOpen {
		t.Fatal("Extensions dropdown reopened even though the user had closed it")
	}
	if model.selectedAction != "system" || model.sidebarCursor != secondAction {
		t.Fatalf("extension action state was lost: action=%q cursor=%d", model.selectedAction, model.sidebarCursor)
	}
}

func TestEmbeddedEditorSessionSurvivesSectionCycle(t *testing.T) {
	model := NewModel(t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: editorSessionID, Name: "test editor", Path: executable,
		Args:       []string{"-test.run=^TestTerminalChildProcess$"},
		Dir:        model.root,
		Env:        terminalhost.Environment(os.Environ(), "LISAN_TEST_TERMINAL_CHILD=1"),
		Background: lipgloss.Color(nvimconfig.ChocolateBackground),
	}, 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[editorSessionID] = session
	model.section = sectionExplorer
	model.page = pageFile
	model.sidebar = false
	model.capture = true

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(session.Render(), "nvchad-state") && time.Now().Before(deadline) {
		_ = session.NextEvent()
	}
	if !strings.Contains(session.Render(), "nvchad-state") {
		t.Fatalf("embedded test session did not draw: %q", session.Render())
	}

	// Match the mouse path: opening Extensions adds its dropdown row before
	// Files is hidden, changing the embedded viewport height for the return.
	model.extensionsOpen = true
	model.selectSection(sectionExtensions)
	if model.sessions[editorSessionID] != session || model.capture {
		t.Fatal("leaving Files replaced the editor session or left it focused")
	}
	command := model.selectSection(sectionExplorer)
	if command != nil {
		t.Fatal("returning to Files attempted to start a replacement editor")
	}
	if model.sessions[editorSessionID] != session || !model.capture {
		t.Fatal("returning to Files did not resume the same editor session")
	}
	if !strings.Contains(session.Render(), "nvchad-state") {
		t.Fatal("embedded editor screen contents were reset during page cycling")
	}
	view := model.View()
	if !sameColor(view.BackgroundColor, lipgloss.Color(nvimconfig.ChocolateBackground)) {
		t.Fatalf("restored editor frame used wrapper background %v", view.BackgroundColor)
	}
	content, ok := model.embeddedContent(theme.All[model.themeIndex], model.mainPaneWidth(), model.mainContentHeight())
	if !ok {
		t.Fatal("restored editor was not rendered as embedded content")
	}
	backgroundSequence := ansi.Style{}.BackgroundColor(lipgloss.Color(nvimconfig.ChocolateBackground)).String()
	for index, row := range strings.Split(content, "\n")[embeddedHeaderHeight:] {
		if !strings.HasPrefix(row, backgroundSequence) {
			t.Fatalf("restored editor row %d did not begin with its own background: %q", index, row)
		}
	}
}

func TestPasteReachesCapturedTerminalAndMentatSessions(t *testing.T) {
	content := "fear is the mind-killer\nspice must flow"
	tests := []struct {
		name    string
		section section
		page    page
		session string
		agentID string
	}{
		{name: "terminal", section: sectionTerminal, page: pageTerminal, session: shellSessionID},
		{name: "mentat", section: sectionAgents, page: pageAgent, session: agentSessionID("codex"), agentID: "codex"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(t.TempDir())
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			session, err := terminalhost.Start(terminalhost.Spec{
				ID: test.session, Name: test.name, Path: executable,
				Args: []string{"-test.run=^TestTerminalChildProcess$"},
				Dir:  model.root,
				Env: terminalhost.Environment(os.Environ(),
					"LISAN_TEST_TERMINAL_CHILD=1",
					"LISAN_TEST_TERMINAL_PASTE="+content,
				),
			}, 60, 10)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			model.sessions[test.session] = session
			model.section, model.page = test.section, test.page
			model.selectedAgent = test.agentID
			if test.page == pageTerminal {
				if id := model.terminalWorkspace.newTab(); id != test.session {
					t.Fatalf("first terminal session ID = %q, want %q", id, test.session)
				}
			}
			model.capture = true
			waitForSessionText(t, session, "paste-ready")

			model.Update(tea.PasteMsg{Content: content})
			waitForSessionText(t, session, "paste-ok")
		})
	}
}

func TestLargeMentatPasteDoesNotBlockTheUI(t *testing.T) {
	model := NewModel(t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("x", 256<<10)
	id := agentSessionID("omp")
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: id, Name: "Oh My Pi", Path: executable,
		Args: []string{"-test.run=^TestTerminalChildProcess$"},
		Dir:  model.root,
		Env: terminalhost.Environment(os.Environ(),
			"LISAN_TEST_TERMINAL_CHILD=1",
			"LISAN_TEST_TERMINAL_PASTE_SIZE="+strconv.Itoa(len(content)),
		),
		Input: terminalhost.InputPolicy{WaitForBracketedPaste: true},
	}, 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[id] = session
	model.section, model.page, model.selectedAgent, model.capture = sectionAgents, pageAgent, "omp", true
	waitForSessionText(t, session, "paste-ready")

	started := time.Now()
	model.Update(tea.PasteMsg{Content: content})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("Bubble Tea update blocked for %s while queuing a large paste", elapsed)
	}
	waitForSessionText(t, session, "paste-ok")
}

func TestSplitTerminalRoutesPasteToFocusedPane(t *testing.T) {
	model := NewModel(t.TempDir())
	model.section, model.page = sectionTerminal, pageTerminal
	model.width, model.height = 100, 30
	first := model.terminalWorkspace.newTab()
	second, ok := model.terminalWorkspace.splitActive(terminalSplitVertical)
	if !ok {
		t.Fatal("could not construct split terminal")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(id, content string) *terminalhost.Session {
		t.Helper()
		width, height := model.sessionDimensions(id)
		session, startErr := terminalhost.Start(terminalhost.Spec{
			ID: id, Name: id, Path: executable,
			Args: []string{"-test.run=^TestTerminalChildProcess$"},
			Dir:  model.root,
			Env: terminalhost.Environment(os.Environ(),
				"LISAN_TEST_TERMINAL_CHILD=1",
				"LISAN_TEST_TERMINAL_PASTE="+content,
			),
		}, width, height)
		if startErr != nil {
			t.Fatal(startErr)
		}
		model.sessions[id] = session
		waitForSessionText(t, session, "paste-ready")
		return session
	}
	firstContent, secondContent := "first pane", "second pane"
	firstSession := start(first, firstContent)
	defer firstSession.Close()
	secondSession := start(second, secondContent)
	defer secondSession.Close()

	model.activateTerminalPane(first, true)
	model.Update(tea.PasteMsg{Content: firstContent})
	waitForSessionText(t, firstSession, "paste-ok")
	if strings.Contains(secondSession.Render(), "paste-ok") {
		t.Fatal("paste leaked into the inactive split")
	}
	model.activateTerminalPane(second, true)
	model.Update(tea.PasteMsg{Content: secondContent})
	waitForSessionText(t, secondSession, "paste-ok")
}

func TestPasteIntoLineInputsRemovesTerminalControls(t *testing.T) {
	model := NewModel(t.TempDir())
	model.extensionInputEdit = "survey:subject"
	model.Update(tea.PasteMsg{Content: "deep\r\ndesert\t\x1b[31m"})
	if model.extensionInputText != "deep desert [31m" {
		t.Fatalf("extension input paste = %q", model.extensionInputText)
	}

	model.extensionInputEdit = ""
	model.extensionSessionCapture = true
	model.Update(tea.PasteMsg{Content: "one\ntwo"})
	if model.extensionSessionInput != "one two" {
		t.Fatalf("extension session paste = %q", model.extensionSessionInput)
	}
}

func TestMentatSessionSupportsWrapperScrollback(t *testing.T) {
	model := NewModel(t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	id := agentSessionID("codex")
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: id, Name: "Codex", Path: executable,
		Args: []string{"-test.run=^TestTerminalChildProcess$"},
		Dir:  model.root,
		Env: terminalhost.Environment(os.Environ(),
			"LISAN_TEST_TERMINAL_CHILD=1",
			"LISAN_TEST_TERMINAL_SCROLLBACK=1",
		),
	}, 30, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	model.sessions[id] = session
	model.section, model.page = sectionAgents, pageAgent
	model.selectedAgent = "codex"
	waitForSessionText(t, session, "scroll-line-19")
	if limit := session.ScrollbackLen(); limit == 0 {
		t.Fatal("Mentat session did not retain scrollback")
	}
	if !model.moveSessionVertically(false) || model.sessionScrollY[id] == 0 {
		t.Fatal("Mentat session did not move to the top of its history")
	}
	content, ok := model.embeddedContent(theme.All[model.themeIndex], 30, 5)
	if !ok || !strings.Contains(content, "scroll-line-00") {
		t.Fatalf("Mentat history did not render its oldest output: %q", content)
	}
	if !model.moveSessionVertically(true) || model.sessionScrollY[id] != 0 {
		t.Fatal("Mentat session did not return to live output")
	}
}

func TestNativeCopyModeSelectsCellsAndRestoresInput(t *testing.T) {
	model := NewModel(t.TempDir())
	id := agentSessionID("codex")
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: id, Name: "Codex", Path: "/bin/sh",
		Args: []string{"-c", "printf 'alpha beta'; sleep 5"},
		Dir:  model.root,
	}, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[id] = session
	model.section, model.page, model.selectedAgent, model.capture = sectionAgents, pageAgent, "codex", true
	waitForSessionText(t, session, "alpha beta")

	model.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}))
	if !model.copy.Active || model.capture {
		t.Fatalf("copy mode state = %#v, capture=%v", model.copy, model.capture)
	}
	model.copy.Anchor = terminalhost.ViewportPoint{X: 0, Y: 0}
	model.copy.Focus = terminalhost.ViewportPoint{X: 4, Y: 0}
	model.copy.HasSelection = true
	content, ok := model.embeddedContent(theme.All[model.themeIndex], 40, 6)
	if !ok || !strings.Contains(content, "\x1b[7m") {
		t.Fatalf("selected cells were not highlighted: %q", content)
	}

	command := model.handleCopyKey("enter")
	if command == nil {
		t.Fatal("copying a selection returned no clipboard command")
	}
	if got := fmt.Sprint(command()); got != "alpha" {
		t.Fatalf("clipboard selection = %q, want alpha", got)
	}
	if model.copy.Active || !model.capture {
		t.Fatalf("copy completion did not restore input: copy=%#v capture=%v", model.copy, model.capture)
	}
}

func TestShiftDragStartsCopyWithoutReturningScrollbackToBottom(t *testing.T) {
	model := NewModel(t.TempDir())
	model.width, model.height = 80, 20
	model.sidebar = false
	id := agentSessionID("codex")
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: id, Name: "Codex", Path: "/bin/sh",
		Args: []string{"-c", "printf 'alpha beta'; sleep 5"},
		Dir:  model.root,
	}, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[id] = session
	model.section, model.page, model.selectedAgent, model.capture = sectionAgents, pageAgent, "codex", true
	model.sessionScrollY[id] = 3

	model.Update(tea.MouseClickMsg{
		X: 1, Y: model.topHeight() + embeddedHeaderHeight,
		Button: tea.MouseLeft, Mod: tea.ModShift,
	})
	if !model.copy.Active || !model.copy.Dragging || !model.copy.HasSelection {
		t.Fatalf("shift-drag did not enter copy selection: %#v", model.copy)
	}
	if model.sessionScrollY[id] != 3 {
		t.Fatalf("shift-drag returned scrollback to live output: y=%d", model.sessionScrollY[id])
	}
	model.leaveCopyMode(true)
}

func TestCopyModePausesAndResumesStreamingOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ConPTY sessions cannot suspend their process tree")
	}
	model := NewModel(t.TempDir())
	id := agentSessionID("codex")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: id, Name: "Codex", Path: executable,
		Args: []string{"-test.run=^TestTerminalChildProcess$"},
		Dir:  model.root,
		Env: terminalhost.Environment(os.Environ(),
			"LISAN_TEST_TERMINAL_CHILD=1",
			"LISAN_TEST_TERMINAL_STREAM=1",
		),
	}, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[id] = session
	model.section, model.page, model.selectedAgent, model.capture = sectionAgents, pageAgent, "codex", true

	deadline := time.Now().Add(3 * time.Second)
	for session.ScrollbackLen() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	model.enterCopyMode()
	if !model.copy.Paused {
		t.Fatal("copy mode did not pause the producing session")
	}
	time.Sleep(30 * time.Millisecond) // allow bytes already in the PTY to drain
	frozen := session.ScrollbackLen()
	time.Sleep(75 * time.Millisecond)
	if got := session.ScrollbackLen(); got != frozen {
		t.Fatalf("streaming output moved during copy mode: scrollback %d -> %d", frozen, got)
	}

	model.leaveCopyMode(true)
	deadline = time.Now().Add(3 * time.Second)
	for session.ScrollbackLen() <= frozen && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := session.ScrollbackLen(); got <= frozen {
		t.Fatalf("streaming output did not resume after copy mode: scrollback %d -> %d", frozen, got)
	}
}

func TestNeovimThemeCommandAndLiveBackgroundFollowLisanTheme(t *testing.T) {
	t.Setenv(appconfig.EnvironmentConfig, filepath.Join(t.TempDir(), "config.json"))
	model := NewModel(t.TempDir())
	model.themeIndex = 0
	initial := theme.All[0].NeovimTheme()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := terminalhost.Start(terminalhost.Spec{
		ID: editorSessionID, Name: "NvChad", Path: executable,
		Args: []string{"-test.run=^TestTerminalChildProcess$"},
		Dir:  model.root,
		Env: terminalhost.Environment(os.Environ(),
			"LISAN_TEST_TERMINAL_CHILD=1",
		),
		Background: lipgloss.Color(initial.Background),
	}, 40, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	model.sessions[editorSessionID] = session

	model.cycleTheme(1)
	want := theme.All[1]
	paired := want.NeovimTheme()
	if model.themeIndex != 1 {
		t.Fatalf("theme index = %d, want 1", model.themeIndex)
	}
	if !sameColor(session.BackgroundColor(), lipgloss.Color(paired.Background)) {
		t.Fatalf("NvChad background did not follow %s", want.Name)
	}
	lua := nvimThemeLua(paired.Name)
	if !strings.Contains(lua, `c.theme="bearded-arc"`) || !strings.Contains(lua, "load_all_highlights") {
		t.Fatalf("unexpected live NvChad theme command: %q", lua)
	}
	if startup := nvimThemeStartupCommand(paired.Name); !strings.Contains(startup, lua) || !strings.Contains(startup, "VimEnter") {
		t.Fatalf("unexpected startup NvChad theme command: %q", startup)
	}
}

func waitForSessionText(t *testing.T, session *terminalhost.Session, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(session.Render(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("embedded session never rendered %q: %q", want, session.Render())
}

func TestTerminalChildProcess(t *testing.T) {
	if os.Getenv("LISAN_TEST_TERMINAL_CHILD") != "1" {
		return
	}
	content := os.Getenv("LISAN_TEST_TERMINAL_PASTE")
	if rawSize := os.Getenv("LISAN_TEST_TERMINAL_PASTE_SIZE"); rawSize != "" {
		size, err := strconv.Atoi(rawSize)
		if err != nil || size <= 0 {
			_, _ = fmt.Fprintf(os.Stdout, "paste-size-error:%q", rawSize)
			os.Exit(1)
		}
		content = strings.Repeat("x", size)
	}
	if content != "" {
		state, err := term.MakeRaw(os.Stdin.Fd())
		if err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "paste-raw-error:%v", err)
			os.Exit(1)
		}
		defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
		_, _ = os.Stdout.WriteString("\x1b[?2004hpaste-ready")
		want := ansi.BracketedPasteStart + content + ansi.BracketedPasteEnd
		buffer := make([]byte, len(want))
		if _, err := io.ReadFull(os.Stdin, buffer); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "paste-read-error:%v", err)
			os.Exit(1)
		}
		if string(buffer) != want {
			_, _ = fmt.Fprintf(os.Stdout, "paste-mismatch:%q", string(buffer))
			os.Exit(1)
		}
		_, _ = os.Stdout.WriteString("\r\npaste-ok")
		return
	}
	if os.Getenv("LISAN_TEST_TERMINAL_SCROLLBACK") == "1" {
		for index := range 20 {
			_, _ = fmt.Fprintf(os.Stdout, "scroll-line-%02d\r\n", index)
		}
		time.Sleep(10 * time.Second)
		return
	}
	if os.Getenv("LISAN_TEST_TERMINAL_STREAM") == "1" {
		for index := range 200 {
			_, _ = fmt.Fprintf(os.Stdout, "stream-line-%03d\r\n", index)
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(10 * time.Second)
		return
	}
	_, _ = os.Stdout.WriteString("\x1b[41mnvchad-state\x1b[0m")
	time.Sleep(10 * time.Second)
	os.Exit(0)
}

func sameColor(left, right color.Color) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftR, leftG, leftB, leftA := left.RGBA()
	rightR, rightG, rightB, rightA := right.RGBA()
	return leftR == rightR && leftG == rightG && leftB == rightB && leftA == rightA
}

func TestEmbeddedTerminalScreenFillsItsViewport(t *testing.T) {
	const (
		width  = 24
		height = 4
	)
	rendered := renderTerminalScreen(
		"\x1b[38;2;255;0;0mNvChad\x1b[0m",
		lipgloss.Color(nvimconfig.ChocolateBackground),
		width,
		height,
	)
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("terminal viewport has %d rows, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("terminal viewport row %d has width %d, want %d", index, got, width)
		}
	}
	backgroundSequence := ansi.Style{}.BackgroundColor(lipgloss.Color(nvimconfig.ChocolateBackground)).String()
	if !strings.Contains(lines[0], "\x1b[0m"+backgroundSequence+strings.Repeat(" ", width-len("NvChad"))) {
		t.Fatalf("terminal reset was not followed by explicitly painted trailing cells: %q", lines[0])
	}
	blankRow := backgroundSequence + strings.Repeat(" ", width) + ansi.ResetStyle
	for index, line := range lines[1:] {
		if line != blankRow {
			t.Fatalf("blank terminal row %d was not painted with the editor background: %q", index+1, line)
		}
	}
}

func TestEmbeddedTerminalScreenUsesTheNaturalLeftEdge(t *testing.T) {
	rendered := renderTerminalScreen(
		"\x1b[38;2;255;0;0m0123456789\x1b[0m",
		lipgloss.Color(nvimconfig.ChocolateBackground),
		4,
		1,
	)
	if !strings.Contains(rendered, "0123") || strings.Contains(rendered, "4567") {
		t.Fatalf("terminal renderer did not keep the natural left edge: %q", rendered)
	}
	if width := lipgloss.Width(rendered); width != 4 {
		t.Fatalf("terminal viewport width = %d, want 4", width)
	}
}

func TestEmbeddedTerminalScreenBoundsWideAndCombiningGraphemes(t *testing.T) {
	const width = 7
	rendered := renderTerminalScreen(
		"a😀界e\u0301\x1b]8;;https://example.invalid\x1b\\linked\x1b]8;;\x1b\\",
		lipgloss.Color(nvimconfig.ChocolateBackground),
		width,
		2,
	)
	for index, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("Unicode terminal row %d has width %d, want %d: %q", index, got, width, line)
		}
	}
}

func TestExtensionOutputWrapsToThePane(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	model.width = 32
	model.height = 24
	model.section = sectionExtensions
	model.page = pageConnector
	model.sidebar = false
	model.selectedConnector = profile.Connectors[0].ID
	model.connectors = []connectorapi.State{{
		Config: profile.Connectors[0],
		Online: true,
		Manifest: connectorapi.Manifest{
			ProtocolVersion: connectorapi.ProtocolVersion,
			ID:              profile.Connectors[0].ID,
			Name:            profile.Connectors[0].Name,
			Version:         "3.0.0",
			Views:           []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview"}},
			Actions:         []connectorapi.ActionDescriptor{{ID: "wrapped", Name: "Wrapped"}},
		},
	}}
	longLine := "  │ " + strings.Repeat("a", 70) + "TAIL"
	model.connectorJobs[profile.Connectors[0].ID] = connectorapi.Job{ID: "job", ActionID: "wrapped", Status: connectorapi.JobSucceeded, Progress: 100, Result: strings.TrimPrefix(longLine, "  │ ")}
	model.selectedAction = "wrapped"

	width, height := model.mainPaneWidth(), model.mainContentHeight()
	rendered := model.renderMain(theme.All[0], width, height)
	if !strings.Contains(rendered, "TAIL") {
		t.Fatalf("wrapped extension tail was not visible: %q", rendered)
	}
	for index, line := range wrapExtensionLines([]string{longLine}, width-1) {
		if cells := visibleWidth(line); cells > width-1 {
			t.Fatalf("wrapped extension row %d has width %d, want at most %d", index, cells, width-1)
		}
	}
}

func TestExtensionOutputSupportsVerticalKeysAndWheel(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	model.width = 72
	model.height = 14
	model.section = sectionExtensions
	model.page = pageConnector
	model.sidebar = false
	model.selectedConnector = profile.Connectors[0].ID
	model.connectors = []connectorapi.State{{
		Config: profile.Connectors[0],
		Online: true,
		Manifest: connectorapi.Manifest{
			ProtocolVersion: connectorapi.ProtocolVersion,
			ID:              profile.Connectors[0].ID,
			Name:            profile.Connectors[0].Name,
			Version:         "3.0.0",
			Views:           []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview"}},
		},
	}}
	var output []string
	for index := range 30 {
		output = append(output, fmt.Sprintf("line-%02d", index))
	}
	model.connectorJobs[profile.Connectors[0].ID] = connectorapi.Job{ID: "job", ActionID: "long", Status: connectorapi.JobSucceeded, Progress: 100, Logs: output}
	model.selectedAction = "long"

	width, height := model.mainPaneWidth(), model.mainContentHeight()
	if initial := model.renderMain(theme.All[0], width, height); !strings.Contains(initial, "line-00") || strings.Contains(initial, "line-29") {
		t.Fatalf("extension did not start at the top: %q", initial)
	}
	model.handleWheel(tea.MouseWheelMsg{X: width - 1, Y: 8, Button: tea.MouseWheelDown})
	if model.extensionScrollY != verticalScrollStep {
		t.Fatalf("vertical wheel moved extension output to %d", model.extensionScrollY)
	}
	model.handleKey("pgdown")
	if model.extensionScrollY <= verticalScrollStep {
		t.Fatalf("PageDown did not advance extension output: %d", model.extensionScrollY)
	}
	model.handleKey("end")
	if rendered := model.renderMain(theme.All[0], width, height); !strings.Contains(rendered, "line-29") {
		t.Fatalf("extension bottom was not reachable: %q", rendered)
	}
	model.handleKey("home")
	if model.extensionScrollY != 0 {
		t.Fatalf("Home left extension offset at %d", model.extensionScrollY)
	}
}

func TestExtensionTypedInputsAreGenericCoreState(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	inputs := []connectorapi.InputSpec{
		{ID: "subject", Label: "Subject", Kind: connectorapi.InputText, Default: "spice"},
		{ID: "deep", Label: "Deep", Kind: connectorapi.InputBoolean, Default: "false"},
		{ID: "detail", Label: "Detail", Kind: connectorapi.InputSelect, Default: "brief", Options: []connectorapi.InputOption{{Value: "brief", Label: "Brief"}, {Value: "deep", Label: "Deep"}}},
	}
	state := connectorapi.State{Config: profile.Connectors[0], Online: true, Manifest: connectorapi.Manifest{
		ProtocolVersion: connectorapi.ProtocolVersion, ID: profile.Connectors[0].ID, Name: profile.Connectors[0].Name, Version: "3.0.0",
		Views:   []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview"}},
		Actions: []connectorapi.ActionDescriptor{{ID: "survey", Name: "Survey", Inputs: inputs}},
	}}
	model.connectors = []connectorapi.State{state}
	model.seedConnectorInputs(state)
	model.selectedConnector, model.selectedAction = state.Config.ID, "survey"
	model.beginConnectorInput("survey", "deep")
	model.beginConnectorInput("survey", "detail")
	model.beginConnectorInput("survey", "subject")
	model.handleExtensionInputKey("backspace")
	model.handleExtensionInputKey("enter")
	values := model.connectorActionInputs(state.Config.ID, "survey")
	if values["deep"] != "true" || values["detail"] != "deep" || values["subject"] != "spic" {
		t.Fatalf("typed inputs were not edited by kind: %#v", values)
	}
}

func TestExtensionControlsHaveCoreOwnedAffordances(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	inputs := []connectorapi.InputSpec{
		{ID: "subject", Label: "Subject", Kind: connectorapi.InputText, Default: "spice"},
		{ID: "count", Label: "Count", Kind: connectorapi.InputNumber, Default: "2"},
		{ID: "deep", Label: "Deep scan", Kind: connectorapi.InputBoolean, Default: "true"},
		{ID: "detail", Label: "Detail", Kind: connectorapi.InputSelect, Default: "brief", Options: []connectorapi.InputOption{{Value: "brief", Label: "Brief"}}},
	}
	state := connectorapi.State{Config: profile.Connectors[0], Online: true, Manifest: connectorapi.Manifest{
		ProtocolVersion: connectorapi.ProtocolVersion, ID: profile.Connectors[0].ID, Name: profile.Connectors[0].Name, Version: "3.0.0",
		Views:    []connectorapi.ViewDescriptor{{ID: "overview", Title: "Overview"}},
		Actions:  []connectorapi.ActionDescriptor{{ID: "survey", Name: "Survey", Inputs: inputs}, {ID: "report", Name: "Report", Inputs: inputs[:1]}},
		Sessions: []connectorapi.SessionDescriptor{{ID: "console", Name: "Field console"}},
	}}
	model.connectors = []connectorapi.State{state}
	model.seedConnectorInputs(state)
	model.selectedConnector, model.selectedView, model.selectedAction = state.Config.ID, "", "survey"
	model.connectorJobs[state.Config.ID] = connectorapi.Job{Artifacts: []connectorapi.Artifact{{ID: "report", Name: "report.txt", Size: 42}}}

	rows := model.connectorRows()
	joined := ""
	for _, row := range rows {
		joined += row.Label + " " + row.Subtitle + "\n"
		if row.Kind == rowCategory && strings.Contains(row.Label, "[ ") {
			t.Fatalf("collapse category looks like an action button: %#v", row)
		}
	}
	for _, expected := range []string{
		"○ VIEW  Overview", "● Survey", "○ Report", "○ Field console", "○ report.txt",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("core affordances omitted %q:\n%s", expected, joined)
		}
	}
	for _, clutter := range []string{"[ RUN ]", "[ SET ]", "[ OPEN ]", "[ SAVE ]", "TEXT", "NUMBER", "TOGGLE", "SELECT"} {
		if strings.Contains(joined, clutter) {
			t.Fatalf("sidebar contains main-pane control %q:\n%s", clutter, joined)
		}
	}

	rendered := ansi.Strip(strings.Join(model.extensionActionLines(state), "\n"))
	for _, expected := range []string{"TEXT  Subject", "NUMBER  Count", "TOGGLE  Deep scan", "SELECT  Detail", "RUN SURVEY"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("action form omitted %q:\n%s", expected, rendered)
		}
	}
}

func TestExtensionMainPaneControlsAreClickable(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	model.width, model.height = 100, 32
	model.section, model.page, model.sidebar = sectionExtensions, pageConnector, true
	inputs := []connectorapi.InputSpec{
		{ID: "subject", Label: "Subject", Kind: connectorapi.InputText, Default: "spice"},
		{ID: "count", Label: "Count", Kind: connectorapi.InputNumber, Default: "2"},
		{ID: "deep", Label: "Deep scan", Kind: connectorapi.InputBoolean, Default: "false"},
		{ID: "detail", Label: "Detail", Kind: connectorapi.InputSelect, Default: "brief", Options: []connectorapi.InputOption{{Value: "brief", Label: "Brief"}, {Value: "deep", Label: "Deep"}}},
	}
	state := connectorapi.State{Config: profile.Connectors[0], Online: true, Manifest: connectorapi.Manifest{
		ProtocolVersion: connectorapi.ProtocolVersion, ID: profile.Connectors[0].ID, Name: profile.Connectors[0].Name, Version: "3.0.0",
		Actions: []connectorapi.ActionDescriptor{{ID: "survey", Name: "Survey", Inputs: inputs}},
	}}
	model.connectors = []connectorapi.State{state}
	model.seedConnectorInputs(state)
	model.selectedConnector, model.selectedAction = state.Config.ID, "survey"

	clickControl := func(index int) tea.Cmd {
		document := model.visibleConnectorDocument(max(model.mainPaneWidth()-1, 1), model.mainContentHeight())
		for y, line := range document {
			if !line.Interactive || line.ControlIndex != index {
				continue
			}
			plain := ansi.Strip(line.Text)
			start := visibleWidth(plain) - visibleWidth(strings.TrimLeft(plain, " "))
			return model.handleClick(tea.MouseClickMsg{X: sidebarWidth + start, Y: model.topHeight() + y, Button: tea.MouseLeft})
		}
		t.Fatalf("control %d was not visible", index)
		return nil
	}

	clickControl(0)
	if model.extensionInputEdit != "survey:subject" {
		t.Fatalf("text field click did not enter editing: %q", model.extensionInputEdit)
	}
	model.handleExtensionInputKey("esc")
	clickControl(2)
	if got := model.connectorInputValue(state.Config.ID, "survey", "deep"); got != "true" {
		t.Fatalf("toggle click produced %q, want true", got)
	}
	clickControl(3)
	if got := model.connectorInputValue(state.Config.ID, "survey", "detail"); got != "deep" {
		t.Fatalf("select click produced %q, want deep", got)
	}
	if cmd := clickControl(4); cmd == nil || !model.connectorRunning[state.Config.ID] {
		t.Fatal("dedicated Run button did not start the selected action")
	}
}

func TestExtensionSidebarSelectionNeverExecutes(t *testing.T) {
	profile := testExtensionProfile()
	model := NewModelWithProfile(t.TempDir(), profile)
	state := connectorapi.State{Config: profile.Connectors[0], Online: true, Manifest: connectorapi.Manifest{
		ProtocolVersion: connectorapi.ProtocolVersion, ID: profile.Connectors[0].ID, Name: profile.Connectors[0].Name, Version: "3.0.0",
		Actions:  []connectorapi.ActionDescriptor{{ID: "survey", Name: "Survey"}},
		Sessions: []connectorapi.SessionDescriptor{{ID: "console", Name: "Console"}},
	}}
	model.connectors = []connectorapi.State{state}
	model.selectedConnector = state.Config.ID
	rows := model.connectorRows()

	action := firstRowOfKind(rows, rowConnectorAction)
	if cmd := model.activateRow(rows, action); cmd != nil || model.connectorRunning[state.Config.ID] {
		t.Fatal("selecting an action in the sidebar executed it")
	}
	rows = model.connectorRows()
	session := firstRowOfKind(rows, rowConnectorSession)
	if cmd := model.activateRow(rows, session); cmd != nil {
		t.Fatal("selecting a session in the sidebar opened it")
	}
	if controls := model.extensionMainControls(); len(controls) != 1 || controls[0].Kind != extensionControlOpen {
		t.Fatalf("session did not expose one dedicated main-pane Open button: %#v", controls)
	}
}

func TestControlCQuitsOverviewBeforeStaleCapture(t *testing.T) {
	model := NewModel(t.TempDir())
	model.section, model.page = sectionOverview, pageOverview
	model.extensionSessionCapture = true
	model.extensionInputEdit = "stale:field"
	_, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("Ctrl-C returned no quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Ctrl-C returned %T, want tea.QuitMsg", cmd())
	}
}

func TestViewRequestsCapabilityNeutralInputEnhancements(t *testing.T) {
	model := NewModel(t.TempDir())
	view := model.View()
	if !view.ReportFocus {
		t.Fatal("view did not request host focus events")
	}
	if !view.KeyboardEnhancements.ReportAlternateKeys ||
		!view.KeyboardEnhancements.ReportAllKeysAsEscapeCodes ||
		!view.KeyboardEnhancements.ReportAssociatedText {
		t.Fatalf("view did not request complete semantic key data: %#v", view.KeyboardEnhancements)
	}
}

func TestExtensionControlPaletteUsesExistingThemeRoles(t *testing.T) {
	theme := theme.All[0]
	tests := []struct {
		name string
		row  sidebarRow
		want string
	}{
		{name: "collapse", row: sidebarRow{Kind: rowCategory}, want: theme.Secondary},
		{name: "selected action", row: sidebarRow{Kind: rowConnectorAction, Active: true}, want: theme.Primary},
		{name: "selected artifact", row: sidebarRow{Kind: rowConnectorArtifact, Active: true}, want: theme.Success},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			foreground, _, _ := sidebarRowPalette(test.row, theme)
			if foreground != test.want {
				t.Fatalf("foreground %q, want theme role %q", foreground, test.want)
			}
		})
	}
}

func TestExtensionStructuredBlocksRenderWithoutDomainBranches(t *testing.T) {
	blocks := []connectorapi.Block{
		{ID: "status", Kind: connectorapi.BlockStatus, Tone: connectorapi.ToneSuccess, Text: "ready"},
		{ID: "fields", Kind: connectorapi.BlockKeyValue, Fields: []connectorapi.FieldValue{{Label: "Region", Value: "Arrakeen"}}},
		{ID: "list", Kind: connectorapi.BlockList, Items: []connectorapi.ListItem{{Label: "Trace", Detail: "stable"}}},
		{ID: "table", Kind: connectorapi.BlockTable, Columns: []connectorapi.Column{{ID: "id", Title: "ID"}}, Rows: [][]string{{"01"}}},
		{ID: "progress", Kind: connectorapi.BlockProgress, Progress: 50, Detail: "sampling"},
	}
	var rendered []string
	for _, block := range blocks {
		rendered = append(rendered, renderExtensionBlock(block)...)
	}
	joined := strings.Join(rendered, "\n")
	for _, expected := range []string{"ready", "Arrakeen", "stable", "01", "50%", "sampling"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("structured renderer omitted %q: %s", expected, joined)
		}
	}
}

func TestSmallTerminalUsesItsActualDimensions(t *testing.T) {
	model := NewModel(t.TempDir())
	model.Update(tea.WindowSizeMsg{Width: 12, Height: 3})
	view := model.View()
	lines := strings.Split(view.Content, "\n")
	if len(lines) != 3 {
		t.Fatalf("compact view has %d lines, want 3", len(lines))
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width != model.wrapSafeWidth() {
			t.Fatalf("compact row %d has width %d, want wrap-safe width %d", index, width, model.wrapSafeWidth())
		}
	}
}

func TestNarrowTerminalHidesSidebarWithoutShrinkingMainPastViewport(t *testing.T) {
	model := NewModel(t.TempDir())
	model.sidebar = true
	model.width = sidebarWidth + 7
	if model.sidebarDrawn() || model.mainPaneWidth() != model.wrapSafeWidth() {
		t.Fatalf("narrow layout still drew sidebar: drawn=%v main=%d width=%d", model.sidebarDrawn(), model.mainPaneWidth(), model.width)
	}
	model.width = sidebarWidth + 8
	if model.sidebarDrawn() || model.mainPaneWidth() != model.wrapSafeWidth() {
		t.Fatalf("reserved final column was counted as sidebar space: drawn=%v main=%d", model.sidebarDrawn(), model.mainPaneWidth())
	}
	model.width = sidebarWidth + 9
	if !model.sidebarDrawn() || model.mainPaneWidth() != 8 {
		t.Fatalf("wide-enough layout did not restore sidebar: drawn=%v main=%d", model.sidebarDrawn(), model.mainPaneWidth())
	}
}

func TestTextTruncationUsesTerminalCellWidth(t *testing.T) {
	got := trimRunes("界界", 3)
	if width := lipgloss.Width(got); width > 3 {
		t.Fatalf("wide text overflowed: %q has width %d", got, width)
	}
}

func TestArrakisIsDefaultTheme(t *testing.T) {
	t.Setenv(appconfig.EnvironmentConfig, filepath.Join(t.TempDir(), "config.json"))
	model := NewModel(t.TempDir())
	if model.themeIndex != 0 {
		t.Fatalf("expected Arrakis theme index 0, got %d", model.themeIndex)
	}
}

func TestVimEditCommandEscapesSingleQuote(t *testing.T) {
	path := "/tmp/paul's\r:q!.go"
	got := vimEditCommand(path)
	if strings.Contains(got, path) || strings.Count(got, "\r") != 1 || !strings.Contains(got, "2f746d70") {
		t.Fatalf("editor path was not encoded safely: %q", got)
	}
}

func TestAgentWorkingDirUsesPreparedWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, id := range appconfig.AgentIDs() {
		want := filepath.Join(root, "agents", id)
		if err := os.MkdirAll(want, 0o700); err != nil {
			t.Fatal(err)
		}
		if got := agentWorkingDir(root, id); got != want {
			t.Fatalf("%s working directory = %q, want %q", id, got, want)
		}
	}
	if got := agentWorkingDir(root, "missing"); got != root {
		t.Fatalf("missing prepared workspace should fall back to root, got %q", got)
	}
}

func testExtensionProfile() appconfig.Profile {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.Connectors = []appconfig.ConnectorConfig{{
		ID: "test-observatory", Name: "Test Observatory", Icon: "󰆤", Enabled: true, Managed: true,
		Bundle: "extensions/test-observatory", Version: "3.0.0", Image: "fixture:3", BuildContext: ".",
		Dockerfile: "extensions/test-observatory/Dockerfile", NativeExecutable: "extensions/test-observatory/bin/test",
		Container: "lisan-test-observatory", User: "65532:65532", Network: "arrakis-extension-control",
		Endpoint: "http://lisan-test-observatory:7777",
	}}
	return profile
}
