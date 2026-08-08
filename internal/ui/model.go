package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"lisanalgaib/internal/appconfig"
	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/settings"
	"lisanalgaib/internal/skills"
	terminalhost "lisanalgaib/internal/terminal"
	"lisanalgaib/internal/theme"
)

const (
	sidebarWidth         = 33
	baseTopHeight        = 2
	footerHeight         = 1
	panelHeader          = 2
	embeddedHeaderHeight = 1
)

type section int

const (
	sectionOverview section = iota
	sectionExplorer
	sectionTools
	sectionAgents
	sectionSkills
	sectionTerminal
	sectionExtensions
)

type page int

const (
	pageOverview page = iota
	pageFile
	pageTool
	pageAgent
	pageSkill
	pageTerminal
	pageConnector
	pageHelp
)

type rowKind int

const (
	rowInfo rowKind = iota
	rowCategory
	rowTool
	rowPackage
	rowAgent
	rowSkill
	rowConnectorTool
	rowConnectorAction
)

type sidebarRow struct {
	Kind     rowKind
	ID       string
	Label    string
	Subtitle string
	Depth    int
	Expanded bool
}

type sectionViewState struct {
	Sidebar      bool
	FocusSidebar bool
	Cursor       int
	Scroll       int
}

type extensionViewState struct {
	SelectedAction string
	Cursor         int
	Scroll         int
}

type navItem struct {
	Section section
	Icon    string
	Name    string
}

var navigation = []navItem{
	{sectionOverview, "󰋜", "Overview"},
	{sectionExplorer, "", "Files"},
	{sectionTools, "󰒓", "Tools"},
	{sectionAgents, "󰚩", "Mentats"},
	{sectionSkills, "", "Skills"},
	{sectionTerminal, "", "Terminal"},
}

type navigationSpan struct {
	Index int
	Start int
	End   int
}

func navigationSpans(totalWidth int) []navigationSpan {
	return navigationSpansFor(navigation, totalWidth)
}

func navigationSpansFor(items []navItem, totalWidth int) []navigationSpan {
	spans := make([]navigationSpan, 0, len(items))
	if totalWidth <= 0 || len(items) == 0 {
		return spans
	}
	baseWidth := totalWidth / len(items)
	remainder := totalWidth % len(items)
	x := 0
	for index := range items {
		width := baseWidth
		if index < remainder {
			width++
		}
		spans = append(spans, navigationSpan{Index: index, Start: x, End: x + width})
		x += width
	}
	return spans
}

func navigationAtFor(items []navItem, x, totalWidth int) (section, bool) {
	for _, span := range navigationSpansFor(items, totalWidth) {
		if x >= span.Start && x < span.End {
			return items[span.Index].Section, true
		}
	}
	return sectionOverview, false
}

type extensionSpan struct {
	Config appconfig.ConnectorConfig
	Start  int
	End    int
}

func (m *Model) enabledExtensions() []appconfig.ConnectorConfig {
	var extensions []appconfig.ConnectorConfig
	for _, connector := range m.profile.Connectors {
		if connector.Enabled {
			extensions = append(extensions, connector)
		}
	}
	return extensions
}

func (m *Model) extensionSpans() []extensionSpan {
	extensions := m.enabledExtensions()
	if len(extensions) == 0 || m.width <= 0 {
		return nil
	}
	spans := make([]extensionSpan, 0, len(extensions))
	baseWidth := m.width / len(extensions)
	remainder := m.width % len(extensions)
	x := 0
	for index, extension := range extensions {
		width := baseWidth
		if index < remainder {
			width++
		}
		spans = append(spans, extensionSpan{Config: extension, Start: x, End: x + width})
		x += width
	}
	return spans
}

func (m *Model) extensionAt(x int) (string, bool) {
	for _, span := range m.extensionSpans() {
		if x >= span.Start && x < span.End {
			return span.Config.ID, true
		}
	}
	return "", false
}

func (m *Model) topHeight() int {
	if m.extensionsOpen && len(m.enabledExtensions()) > 0 {
		return baseTopHeight + 1
	}
	return baseTopHeight
}

type refreshMsg struct {
	Inventory  inventory.Snapshot
	Skills     []skills.Skill
	Connectors []connectorapi.State
}

type connectorActionMsg struct {
	ConnectorID string
	ActionID    string
	Result      connectorapi.RunResponse
	Err         error
}

