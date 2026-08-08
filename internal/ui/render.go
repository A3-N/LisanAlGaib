package ui

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/textsafe"
	"lisanalgaib/internal/theme"
)

func (m *Model) View() tea.View {
	currentTheme := theme.All[m.themeIndex]
	if m.wrapSafeWidth() < 16 || m.height < 5 {
		return m.compactView(currentTheme)
	}
	bodyHeight := m.mainContentHeight()
	mainWidth := m.mainPaneWidth()

	top := m.renderTop(currentTheme)
	main := m.renderMain(currentTheme, mainWidth, bodyHeight)
	var parts []string
	if m.sidebarDrawn() {
		parts = append(parts, m.renderSidebar(currentTheme, bodyHeight))
	}
	parts = append(parts, main)
	body := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	footer := m.renderFooter(currentTheme)
	// Do not use JoinVertical here: it pads narrower blocks to the widest row.
	// The body deliberately leaves the terminal's final column untouched, so
	// padding it back to the top bar width would reintroduce Windows EOL wrap.
	content := strings.Join([]string{top, body, footer}, "\n")

	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "LisanAlGaib"
	view.BackgroundColor = m.frameBackground(currentTheme)
	view.ForegroundColor = lipgloss.Color(currentTheme.Text)
	view.Cursor = m.embeddedCursor()
	return view
}

func (m *Model) compactView(currentTheme theme.Theme) tea.View {
	wrapSafeWidth := m.wrapSafeWidth()
	content := lipgloss.NewStyle().Width(wrapSafeWidth).Height(m.height).
		Foreground(lipgloss.Color(currentTheme.Primary)).
		Background(lipgloss.Color(currentTheme.Background)).
		Render(trimRunes(" Resize terminal", wrapSafeWidth))
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "LisanAlGaib"
	view.BackgroundColor = m.frameBackground(currentTheme)
	view.ForegroundColor = lipgloss.Color(currentTheme.Text)
	return view
}

// frameBackground is also the terminal's default background for this frame.
// Full-screen children commonly reset attributes or erase cells instead of
// painting every trailing column. When an embedded session is visible those
// cells must resolve to the child's background, not the wrapper theme beneath
// it.
func (m *Model) frameBackground(currentTheme theme.Theme) color.Color {
	if session, _ := m.visibleSession(); session != nil {
		if background := session.BackgroundColor(); background != nil {
			return background
		}
	}
	return lipgloss.Color(currentTheme.Background)
}

