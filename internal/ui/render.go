package ui

import (
	"fmt"
	"image/color"
	"os"
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
		foreground, background, bold := sidebarRowPalette(row, t)
		style := lipgloss.NewStyle().Width(sidebarWidth - 1).
			Foreground(lipgloss.Color(foreground)).
			Background(lipgloss.Color(background)).
			Bold(bold)
		if index == m.sidebarCursor {
			style = style.Background(lipgloss.Color(t.Selection)).Foreground(lipgloss.Color(t.Primary)).Bold(m.focusSidebar)
		}
		lines = append(lines, style.Render(trimRunes(label, sidebarWidth-3)))
	}
	return lipgloss.NewStyle().Width(sidebarWidth).Height(height).BorderRight(true).BorderForeground(lipgloss.Color(t.Border)).Render(strings.Join(lines, "\n"))
}

// sidebarRowPalette is the core visual contract for every sidebar, including
// extension-provided controls. Extensions describe semantics; they never need
// to know about ANSI colors or the active Lisan theme.
func sidebarRowPalette(row sidebarRow, t theme.Theme) (foreground, background string, bold bool) {
	foreground, background = t.Text, t.Surface
	if row.Active {
		background = t.Panel
	}

	switch row.Kind {
	case rowCategory:
		return t.Secondary, t.Panel, true
	case rowConnectorView:
		if row.Active {
			return t.Primary, background, true
		}
		return t.Muted, background, false
	case rowConnectorAction:
		if row.Active {
			return t.Primary, background, true
		}
		return t.Text, background, false
	case rowConnectorSession:
		if row.Active {
			return t.Primary, background, true
		}
		return t.Text, background, false
	case rowConnectorArtifact:
		if row.Active {
			return t.Success, background, true
		}
		return t.Text, background, false
	default:
		return foreground, background, bold
	}
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
		case pageTerminal:
			lines = m.terminalLines()
		case pageConnector:
			document := m.visibleConnectorDocument(max(width-1, 1), height)
			lines = make([]string, 0, len(document))
			for _, line := range document {
				lines = append(lines, line.Text)
			}
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
	contentWidth := max(width-1, 1)
	for _, line := range lines {
		visible := trimRunes(line, contentWidth)
		rendered = append(rendered, style.Width(width).Height(1).Render(visible))
	}
	return strings.Join(rendered, "\n")
}

func (m *Model) wrappedConnectorLines(width int) []string {
	document := wrapExtensionDocument(m.connectorDocument(), width)
	lines := make([]string, 0, len(document))
	for _, line := range document {
		lines = append(lines, line.Text)
	}
	return lines
}

type extensionLine struct {
	Text         string
	ControlIndex int
	Interactive  bool
}

func extensionTextLines(lines ...string) []extensionLine {
	result := make([]extensionLine, 0, len(lines))
	for _, line := range lines {
		result = append(result, extensionLine{Text: line})
	}
	return result
}

func wrapExtensionDocument(lines []extensionLine, width int) []extensionLine {
	width = max(width, 1)
	wrapped := make([]extensionLine, 0, len(lines))
	for _, line := range lines {
		for _, part := range strings.Split(ansi.Hardwrap(line.Text, width, true), "\n") {
			wrapped = append(wrapped, extensionLine{Text: part, ControlIndex: line.ControlIndex, Interactive: line.Interactive})
		}
	}
	return wrapped
}

func (m *Model) visibleConnectorDocument(width, height int) []extensionLine {
	document := wrapExtensionDocument(m.connectorDocument(), width)
	m.extensionScrollY = min(max(m.extensionScrollY, 0), max(len(document)-height, 0))
	end := min(m.extensionScrollY+height, len(document))
	return document[m.extensionScrollY:end]
}

func (m *Model) extensionControlAt(x, y int) (int, bool) {
	if x < 0 || y < 0 || y >= m.mainContentHeight() {
		return 0, false
	}
	document := m.visibleConnectorDocument(max(m.mainPaneWidth()-1, 1), m.mainContentHeight())
	if y >= len(document) || !document[y].Interactive {
		return 0, false
	}
	plain := ansi.Strip(document[y].Text)
	start := visibleWidth(plain) - visibleWidth(strings.TrimLeft(plain, " "))
	end := visibleWidth(strings.TrimRight(plain, " "))
	if x < start || x >= end {
		return 0, false
	}
	return document[y].ControlIndex, true
}