type Model struct {
	ctx               context.Context
	cancel            context.CancelFunc
	root              string
	profile           appconfig.Profile
	navigation        []navItem
	width             int
	height            int
	section           section
	page              page
	previousPage      page
	sidebar           bool
	focusSidebar      bool
	sidebarCursor     int
	sidebarScroll     int
	themeIndex        int
	inventory         inventory.Snapshot
	skills            []skills.Skill
	connectors        []connectorapi.State
	extensionsOpen    bool
	selectedConnector string
	selectedAction    string
	connectorOutput   map[string]connectorapi.RunResponse
	connectorRunning  map[string]bool
	selectedTool      string
	selectedAgent     string
	selectedSkill     string
	editorPath        string
	expanded          map[string]bool
	sectionViews      map[section]sectionViewState
	extensionViews    map[string]extensionViewState
	loading           bool
	status            string
	sessions          map[string]*terminalhost.Session
	starting          map[string]bool
	capture           bool
}

func NewModel(root string) *Model {
	return NewModelWithProfile(root, appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now()))
}

func NewModelWithProfile(root string, profile appconfig.Profile) *Model {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	saved := settings.Load()
	_, themeIndex := theme.ByName(saved.Theme)
	ctx, cancel := context.WithCancel(context.Background())
	return &Model{
		ctx:          ctx,
		cancel:       cancel,
		root:         root,
		profile:      profile.Clone(),
		navigation:   navigationFor(profile),
		width:        120,
		height:       36,
		section:      sectionOverview,
		page:         pageOverview,
		previousPage: pageOverview,
		sidebar:      true,
		focusSidebar: false,
		themeIndex:   themeIndex,
		expanded: map[string]bool{
			"workspace": true,
			"user":      true,
		},
		loading:           true,
		status:            "Scanning configured tools, agents, skills, and extensions…",
		sessions:          make(map[string]*terminalhost.Session),
		starting:          make(map[string]bool),
		connectorOutput:   make(map[string]connectorapi.RunResponse),
		connectorRunning:  make(map[string]bool),
		sectionViews:      make(map[section]sectionViewState),
		extensionViews:    make(map[string]extensionViewState),
		selectedConnector: firstEnabledConnector(profile),
	}
}

func (m *Model) Init() tea.Cmd {
	return refreshCmd(m.ctx, m.root, m.profile)
}

func refreshCmd(parent context.Context, root string, profile appconfig.Profile) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 12*time.Second)
		defer cancel()
		return refreshMsg{
			Inventory:  inventory.ScanSelected(ctx, inventorySelection(profile)),
			Skills:     scanSkills(root, profile),
			Connectors: connectorapi.Scan(ctx, profile.Connectors),
		}
	}
}

func navigationFor(profile appconfig.Profile) []navItem {
	items := []navItem{{sectionOverview, "󰋜", "Overview"}}
	for _, item := range navigation[1:] {
		enabled := false
		switch item.Section {
		case sectionExplorer:
			enabled = profile.Feature("files")
		case sectionTools:
			enabled = profile.Feature("tools")
		case sectionAgents:
			enabled = profile.Feature("agents") && anyAgentEnabled(profile)
		case sectionSkills:
			enabled = profile.Feature("skills")
		case sectionTerminal:
			enabled = profile.Feature("terminal")
		}
		if enabled {
			items = append(items, item)
		}
	}
	for _, connector := range profile.Connectors {
		if connector.Enabled {
			items = append(items, navItem{Section: sectionExtensions, Icon: "󰒍", Name: "Extensions"})
			break
		}
	}
	return items
}

func firstEnabledConnector(profile appconfig.Profile) string {
	for _, connector := range profile.Connectors {
		if connector.Enabled {
			return connector.ID
		}
	}
	return ""
}

func anyAgentEnabled(profile appconfig.Profile) bool {
	for _, id := range []string{"codex", "opencode", "claude", "kimi"} {
		if profile.Agent(id) {
			return true
		}
	}
	return false
}