func (m *Model) renderTop(t theme.Theme) string {
	left := "  LISANALGAIB  //  " + strings.ToUpper(m.sectionName())
	right := fmt.Sprintf("F2 Theme: %s  ", t.Name)
	space := max(m.width-visibleWidth(left)-visibleWidth(right), 1)
	brandLine := left + strings.Repeat(" ", space) + right
	brand := lipgloss.NewStyle().Width(m.width).Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Panel)).Bold(true).Render(trimRunes(brandLine, m.width))

	navStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted)).Background(lipgloss.Color(t.Surface))
	parts := make([]string, 0, len(m.navigation))
	for _, span := range navigationSpansFor(m.navigation, m.width) {
		item := m.navigation[span.Index]
		segmentWidth := span.End - span.Start
		label := trimRunes(item.Icon+"  "+item.Name, max(segmentWidth-2, 1))
		style := navStyle
		if item.Section == m.section {
			style = style.Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Selection)).Bold(true)
		}
		parts = append(parts, style.Width(segmentWidth).Align(lipgloss.Center).Render(label))
	}
	lines := []string{brand, lipgloss.JoinHorizontal(lipgloss.Top, parts...)}
	if m.extensionsOpen {
		extensionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Panel))
		var extensionParts []string
		for _, span := range m.extensionSpans() {
			width := span.End - span.Start
			label := trimRunes(span.Config.Icon+"  "+span.Config.Name, max(width-2, 1))
			style := extensionStyle
			if span.Config.ID == m.selectedConnector {
				style = style.Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Selection)).Bold(true)
			}
			extensionParts = append(extensionParts, style.Width(width).Align(lipgloss.Center).Render(label))
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, extensionParts...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *Model) renderSidebar(t theme.Theme, height int) string {
	rows := m.sidebarRows()
	visible := m.sidebarVisibleHeight()
	header := lipgloss.NewStyle().Width(sidebarWidth - 1).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Panel)).Bold(true).Render(" " + strings.ToUpper(m.sectionName()) + "  󰅖")
	separator := lipgloss.NewStyle().Width(sidebarWidth - 1).Foreground(lipgloss.Color(t.Border)).Background(lipgloss.Color(t.Panel)).Render(strings.Repeat("─", max(sidebarWidth-1, 1)))
	lines := []string{header, separator}
	for i := 0; i < visible; i++ {
		index := m.sidebarScroll + i
		if index >= len(rows) {
			lines = append(lines, lipgloss.NewStyle().Width(sidebarWidth-1).Background(lipgloss.Color(t.Surface)).Render(""))
			continue
		}
		row := rows[index]
		prefix := strings.Repeat("  ", row.Depth)
		switch row.Kind {
		case rowCategory:
			arrow := ""
			if row.Expanded {
				arrow = ""
			}
			prefix += arrow + " "
		default:
			prefix += "  "
		}
		label := prefix + row.Label
		if row.Subtitle != "" && visibleWidth(label)+visibleWidth(row.Subtitle)+2 < sidebarWidth-1 {
			label += " " + row.Subtitle
		}
		style := lipgloss.NewStyle().Width(sidebarWidth - 1).Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Surface))
		if index == m.sidebarCursor && row.Kind != rowInfo {
			style = style.Background(lipgloss.Color(t.Selection)).Foreground(lipgloss.Color(t.Primary)).Bold(m.focusSidebar)
		}
		lines = append(lines, style.Render(trimRunes(label, sidebarWidth-3)))
	}
	return lipgloss.NewStyle().Width(sidebarWidth).Height(height).BorderRight(true).BorderForeground(lipgloss.Color(t.Border)).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderMain(t theme.Theme, width, height int) string {
	if content, ok := m.embeddedContent(t, width, height); ok {
		return content
	}
	var lines []string
	if m.page == pageHelp {
		lines = m.helpLines()
	} else {
		switch m.page {
		case pageOverview:
			lines = m.overviewLines(t, width, height)
		case pageFile:
			lines = m.fileLines(width, height)
		case pageTool:
			lines = m.toolLines()
		case pageAgent:
			lines = m.agentLines()
		case pageSkill:
			lines = m.skillLines()
		case pageTerminal:
			lines = m.terminalLines()
		case pageConnector:
			lines = m.connectorLines()
		}
	}
	if len(lines) == 0 {
		lines = []string{"", "  Nothing selected."}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	style := lipgloss.NewStyle().Width(width).Height(height).Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Background))
	var rendered []string
	for _, line := range lines {
		rendered = append(rendered, style.Width(width).Height(1).Render(trimRunes(line, max(width-1, 1))))
	}
	return strings.Join(rendered, "\n")
}

func (m *Model) overviewLines(_ theme.Theme, width, height int) []string {
	art := fitASCII(overviewASCII, max(width-4, 1), max(height-2, 1))
	lines := make([]string, 0, height)
	for index := 0; index < max((height-len(art))/2, 0); index++ {
		lines = append(lines, "")
	}
	for _, line := range art {
		left := max((width-visibleWidth(line))/2, 0)
		lines = append(lines, strings.Repeat(" ", left)+line)
	}
	return lines
}

func fitASCII(value string, width, height int) []string {
	source := strings.Split(strings.TrimRight(value, "\n"), "\n")
	if len(source) == 0 || width <= 0 || height <= 0 {
		return nil
	}
	targetHeight := min(len(source), height)
	result := make([]string, 0, targetHeight)
	for targetY := 0; targetY < targetHeight; targetY++ {
		sourceY := targetY
		if targetHeight < len(source) && targetHeight > 1 {
			sourceY = targetY * (len(source) - 1) / (targetHeight - 1)
		}
		line := []rune(source[sourceY])
		if len(line) > width {
			resized := make([]rune, width)
			for targetX := range resized {
				resized[targetX] = line[targetX*len(line)/width]
			}
			line = resized
		}
		result = append(result, string(line))
	}
	return result
}

func onlineConnectors(states []connectorapi.State) int {
	count := 0
	for _, state := range states {
		if state.Online {
			count++
		}
	}
	return count
}

func (m *Model) fileLines(_, _ int) []string {
	if m.sessionStarting() {
		return []string{"", "  Starting embedded NvChad and native NvimTree…", "", "  Workspace  " + displayText(m.root)}
	}
	return []string{
		"", "  NVCHAD WORKSPACE", "",
		"  NvChad opens on its dashboard for file and folder discovery.",
		"  Browse Folders selects a workspace and opens native NvimTree.",
		"  Find File opens one file in the current workspace.",
		"", "  Workspace  " + displayText(m.root),
		"", "  " + displayText(m.status),
	}
}