func (m *Model) ensureExtensionControlVisible() {
	document := wrapExtensionDocument(m.connectorDocument(), max(m.mainPaneWidth()-1, 1))
	height := m.mainContentHeight()
	for index, line := range document {
		if !line.Interactive || line.ControlIndex != m.extensionControlCursor {
			continue
		}
		if index < m.extensionScrollY {
			m.extensionScrollY = index
		} else if index >= m.extensionScrollY+height {
			m.extensionScrollY = index - height + 1
		}
		return
	}
}

func wrapExtensionLines(lines []string, width int) []string {
	width = max(width, 1)
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(line, width, true), "\n")...)
	}
	return wrapped
}

func (m *Model) overviewLines(_ theme.Theme, width, height int) []string {
	availableWidth := max(width-4, 1)
	availableHeight := max(height-2, 1)
	artwork, ok := overviewArtworkForViewport(availableWidth, availableHeight)
	if !ok {
		// Keep Overview intentionally empty when even a centered banner crop
		// would be reduced to an unrecognizable fragment.
		return []string{""}
	}
	art := artwork.crop(availableWidth, availableHeight)
	lines := make([]string, 0, height)
	for index := 0; index < max((height-len(art))/2, 0); index++ {
		lines = append(lines, "")
	}
	artWidth := 0
	if len(art) > 0 {
		artWidth = visibleWidth(art[0])
	}
	left := max((width-artWidth)/2, 0)
	for _, line := range art {
		lines = append(lines, strings.Repeat(" ", left)+line)
	}
	return lines
}

// Retain the previous compact layout's legibility floor, but crop the banner
// at every visible size so Overview has one consistent source image.
const (
	overviewMinimumWidth  = 59
	overviewMinimumHeight = 19
)

type asciiArtwork struct {
	rows   [][]rune
	width  int
	height int
}

var overviewBanner = newASCIIArtwork(overviewBannerASCII)

func newASCIIArtwork(value string) asciiArtwork {
	value = strings.TrimRight(value, "\r\n")
	if value == "" {
		return asciiArtwork{}
	}

	source := strings.Split(value, "\n")
	artwork := asciiArtwork{height: len(source)}
	for index, line := range source {
		source[index] = strings.TrimSuffix(line, "\r")
		artwork.width = max(artwork.width, len([]rune(source[index])))
	}
	if artwork.width == 0 {
		return asciiArtwork{}
	}

	artwork.rows = make([][]rune, artwork.height)
	for index, line := range source {
		artwork.rows[index] = []rune(line)
		if padding := artwork.width - len(artwork.rows[index]); padding > 0 {
			artwork.rows[index] = append(artwork.rows[index], []rune(strings.Repeat(" ", padding))...)
		}
	}
	return artwork
}

func overviewArtworkForViewport(width, height int) (asciiArtwork, bool) {
	visible := overviewBanner.width > 0 && overviewBanner.height > 0 &&
		width >= overviewMinimumWidth && height >= overviewMinimumHeight
	return overviewBanner, visible
}

func (artwork asciiArtwork) crop(width, height int) []string {
	if artwork.width == 0 || artwork.height == 0 || width <= 0 || height <= 0 {
		return nil
	}
	targetWidth := min(width, artwork.width)
	targetHeight := min(height, artwork.height)
	startX := (artwork.width - targetWidth) / 2
	// Preserve more of the lower subject when height is constrained: remove
	// roughly three rows above for every row removed below.
	startY := (artwork.height - targetHeight) * 3 / 4

	result := make([]string, 0, targetHeight)
	for row := startY; row < startY+targetHeight; row++ {
		result = append(result, string(artwork.rows[row][startX:startX+targetWidth]))
	}
	return result
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
	document := m.connectorDocument()
	lines := make([]string, 0, len(document))
	for _, line := range document {
		lines = append(lines, line.Text)
	}
	return lines
}