func inventorySelection(profile appconfig.Profile) inventory.Selection {
	ids := map[string]bool{}
	if profile.Feature("tools") {
		for _, id := range []string{"git", "rg", "nvim", "nvchad", "node", "go", "python"} {
			ids[id] = profile.Tool(id)
		}
		ids["npm"] = profile.Tool("node")
	}
	if profile.Feature("files") {
		ids["nvim"] = true
	}
	if profile.Feature("terminal") {
		if os.Getenv("LISAN_CONTAINER") == "1" {
			ids[profile.Terminal.DockerShell] = true
		} else if _, shell := hostContextShell(); shell != "" {
			ids[strings.TrimSuffix(strings.ToLower(shell), ".exe")] = true
		}
	}
	if profile.Feature("agents") {
		for _, id := range []string{"codex", "opencode", "claude", "kimi"} {
			ids[id] = profile.Agent(id)
		}
	}
	return inventory.Selection{IDs: ids, APTManual: profile.Feature("tools")}
}

func scanSkills(root string, profile appconfig.Profile) []skills.Skill {
	if !profile.Feature("skills") {
		return nil
	}
	return skills.Scan(root)
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 1)
		m.height = max(msg.Height, 1)
		m.clampCursor()
		m.resizeVisibleSession()
		return m, nil
	case refreshMsg:
		m.inventory = msg.Inventory
		m.skills = msg.Skills
		m.connectors = msg.Connectors
		for _, extension := range m.connectors {
			for _, panel := range extension.Manifest.UI.Sidebar {
				key := extensionPanelKey(extension.Config.ID, panel.ID)
				if _, exists := m.expanded[key]; !exists {
					m.expanded[key] = panel.Expanded
				}
			}
		}
		m.loading = false
		online := 0
		for _, connector := range m.connectors {
			if connector.Online {
				online++
			}
		}
		if runtime.GOOS == "linux" {
			m.status = fmt.Sprintf("Detected %d tools, %d manual apt packages, %d skills, %d/%d extensions online", len(m.inventory.Tools), len(m.inventory.APTManual), len(m.skills), online, len(m.connectors))
		} else {
			m.status = fmt.Sprintf("Detected %d tools, %d skills, %d/%d extensions online", len(m.inventory.Tools), len(m.skills), online, len(m.connectors))
		}
		m.clampCursor()
		if m.section == sectionAgents {
			if m.selectedAgent == "" {
				m.selectedAgent = m.firstAgentID()
			}
			return m, m.ensureAgent(m.selectedAgent)
		}
		if m.section == sectionExtensions {
			m.ensureSelectedExtensionAction()
		}
		return m, nil
	case connectorActionMsg:
		delete(m.connectorRunning, msg.ConnectorID)
		if msg.Err != nil {
			msg.Result.ActionID = msg.ActionID
			msg.Result.Error = msg.Err.Error()
		}
		m.connectorOutput[msg.ConnectorID] = msg.Result
		if msg.Err != nil {
			m.status = "Extension action failed: " + msg.Err.Error()
		} else {
			m.status = fmt.Sprintf("Extension action %s finished with exit %d", msg.ActionID, msg.Result.ExitCode)
		}
		return m, nil
	case terminalStartedMsg:
		return m, m.handleTerminalStarted(msg)
	case terminalEventMsg:
		return m, m.handleTerminalEvent(msg)
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m, m.handleClick(msg)
		}
		return m, m.forwardMouse(msg)
	case tea.MouseReleaseMsg:
		return m, m.forwardMouse(msg)
	case tea.MouseWheelMsg:
		return m, m.handleWheel(msg)
	case tea.MouseMotionMsg:
		return m, m.forwardMouse(msg)
	case tea.KeyPressMsg:
		if session, _ := m.visibleSession(); session != nil && m.capture {
			if msg.String() == "ctrl+g" {
				m.capture = false
				m.focusSidebar = m.sidebar
				session.Blur()
				m.status = "Wrapper controls active; Ctrl-G or click the pane to return"
				return m, nil
			}
			key := uv.Key(msg.Key())
			session.SendKey(uv.KeyPressEvent(key))
			return m, nil
		}
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *Model) handleKey(key string) tea.Cmd {
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "ctrl+g":
		if session, _ := m.visibleSession(); session != nil {
			m.capture = true
			m.focusSidebar = false
			session.Focus()
			m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		}
		return nil
	case "ctrl+b":
		if !m.sectionSupportsSidebar() {
			m.status = "This page uses its native full-width interface"
			return nil
		}
		m.sidebar = !m.sidebar
		m.resizeVisibleSession()
		return nil
	case "f2", "T", "shift+t":
		m.cycleTheme(1)
		return nil
	case "tab":
		return m.cycleSection(1)
	case "shift+tab":
		return m.cycleSection(-1)
	case "?":
		if m.page == pageHelp {
			m.page = m.previousPage
			m.resumeVisibleSession()
		} else {
			m.previousPage = m.page
			m.blurVisibleSession()
			m.page = pageHelp
		}
		return nil
	case "esc":
		if m.page == pageHelp {
			m.page = m.previousPage
			m.resumeVisibleSession()
		} else {
			return m.selectSection(sectionOverview)
		}
		return nil
	case "r":
		if m.loading {
			m.status = "A refresh is already running…"
			return nil
		}
		m.loading = true
		m.status = "Refreshing dynamic inventory…"
		return refreshCmd(m.ctx, m.root, m.profile)
	case "h", "left":
		if m.sidebarDrawn() {
			m.focusSidebar = true
		}
		return nil
	case "l", "right":
		m.focusSidebar = false
		return nil
	}

	if m.focusSidebar && m.sidebarDrawn() {
		return m.handleSidebarKey(key)
	}
	return m.handleMainKey(key)
}