func (m *Model) toolLines() []string {
	if strings.HasPrefix(m.selectedTool, "apt:") {
		name := strings.TrimPrefix(m.selectedTool, "apt:")
		version := ""
		for _, pkg := range m.inventory.APTManual {
			if pkg.Name == name {
				version = pkg.Version
				break
			}
		}
		return []string{"", "  󰏖 " + displayText(name), "", "  Source       apt (manually selected)", "  Version      " + displayText(version), "", "  Inventory is detected dynamically on every refresh."}
	}
	tool, ok := m.findTool(m.selectedTool)
	if !ok {
		return nil
	}
	state := "not installed"
	if tool.Installed {
		state = "installed"
	}
	return []string{
		"", "  󰒓 " + tool.Name, "",
		"  " + tool.Description,
		"",
		"  State        " + state,
		"  Command      " + tool.Command,
		"  Path         " + emptyDash(tool.Path),
		"  Version      " + emptyDash(tool.Version),
		"  Origin       " + emptyDash(tool.Package),
		"  Documentation " + emptyDash(tool.Docs),
		"",
		"  Press R at any time to rescan tools and apt state.",
	}
}

func (m *Model) agentLines() []string {
	tool, ok := m.findTool(m.selectedAgent)
	if !ok {
		return []string{"", "  Select an agent from the left."}
	}
	state := " not installed"
	button := "Installation required before this page can become live."
	if tool.Installed {
		state = " ready"
		button = tool.Name + " opens here automatically when selected."
	}
	if m.sessionStarting() {
		button = "Starting embedded " + tool.Name + "…"
	}
	_, shell := shellForContext(m.profile)
	lines := []string{"", "  󰚩 " + tool.Name, "  " + button, "", "  " + tool.Description, "", "  Status       " + state, "  Runtime      direct executable", "  Shell context " + emptyDash(shell), "  Working dir  " + displayText(agentWorkingDir(m.root, tool.ID)), "  Executable   " + emptyDash(tool.Path), "  Version      " + emptyDash(tool.Version), "", "  Authentication"}
	for _, hint := range tool.AuthHints {
		lines = append(lines, "    • "+hint)
	}
	lines = append(lines, "", "  API keys are never displayed or copied into Lisan state.", "  The native CLI owns its login flow and credential files.", "", "  Docs: "+tool.Docs)
	return lines
}

func (m *Model) skillLines() []string {
	skill, ok := m.findSkill(m.selectedSkill)
	if !ok {
		return []string{"", "  Select a skill from the left."}
	}
	valid := "valid"
	if !skill.Valid {
		valid = "invalid: " + displayText(skill.Error)
	}
	return []string{
		"", "   " + skill.Name, "  [ Open SKILL.md in embedded NvChad: Enter ]", "",
		"  " + skill.Description, "",
		"  Provider     " + skill.Provider,
		"  Scope        " + skill.Scope,
		"  Validation   " + valid,
		"  Path         " + displayText(skill.Path),
		"",
		"  The index scans allowlisted provider roots and ignores caches.",
	}
}

func (m *Model) terminalLines() []string {
	mode := "HOST: default shell"
	_, shell := shellForContext(m.profile)
	if shell == "" {
		shell = "unavailable"
	}
	detail := "This shell uses the current process identity."
	if os.Getenv("LISAN_WORMSIGN") == "1" {
		mode = "WORMSIGN: host user"
		detail = "This shell has the current host user's filesystem permissions."
	} else if os.Getenv("LISAN_CONTAINER") == "1" {
		mode = "SIETCH TABR: " + m.profile.Terminal.DockerUser + "@tabr"
		detail = "The shell is isolated, but USUL persists and remains readable."
	}
	state := "The embedded shell starts automatically when this page opens."
	if m.sessionStarting() {
		state = "Starting the embedded shell…"
	}
	lines := []string{
		"", "   TERMINAL", "  " + state, "",
		"  Mode         " + mode,
		"  Shell        " + shell,
		"  Working dir  " + displayText(m.root),
	}
	if os.Getenv("LISAN_CONTAINER") == "1" {
		lines = append(lines,
			"  Docker user  "+m.profile.Terminal.DockerUser,
			"  Docker workdir  "+m.profile.Terminal.DockerWorkdir,
		)
	}
	return append(lines,
		"", "  "+detail,
		"  The outer terminal is the invoking host terminal and is never changed by Lisan.",
		"", "  Ctrl-G returns to wrapper controls.",
	)
}