func (m *Model) connectorDocument() []extensionLine {
	id := m.selectedConnector
	state, ok := m.connectorState(id)
	if !ok {
		return extensionTextLines("", "  EXTENSION // "+id, "", "  Waiting for extension discovery…", "", "  Press R to retry.")
	}
	if !state.Online {
		return extensionTextLines(
			"", "  EXTENSION // "+state.Config.Name, "", "  Status       offline",
			"  Endpoint     "+displayText(state.Config.Endpoint), "  Network      "+displayText(state.Config.Network),
			"", "  "+displayText(state.Error), "", "  In docker mode the launcher starts enabled managed sidecars.",
			"  In vm mode managed extensions run natively on loopback.", "  Check the bundle executable and host tooling, then press R to retry.",
		)
	}
	lines := extensionTextLines(
		"", "  "+state.Manifest.Icon+"  "+strings.ToUpper(state.Manifest.Name),
		"  "+state.Manifest.Description,
		fmt.Sprintf("  v%s · protocol v%d · %d views · %d actions · %d sessions", state.Manifest.Version, state.Manifest.ProtocolVersion, len(state.Manifest.Views), len(state.Manifest.Actions), len(state.Manifest.Sessions)),
	)
	if m.selectedArtifact != "" {
		return append(lines, m.extensionArtifactDocument(id)...)
	}
	if m.selectedSession != "" {
		return append(lines, m.extensionSessionDocument(id)...)
	}
	if m.selectedAction != "" {
		lines = append(lines, m.extensionActionDocument(state)...)
		for _, line := range m.extensionJobLines(id) {
			lines = append(lines, extensionLine{Text: line})
		}
		return lines
	}
	if view, exists := state.Views[m.selectedView]; exists {
		lines = append(lines, extensionTextLines("", "  "+strings.ToUpper(view.Title), "  "+strings.Repeat("─", min(len([]rune(view.Title))+8, 42)))...)
		for _, block := range view.Blocks {
			for _, line := range renderExtensionBlock(block) {
				lines = append(lines, extensionLine{Text: line})
			}
		}
	}
	return lines
}

func renderExtensionBlock(block connectorapi.Block) []string {
	title := block.Title
	if title == "" {
		title = block.ID
	}
	marker := map[string]string{connectorapi.ToneInfo: "ℹ", connectorapi.ToneSuccess: "✓", connectorapi.ToneWarning: "!", connectorapi.ToneDanger: "×"}[block.Tone]
	if marker == "" {
		marker = "•"
	}
	lines := []string{"", "  " + marker + " " + strings.ToUpper(title)}
	switch block.Kind {
	case connectorapi.BlockText, connectorapi.BlockStatus:
		for _, line := range strings.Split(strings.TrimRight(block.Text, "\n"), "\n") {
			lines = append(lines, "    "+line)
		}
		if block.Detail != "" {
			lines = append(lines, "    "+block.Detail)
		}
	case connectorapi.BlockKeyValue:
		for _, field := range block.Fields {
			lines = append(lines, fmt.Sprintf("    %-18s %s", field.Label, field.Value))
		}
	case connectorapi.BlockList:
		for _, item := range block.Items {
			line := "    • " + item.Label
			if item.Detail != "" {
				line += " · " + item.Detail
			}
			lines = append(lines, line)
		}
	case connectorapi.BlockTable:
		var headers []string
		for _, column := range block.Columns {
			headers = append(headers, column.Title)
		}
		lines = append(lines, "    "+strings.Join(headers, " │ "))
		lines = append(lines, "    "+strings.Repeat("─", min(len([]rune(strings.Join(headers, " │ "))), 80)))
		for _, row := range block.Rows {
			lines = append(lines, "    "+strings.Join(row, " │ "))
		}
	case connectorapi.BlockProgress:
		filled := block.Progress / 5
		lines = append(lines, fmt.Sprintf("    [%s%s] %d%% %s", strings.Repeat("━", filled), strings.Repeat("─", 20-filled), block.Progress, block.Detail))
	}
	return lines
}