func (m *Model) handleSidebarKey(key string) tea.Cmd {
	rows := m.sidebarRows()
	moved := false
	switch key {
	case "j", "down":
		m.sidebarCursor = min(m.sidebarCursor+1, max(len(rows)-1, 0))
		moved = true
	case "k", "up":
		m.sidebarCursor = max(m.sidebarCursor-1, 0)
		moved = true
	case "g", "home":
		m.sidebarCursor = 0
		moved = true
	case "G", "shift+g", "end":
		m.sidebarCursor = max(len(rows)-1, 0)
		moved = true
	case "enter", " ":
		return m.activateRow(rows, m.sidebarCursor)
	}
	m.ensureSidebarVisible()
	if moved {
		return m.revealRow(rows, m.sidebarCursor)
	}
	return nil
}

func (m *Model) handleMainKey(key string) tea.Cmd {
	switch m.page {
	case pageFile:
		if key == "enter" || key == "i" {
			return m.ensureEditor(m.editorPath)
		}
	case pageSkill:
		if key == "enter" || key == "e" {
			return m.launchSelectedSkill()
		}
	case pageAgent:
		if key == "enter" || key == "i" {
			return m.ensureAgent(m.selectedAgent)
		}
	case pageTerminal:
		if key == "enter" || key == "i" {
			return m.ensureShell()
		}
	case pageConnector:
		if key == "enter" || key == "i" {
			return m.runSelectedConnectorAction()
		}
	}
	return nil
}

func (m *Model) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	x, y := msg.X, msg.Y
	if y == 0 && x >= max(m.width-28, 0) {
		m.cycleTheme(1)
		return nil
	}
	if y == 1 {
		if next, ok := navigationAtFor(m.navigation, x, m.width); ok {
			if next == sectionExtensions {
				m.extensionsOpen = !m.extensionsOpen
				if m.extensionsOpen {
					return m.selectSection(next)
				}
				m.resizeVisibleSession()
				m.status = "Extensions menu closed"
				return nil
			}
			if next == m.section && next != sectionExplorer && m.sectionSupportsSidebar() {
				m.sidebar = !m.sidebar
				m.focusSidebar = m.sidebar
				m.resizeVisibleSession()
				state := "collapsed"
				if m.sidebar {
					state = "expanded"
				}
				m.status = m.sectionName() + " sidebar " + state
				return nil
			}
			return m.selectSection(next)
		}
		return nil
	}
	if m.extensionsOpen && y == baseTopHeight {
		if id, ok := m.extensionAt(x); ok {
			return m.selectExtension(id)
		}
		return nil
	}
	topHeight := m.topHeight()
	if y < topHeight || y >= m.height-footerHeight {
		return nil
	}
	if m.sidebarDrawn() && x < sidebarWidth {
		if y == topHeight && x >= sidebarWidth-4 {
			m.sidebar = false
			m.resizeVisibleSession()
			return nil
		}
		rowIndex := m.sidebarScroll + y - topHeight - panelHeader
		rows := m.sidebarRows()
		if rowIndex >= 0 && rowIndex < len(rows) {
			m.focusSidebar = true
			m.sidebarCursor = rowIndex
			return m.activateRow(rows, rowIndex)
		}
		return nil
	}

	m.focusSidebar = false
	if session, _ := m.visibleSession(); session != nil {
		m.capture = true
		session.Focus()
		m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		return m.forwardMouse(msg)
	}
	return nil
}