func (m *Model) connectorLines() []string {
	id := m.selectedConnector
	state, ok := m.connectorState(id)
	if !ok {
		return []string{"", "  EXTENSION // " + id, "", "  Waiting for extension discovery…", "", "  Press R to retry."}
	}
	if !state.Online {
		return []string{
			"", "  EXTENSION // " + state.Config.Name, "", "  Status       offline",
			"  Endpoint     " + displayText(state.Config.Endpoint), "  Network      " + displayText(state.Config.Network),
			"", "  " + displayText(state.Error), "", "  In docker mode the launcher starts enabled managed sidecars.",
			"  In vm mode managed extensions run natively on loopback.", "  Check native_config and host tooling, then press R to retry.",
		}
	}
	var lines []string
	for _, panel := range state.Manifest.UI.Main {
		lines = append(lines, "", "  "+strings.ToUpper(panel.Title), "  "+strings.Repeat("─", min(len([]rune(panel.Title))+8, 42)))
		switch panel.Kind {
		case connectorapi.PanelSummary:
			lines = append(lines, m.extensionSummaryLines(state)...)
		case connectorapi.PanelActionOutput:
			lines = append(lines, m.extensionOutputLines(id)...)
		}
	}
	return lines
}

func (m *Model) extensionSummaryLines(state connectorapi.State) []string {
	return []string{
		"", "  " + state.Manifest.Icon + "  " + strings.ToUpper(state.Manifest.Name), "",
		"  " + state.Manifest.Description, "",
		"  Status       online",
		"  Protocol     v" + strconv.Itoa(state.Manifest.ProtocolVersion),
		"  Endpoint     " + displayText(state.Config.Endpoint),
		"  Network      " + displayText(state.Config.Network),
		fmt.Sprintf("  Tools        %d", len(state.Manifest.Tools)),
		fmt.Sprintf("  Actions      %d", len(state.Manifest.Actions)),
		"", "  Select an action on the left and press Enter or click it.",
	}
}

func (m *Model) extensionOutputLines(id string) []string {
	if m.connectorRunning[id] {
		return []string{"", "  ◌ Running " + m.selectedAction + "…"}
	}
	var lines []string
	if result, exists := m.connectorOutput[id]; exists {
		lines = append(lines, "", fmt.Sprintf("  LAST ACTION // %s // EXIT %d // %dms", result.ActionID, result.ExitCode, result.DurationMS))
		if result.Error != "" {
			lines = append(lines, "  Error: "+result.Error)
		}
		for _, line := range strings.Split(strings.TrimRight(result.Output, "\n"), "\n") {
			lines = append(lines, "  │ "+line)
		}
	}
	return lines
}

func (m *Model) helpLines() []string {
	return []string{
		"", "  HELP // KEYBOARD & MOUSE", "  " + strings.Repeat("━", 40), "",
		"  Mouse        click the top bar, sidebar items, or an embedded pane",
		"  Re-click     collapse/restore the active sidebar; Extensions toggles its menu",
		"  Wheel        scroll the sidebar or active embedded application",
		"  Tab/S-Tab    next/previous top-level page (wrapper mode)",
		"  Ctrl-G       toggle input between embedded app and wrapper",
		"  Ctrl-B       collapse/expand sidebar",
		"  h / l        focus sidebar / main pane",
		"  j / k        move or scroll",
		"  g / G        first/top or last/bottom",
		"  Enter/Space  expand a category or activate the selected item",
		"  e            open a selected skill in embedded NvChad",
		"  F2           cycle colour theme",
		"  r            dynamically rescan tools, apt and skills",
		"  Esc          close help or return to overview",
		"  Ctrl-C       quit",
		"", "  Press ? or Esc to return.",
	}
}

func (m *Model) renderFooter(t theme.Theme) string {
	spinner := ""
	if m.loading {
		spinner = " ◌"
	}
	left := " " + displayText(m.status) + spinner
	right := "mouse on  ? help  F2 theme  "
	space := max(m.width-visibleWidth(left)-visibleWidth(right), 1)
	line := trimRunes(left+strings.Repeat(" ", space)+right, m.width)
	return lipgloss.NewStyle().Width(m.width).Foreground(lipgloss.Color(t.Muted)).Background(lipgloss.Color(t.Panel)).Render(line)
}

func (m *Model) sectionName() string {
	for _, item := range m.navigation {
		if item.Section == m.section {
			return item.Name
		}
	}
	return "Overview"
}

func emptyDash(value string) string {
	value = displayText(value)
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func displayText(value string) string {
	return textsafe.Label(value, 1000)
}

func trimRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func visibleWidth(value string) int {
	return ansi.StringWidth(value)
}