func (m *Model) extensionActionLines(state connectorapi.State) []string {
	document := m.extensionActionDocument(state)
	lines := make([]string, 0, len(document))
	for _, line := range document {
		lines = append(lines, line.Text)
	}
	return lines
}

func (m *Model) extensionActionDocument(state connectorapi.State) []extensionLine {
	var action connectorapi.ActionDescriptor
	for _, candidate := range state.Manifest.Actions {
		if candidate.ID == m.selectedAction {
			action = candidate
			break
		}
	}
	if action.ID == "" {
		return nil
	}
	t := theme.All[m.themeIndex]
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Primary))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted))
	lines := extensionTextLines("", sectionStyle.Render("  ACTION // "+strings.ToUpper(action.Name)), "  "+action.Description, "")
	for index, input := range action.Inputs {
		value := m.connectorInputValue(state.Config.ID, action.ID, input.ID)
		if m.extensionInputEdit == action.ID+":"+input.ID {
			value = m.extensionInputText + "▌"
		}
		label, display := connectorInputAffordance(input, value)
		kind, name := splitInputAffordance(label)
		kindColor := t.Secondary
		if input.Kind == connectorapi.InputSelect {
			kindColor = t.Primary
		} else if input.Kind == connectorapi.InputBoolean {
			kindColor = t.Muted
			if strings.Contains(display, " ON") {
				kindColor = t.Success
			}
		}
		kindStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(kindColor))
		focused := !m.focusSidebar && m.extensionControlCursor == index
		marker := "  "
		background := t.Panel
		if focused {
			marker = "› "
			background = t.Selection
		}
		valueStyle := lipgloss.NewStyle().Bold(focused).Foreground(lipgloss.Color(kindColor)).Background(lipgloss.Color(background))
		line := "  " + marker + kindStyle.Render(kind) + "  " + name + "  " + valueStyle.Render(" "+display+" ")
		if input.Description != "" {
			line += mutedStyle.Render("  ·  " + input.Description)
		}
		lines = append(lines, extensionLine{Text: line, ControlIndex: index, Interactive: true})
	}
	runIndex := len(action.Inputs)
	runLabel := " RUN " + strings.ToUpper(action.Name) + " "
	runForeground, runBackground := t.Background, t.Primary
	if m.connectorRunning[state.Config.ID] {
		runLabel = " RUNNING " + strings.ToUpper(action.Name) + " "
		runForeground, runBackground = t.Muted, t.Panel
	} else if !m.focusSidebar && m.extensionControlCursor == runIndex {
		runBackground = t.Success
	}
	run := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(runForeground)).Background(lipgloss.Color(runBackground)).Render(runLabel)
	lines = append(lines,
		extensionLine{Text: "    " + run, ControlIndex: runIndex, Interactive: true},
		extensionLine{Text: mutedStyle.Render("    ↑/↓ chooses a control · Enter/Space activates · C cancels a running job")},
	)
	return lines
}

func splitInputAffordance(label string) (kind, name string) {
	parts := strings.Fields(label)
	if len(parts) == 0 {
		return "INPUT", ""
	}
	return parts[0], strings.TrimSpace(strings.TrimPrefix(label, parts[0]))
}

func (m *Model) extensionJobLines(id string) []string {
	job, exists := m.connectorJobs[id]
	if !exists {
		return nil
	}
	filled := job.Progress / 5
	lines := []string{"", fmt.Sprintf("  JOB // %s // %s", strings.ToUpper(job.ActionID), strings.ToUpper(job.Status)), fmt.Sprintf("  [%s%s] %d%% %s", strings.Repeat("━", filled), strings.Repeat("─", 20-filled), job.Progress, job.StatusText)}
	for _, line := range job.Logs {
		lines = append(lines, "  │ "+line)
	}
	if job.Result != "" {
		lines = append(lines, "", "  "+job.Result)
	}
	if job.Error != "" {
		lines = append(lines, "  Error: "+job.Error)
	}
	if len(job.Artifacts) > 0 {
		lines = append(lines, fmt.Sprintf("  %d artifact(s) ready in the sidebar", len(job.Artifacts)))
	}
	return lines
}