func (m *Model) handleWheel(msg tea.MouseWheelMsg) tea.Cmd {
	mouse := msg.Mouse()
	delta := 3
	if mouse.Button == tea.MouseWheelUp {
		delta = -3
	}
	if m.sidebarDrawn() && mouse.X < sidebarWidth {
		m.sidebarScroll = max(m.sidebarScroll+delta, 0)
		maxScroll := max(len(m.sidebarRows())-m.sidebarVisibleHeight(), 0)
		m.sidebarScroll = min(m.sidebarScroll, maxScroll)
		return nil
	}
	if session, _ := m.visibleSession(); session != nil {
		return m.forwardMouse(msg)
	}
	return nil
}

func (m *Model) selectSection(next section) tea.Cmd {
	if next != m.section {
		m.rememberSectionView()
		m.blurVisibleSession()
		m.section = next
		m.restoreSectionView(next)
	}
	if next == sectionTerminal {
		m.page = pageTerminal
		m.sidebar = false
		m.focusSidebar = false
		m.resizeVisibleSession()
		return m.ensureShell()
	}
	switch next {
	case sectionOverview:
		m.page = pageOverview
		m.capture = false
	case sectionExplorer:
		m.sidebar = false
		m.focusSidebar = false
		m.page = pageFile
		m.resizeVisibleSession()
		return m.ensureEditor("")
	case sectionTools:
		m.page = pageTool
		m.capture = false
		if m.selectedTool == "" {
			rows := m.sidebarRows()
			if index := firstRowOfKind(rows, rowTool, rowPackage); index >= 0 {
				m.sidebarCursor = index
				return m.activateRow(rows, index)
			}
		}
	case sectionAgents:
		m.page = pageAgent
		if m.selectedAgent == "" {
			m.selectedAgent = m.firstAgentID()
		}
		m.resizeVisibleSession()
		return m.ensureAgent(m.selectedAgent)
	case sectionSkills:
		m.page = pageSkill
		m.capture = false
		if m.selectedSkill == "" {
			rows := m.sidebarRows()
			if index := firstRowOfKind(rows, rowSkill); index >= 0 {
				m.sidebarCursor = index
				return m.activateRow(rows, index)
			}
		}
	case sectionExtensions:
		m.page = pageConnector
		m.capture = false
		if m.selectedConnector == "" {
			m.selectedConnector = firstEnabledConnector(m.profile)
		}
		m.restoreExtensionView(m.selectedConnector)
	}
	m.clampCursor()
	return nil
}

func (m *Model) selectExtension(id string) tea.Cmd {
	for _, connector := range m.profile.Connectors {
		if connector.Enabled && connector.ID == id {
			if m.section == sectionExtensions && m.selectedConnector == id {
				m.status = connector.Name + " extension remains selected"
				return nil
			}
			if m.section != sectionExtensions {
				m.rememberSectionView()
				m.blurVisibleSession()
				m.section = sectionExtensions
				m.restoreSectionView(sectionExtensions)
			} else {
				m.rememberExtensionView()
			}
			m.selectedConnector = id
			m.page = pageConnector
			m.capture = false
			m.restoreExtensionView(id)
			m.status = connector.Name + " extension selected"
			return nil
		}
	}
	return nil
}

func (m *Model) ensureSelectedExtensionAction() {
	rows := m.sidebarRows()
	for _, row := range rows {
		if row.Kind == rowConnectorAction && row.ID == m.selectedAction {
			m.clampCursor()
			return
		}
	}
	m.selectedAction = ""
	if index := firstRowOfKind(rows, rowConnectorAction); index >= 0 {
		m.sidebarCursor = index
		m.selectedAction = rows[index].ID
	}
	m.clampCursor()
}

func defaultSectionView(value section) sectionViewState {
	switch value {
	case sectionExplorer, sectionTerminal:
		return sectionViewState{}
	case sectionOverview:
		return sectionViewState{Sidebar: true}
	default:
		return sectionViewState{Sidebar: true, FocusSidebar: true}
	}
}

func (m *Model) rememberSectionView() {
	m.sectionViews[m.section] = sectionViewState{
		Sidebar:      m.sidebar,
		FocusSidebar: m.focusSidebar,
		Cursor:       m.sidebarCursor,
		Scroll:       m.sidebarScroll,
	}
	if m.section == sectionExtensions {
		m.rememberExtensionView()
	}
}

