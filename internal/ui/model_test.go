package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"lisanalgaib/internal/appconfig"
	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/nvimconfig"
	terminalhost "lisanalgaib/internal/terminal"
	"lisanalgaib/internal/theme"
)

func TestMouseTopBarAndTheme(t *testing.T) {
	model := NewModel(t.TempDir())
	model.inventory = inventory.Snapshot{Tools: []inventory.Tool{{ID: "codex", Name: "Codex", Category: "Agent CLIs", Agent: true, Installed: true}}}
	model.loading = false
	model.width = 100
	model.height = 30

	toolX := 0
	for _, span := range navigationSpansFor(model.navigation, model.width) {
		if model.navigation[span.Index].Section == sectionTools {
			toolX = span.Start
		}
	}
	model.Update(tea.MouseClickMsg{X: toolX, Y: 1, Button: tea.MouseLeft})
	if model.section != sectionTools || !model.sidebar {
		t.Fatalf("mouse did not select tools section: %#v", model)
	}
	before := model.themeIndex
	model.Update(tea.MouseClickMsg{X: 99, Y: 0, Button: tea.MouseLeft})
	if model.themeIndex == before {
		t.Fatal("top-right click should cycle theme")
	}
}

func TestRepeatedTopClickTogglesSidebarExceptFilesAndExtensions(t *testing.T) {
	model := NewModel(t.TempDir())
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

	toolsX := topX(sectionTools)
	model.handleClick(tea.MouseClickMsg{X: toolsX, Y: 1, Button: tea.MouseLeft})
	if model.section != sectionTools || !model.sidebar {
		t.Fatal("first Tools click did not select its sidebar")
	}
	model.handleClick(tea.MouseClickMsg{X: toolsX, Y: 1, Button: tea.MouseLeft})
	if model.sidebar {
		t.Fatal("second Tools click did not collapse its sidebar")
	}
	model.handleClick(tea.MouseClickMsg{X: toolsX, Y: 1, Button: tea.MouseLeft})
	if !model.sidebar {
		t.Fatal("third Tools click did not restore its sidebar")
	}

	filesX := topX(sectionExplorer)
	model.section = sectionExplorer
	model.page = pageFile
	model.sidebar = false
	model.handleClick(tea.MouseClickMsg{X: filesX, Y: 1, Button: tea.MouseLeft})
	if model.sidebar {
		t.Fatal("repeated Files click added a wrapper sidebar over NvChad")
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
	if len(message.Inventory.Tools) != 0 || len(message.Inventory.APTManual) != 0 || len(message.Skills) != 0 {
		t.Fatalf("minimal profile scanned disabled dependencies: %#v", message)
	}
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

func TestOverviewUsesSharedASCIIArtwork(t *testing.T) {
	model := NewModel(t.TempDir())
	model.width = 120
	model.height = 40
	art := fitASCII(overviewASCII, model.mainPaneWidth()-4, model.mainContentHeight()-2)
	if len(art) < 6 {
		t.Fatalf("overview artwork was not fitted: %#v", art)
	}
	if content := model.View().Content; !strings.Contains(content, art[0]) {
		t.Fatal("Overview does not render the shared ascii.txt artwork")
	}
	viewLines := strings.Split(model.View().Content, "\n")
	sidebar := model.renderSidebar(theme.All[model.themeIndex], model.mainContentHeight())
	if width := lipgloss.Width(sidebar); width != sidebarWidth {
		t.Fatalf("Overview sidebar has width %d, want configured width %d", width, sidebarWidth)
	}
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
	rows := model.sidebarRows()
	joined := ""
	for _, row := range rows {
		joined += row.Label + "\n"
		if row.Kind != rowInfo {
			t.Fatalf("Overview sidebar contains an interactive page row: %#v", row)
		}
	}
	if !strings.Contains(joined, "Scanning tools") || !strings.Contains(joined, "SHORTCUTS") {
		t.Fatalf("Overview status rail is incomplete: %q", joined)
	}
}

func TestOverviewArtworkUsesOneCanvasAtEveryWidth(t *testing.T) {
	for _, width := range []int{72, 240} {
		art := fitASCII(overviewASCII, width, 100)
		if len(art) == 0 {
			t.Fatalf("artwork was empty at width %d", width)
		}
		expectedWidth := lipgloss.Width(art[0])
		if expectedWidth != min(width, 118) {
			t.Fatalf("artwork width is %d at limit %d, want %d", expectedWidth, width, min(width, 118))
		}
		for index, line := range art {
			if lineWidth := lipgloss.Width(line); lineWidth != expectedWidth {
				t.Fatalf("artwork row %d has width %d at limit %d, want shared canvas width %d", index, lineWidth, width, expectedWidth)
			}
		}
	}

	const (
		viewportWidth  = 240
		viewportHeight = 60
	)
	art := fitASCII(overviewASCII, viewportWidth-4, viewportHeight-2)
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

func TestExtensionsUseOneStickyMenuAndManifestDrivenPanels(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
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
		if item.Name == "Ornithopter" || item.Name == "Custom Probe" {
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
				ProtocolVersion: connectorapi.ProtocolVersion, ID: "host-check", Name: "Ornithopter",
				UI: connectorapi.UIConfig{
					Sidebar: []connectorapi.Panel{
						{ID: "tools", Title: "Tools", Kind: connectorapi.PanelTools, Expanded: true},
						{ID: "actions", Title: "Actions", Kind: connectorapi.PanelActions, Expanded: true},
					},
					Main: []connectorapi.Panel{{ID: "summary", Title: "Summary", Kind: connectorapi.PanelSummary}},
				},
				Tools:   []connectorapi.Tool{{ID: "uname", Name: "uname", Ready: true}},
				Actions: []connectorapi.Action{{ID: "hostname", Name: "Host name"}},
			},
		},
		{
			Config: profile.Connectors[1], Online: true,
			Manifest: connectorapi.Manifest{
				ProtocolVersion: connectorapi.ProtocolVersion, ID: "custom-probe", Name: "Custom Probe",
				UI: connectorapi.UIConfig{
					Sidebar: []connectorapi.Panel{{ID: "actions", Title: "Diagnostics", Kind: connectorapi.PanelActions, Expanded: true}},
					Main:    []connectorapi.Panel{{ID: "output", Title: "Output", Kind: connectorapi.PanelActionOutput}},
				},
				Actions: []connectorapi.Action{{ID: "probe", Name: "Probe"}},
			},
		},
	}
	model.extensionsOpen = true
	model.selectSection(sectionExtensions)
	if model.page != pageConnector || model.selectedAction != "hostname" {
		t.Fatalf("extension page did not select its advertised action: page=%v action=%q", model.page, model.selectedAction)
	}
	if firstRowOfKind(model.sidebarRows(), rowConnectorTool) < 0 || firstRowOfKind(model.sidebarRows(), rowConnectorAction) < 0 {
		t.Fatal("Ornithopter manifest panels were not rendered")
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
	if firstRowOfKind(model.sidebarRows(), rowConnectorTool) >= 0 || firstRowOfKind(model.sidebarRows(), rowConnectorAction) < 0 {
		t.Fatal("custom actions-only layout was not honored")
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

	model.selectSection(sectionTools)
	model.selectedTool = "git"
	model.sidebarCursor = 1
	model.sidebarScroll = 1
	model.sidebar = false

	model.selectSection(sectionAgents)
	model.selectedAgent = "codex"
	model.sidebarCursor = 0
	model.sidebar = true

	model.selectSection(sectionTools)
	if model.selectedTool != "git" || model.selectedAgent != "codex" {
		t.Fatalf("tool and agent selections were not independent: tool=%q agent=%q", model.selectedTool, model.selectedAgent)
	}
	if model.sidebar {
		t.Fatal("Tools sidebar collapse state was lost after cycling pages")
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
	model.section = sectionTools

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
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	model := NewModelWithProfile(t.TempDir(), profile)
	model.connectors = []connectorapi.State{{
		Config: profile.Connectors[0], Online: true,
		Manifest: connectorapi.Manifest{
			ProtocolVersion: connectorapi.ProtocolVersion,
			ID:              "host-check",
			Name:            "Ornithopter",
			UI: connectorapi.UIConfig{Sidebar: []connectorapi.Panel{{
				ID: "actions", Title: "Actions", Kind: connectorapi.PanelActions, Expanded: true,
			}}},
			Actions: []connectorapi.Action{{ID: "hostname", Name: "Host name"}, {ID: "system", Name: "System information"}},
		},
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

	model.selectSection(sectionTools)
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

func TestTerminalChildProcess(t *testing.T) {
	if os.Getenv("LISAN_TEST_TERMINAL_CHILD") != "1" {
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
	want := filepath.Join(root, "agents", "codex")
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := agentWorkingDir(root, "codex"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := agentWorkingDir(root, "missing"); got != root {
		t.Fatalf("missing prepared workspace should fall back to root, got %q", got)
	}
}