func (m *Model) extensionSessionDocument(id string) []extensionLine {
	state, _ := m.connectorState(id)
	title := m.selectedSession
	for _, descriptor := range state.Manifest.Sessions {
		if descriptor.ID == m.selectedSession {
			title = descriptor.Name
		}
	}
	t := theme.All[m.themeIndex]
	lines := extensionTextLines("", "  SESSION // "+strings.ToUpper(title))
	session, exists := m.connectorSessions[id]
	if !exists || session.SessionID != m.selectedSession {
		buttonBackground := t.Primary
		if !m.focusSidebar && m.extensionControlCursor == 0 {
			buttonBackground = t.Success
		}
		button := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Background)).Background(lipgloss.Color(buttonBackground)).Render(" OPEN " + strings.ToUpper(title) + " ")
		return append(lines,
			extensionLine{Text: "", Interactive: false},
			extensionLine{Text: "    " + button, ControlIndex: 0, Interactive: true},
			extensionLine{Text: "    Enter/Space opens this extension-owned session."},
		)
	}
	focusBackground := t.Primary
	if !m.focusSidebar && m.extensionControlCursor == 0 {
		focusBackground = t.Success
	}
	focus := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Background)).Background(lipgloss.Color(focusBackground)).Render(" FOCUS INPUT ")
	lines = append(lines,
		extensionLine{Text: ""},
		extensionLine{Text: "    " + focus, ControlIndex: 0, Interactive: true},
	)
	for _, line := range strings.Split(strings.TrimRight(session.Output, "\n"), "\n") {
		lines = append(lines, extensionLine{Text: "  " + line})
	}
	prompt := session.Prompt
	if prompt == "" {
		prompt = "> "
	}
	cursor := ""
	if m.extensionSessionCapture {
		cursor = "▌"
	}
	lines = append(lines, extensionTextLines("", "  "+prompt+m.extensionSessionInput+cursor, "", "  Enter sends a line · Ctrl-G returns to wrapper controls")...)
	return lines
}

func (m *Model) extensionArtifactDocument(id string) []extensionLine {
	job := m.connectorJobs[id]
	var selected connectorapi.Artifact
	for _, artifact := range job.Artifacts {
		if artifact.ID == m.selectedArtifact {
			selected = artifact
			break
		}
	}
	if selected.ID == "" {
		return extensionTextLines("", "  ARTIFACT // NOT FOUND")
	}
	t := theme.All[m.themeIndex]
	background := t.Success
	if m.focusSidebar {
		background = t.Primary
	}
	button := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Background)).Background(lipgloss.Color(background)).Render(" SAVE TO SHARED ")
	return []extensionLine{
		{Text: ""},
		{Text: "  ARTIFACT // " + strings.ToUpper(selected.Name)},
		{Text: "  Size        " + fmt.Sprintf("%d B", selected.Size)},
		{Text: "  Media type  " + emptyDash(selected.MediaType)},
		{Text: "  SHA-256     " + emptyDash(selected.SHA256)},
		{Text: ""},
		{Text: "    " + button, ControlIndex: 0, Interactive: true},
		{Text: "    Enter/Space exports this artifact into the shared directory."},
	}
}

func (m *Model) helpLines() []string {
	return []string{
		"", "  HELP // KEYBOARD & MOUSE", "  " + strings.Repeat("━", 40), "",
		"  Mouse        click navigation, sidebar items, main controls, or embedded panes",
		"  Re-click     collapse/restore the active sidebar; Extensions toggles its menu",
		"  Wheel        vertically scroll Terminal history, extensions, or the sidebar",
		"  Tab/S-Tab    next/previous top-level page (wrapper mode)",
		"  Ctrl-G       toggle input between embedded app and wrapper",
		"  Ctrl-B       collapse/expand sidebar",
		"  h / l        focus sidebar / main pane",
		"  j / k        move, select an extension control, or vertically scroll",
		"  g / G        first/top or last/bottom; PgUp/PgDn scroll a page",
		"  Enter/Space  expand a category or activate the selected item",
		"  F2           cycle colour theme",
		"  r            dynamically rescan configured inventory",
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