func (m *Model) restoreSectionView(value section) {
	state, ok := m.sectionViews[value]
	if !ok {
		state = defaultSectionView(value)
	}
	m.sidebar = state.Sidebar
	m.focusSidebar = state.FocusSidebar
	m.sidebarCursor = state.Cursor
	m.sidebarScroll = state.Scroll
}

func (m *Model) rememberExtensionView() {
	if m.selectedConnector == "" {
		return
	}
	m.extensionViews[m.selectedConnector] = extensionViewState{
		SelectedAction: m.selectedAction,
		Cursor:         m.sidebarCursor,
		Scroll:         m.sidebarScroll,
	}
}

func (m *Model) restoreExtensionView(id string) {
	if state, ok := m.extensionViews[id]; ok {
		m.selectedAction = state.SelectedAction
		m.sidebarCursor = state.Cursor
		m.sidebarScroll = state.Scroll
	} else {
		m.selectedAction = ""
		m.sidebarCursor = 0
		m.sidebarScroll = 0
	}
	m.ensureSelectedExtensionAction()
}

func (m *Model) blurVisibleSession() {
	if session, id := m.visibleSession(); session != nil {
		session.Blur()
		// NvChad is stateful but normally does not need to make progress while
		// hidden. Shells and agents may be running intentional background work,
		// so leave those live rather than silently freezing a heavy job.
		if id == editorSessionID {
			if err := session.Pause(); err != nil {
				m.status = fmt.Sprintf("Pause %s: %v", session.Name(), err)
			}
		}
	}
	m.capture = false
}

func (m *Model) resumeVisibleSession() {
	if session, _ := m.visibleSession(); session != nil {
		if err := session.Resume(); err != nil {
			m.status = fmt.Sprintf("Resume %s: %v", session.Name(), err)
		}
	}
}

func (m *Model) sectionSupportsSidebar() bool {
	return m.section != sectionExplorer && m.section != sectionTerminal
}

func (m *Model) cycleSection(delta int) tea.Cmd {
	current := 0
	for index, item := range m.navigation {
		if item.Section == m.section {
			current = index
			break
		}
	}
	next := (current + delta + len(m.navigation)) % len(m.navigation)
	return m.selectSection(m.navigation[next].Section)
}

func (m *Model) cycleTheme(delta int) {
	m.themeIndex = (m.themeIndex + delta + len(theme.All)) % len(theme.All)
	current := theme.All[m.themeIndex]
	m.status = "Theme changed to " + current.Name
	if err := settings.Save(settings.Settings{Theme: current.Name}); err != nil {
		m.status += " (could not save: " + err.Error() + ")"
	}
}

func (m *Model) activateRow(rows []sidebarRow, index int) tea.Cmd {
	if index < 0 || index >= len(rows) {
		return nil
	}
	row := rows[index]
	switch row.Kind {
	case rowCategory:
		m.expanded[row.ID] = !m.expanded[row.ID]
	case rowTool, rowPackage:
		m.selectedTool = row.ID
		m.page = pageTool
		m.focusSidebar = false
		m.capture = false
	case rowAgent:
		if row.ID != m.selectedAgent {
			m.blurVisibleSession()
		}
		m.selectedAgent = row.ID
		m.page = pageAgent
		m.focusSidebar = false
		m.capture = true
		m.clampCursor()
		return m.ensureAgent(row.ID)
	case rowSkill:
		m.selectedSkill = row.ID
		m.page = pageSkill
		m.focusSidebar = false
		m.capture = false
	case rowConnectorTool:
		m.page = pageConnector
		m.focusSidebar = false
		m.capture = false
	case rowConnectorAction:
		m.selectedAction = row.ID
		m.page = pageConnector
		m.focusSidebar = false
		m.capture = false
		return m.runSelectedConnectorAction()
	}
	m.clampCursor()
	return nil
}

func (m *Model) revealRow(rows []sidebarRow, index int) tea.Cmd {
	if index < 0 || index >= len(rows) {
		return nil
	}
	switch rows[index].Kind {
	case rowTool, rowPackage, rowAgent, rowSkill:
		// Merely moving through the contextual pane updates the main pane.
		// Agent rows also surface their live embedded session.
	default:
		return nil
	}
	return m.activateRow(rows, index)
}

func firstRowOfKind(rows []sidebarRow, kinds ...rowKind) int {
	for index, row := range rows {
		for _, kind := range kinds {
			if row.Kind == kind {
				return index
			}
		}
	}
	return -1
}
