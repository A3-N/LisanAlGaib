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
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"lisanalgaib/internal/appconfig"
	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/settings"
	terminalhost "lisanalgaib/internal/terminal"
	"lisanalgaib/internal/theme"
)

const (
	sidebarWidth         = 33
	baseTopHeight        = 2
	footerHeight         = 1
	panelHeader          = 2
	embeddedHeaderHeight = 1
	verticalScrollStep   = 3
)

type section int

const (
	sectionOverview section = iota
	sectionExplorer
	sectionAgents
	sectionTerminal
	sectionExtensions
	sectionDisabled
)

type page int

const (
	pageOverview page = iota
	pageFile
	pageTool
	pageAgent
	pageTerminal
	pageConnector
	pageHelp
	pageDisabled
)

type rowKind int

const (
	rowCategory rowKind = iota
	rowTool
	rowPackage
	rowAgent
	rowConnectorView
	rowConnectorAction
	rowConnectorSession
	rowConnectorArtifact
)

type sidebarRow struct {
	Kind     rowKind
	ID       string
	Label    string
	Subtitle string
	Depth    int
	Expanded bool
	Active   bool
}

type sectionViewState struct {
	Sidebar      bool
	FocusSidebar bool
	Cursor       int
	Scroll       int
}

type extensionViewState struct {
	SelectedView     string
	SelectedAction   string
	SelectedSession  string
	SelectedArtifact string
	Control          int
	Cursor           int
	Scroll           int
	Vertical         int
}

type extensionControlKind int

const (
	extensionControlInput extensionControlKind = iota
	extensionControlRun
	extensionControlOpen
	extensionControlSave
)

type extensionControl struct {
	Kind       extensionControlKind
	ActionID   string
	InputID    string
	ArtifactID string
}

type navItem struct {
	Section section
	Icon    string
	Name    string
}

var navigation = []navItem{
	{sectionOverview, "󰋜", "Overview"},
	{sectionExplorer, "", "Files"},
	{sectionAgents, "󰚩", "Mentats"},
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
	Connectors []connectorapi.State
}

type connectorActionMsg struct {
	ConnectorID string
	ActionID    string
	Job         connectorapi.Job
	Err         error
}

type connectorJobPollMsg struct {
	ConnectorID string
	Job         connectorapi.Job
	Err         error
}

type connectorSessionMsg struct {
	ConnectorID string
	Session     connectorapi.Session
	ClearInput  bool
	Capture     bool
	Err         error
}

type connectorArtifactMsg struct {
	ConnectorID string
	Path        string
	Err         error
}

type connectorViewsMsg struct {
	ConnectorID string
	Views       map[string]connectorapi.View
	Err         error
}

type Model struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	root                    string
	profile                 appconfig.Profile
	navigation              []navItem
	width                   int
	height                  int
	section                 section
	page                    page
	previousPage            page
	sidebar                 bool
	focusSidebar            bool
	sidebarCursor           int
	sidebarScroll           int
	themeIndex              int
	inventory               inventory.Snapshot
	connectors              []connectorapi.State
	extensionsOpen          bool
	selectedConnector       string
	selectedView            string
	selectedAction          string
	selectedSession         string
	selectedArtifact        string
	connectorJobs           map[string]connectorapi.Job
	connectorRunning        map[string]bool
	connectorInputs         map[string]map[string]map[string]string
	connectorSessions       map[string]connectorapi.Session
	connectorSessionPending map[string]bool
	extensionInputEdit      string
	extensionInputText      string
	extensionInputCursor    int
	extensionControlCursor  int
	extensionSessionCapture bool
	extensionSessionInput   string
	extensionSessionCursor  int
	selectedTool            string
	selectedAgent           string
	editorPath              string
	expanded                map[string]bool
	sectionViews            map[section]sectionViewState
	extensionViews          map[string]extensionViewState
	extensionScrollY        int
	loading                 bool
	status                  string
	sessions                map[string]*terminalhost.Session
	starting                map[string]bool
	terminalWorkspace       terminalWorkspace
	capture                 bool
	copy                    terminalCopyState
	sessionScrollY          map[string]int
	sessionScrollback       map[string]int
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
	navigation := navigationFor(profile)
	initialSection, initialPage := initialView(navigation)
	return &Model{
		ctx:          ctx,
		cancel:       cancel,
		root:         root,
		profile:      profile.Clone(),
		navigation:   navigation,
		width:        120,
		height:       36,
		section:      initialSection,
		page:         initialPage,
		previousPage: initialPage,
		sidebar:      false,
		focusSidebar: false,
		themeIndex:   themeIndex,
		expanded: map[string]bool{
			"workspace": true,
			"user":      true,
		},
		loading:                 true,
		status:                  "Scanning configured inventory and extensions…",
		sessions:                make(map[string]*terminalhost.Session),
		starting:                make(map[string]bool),
		terminalWorkspace:       newTerminalWorkspace(),
		sessionScrollY:          make(map[string]int),
		sessionScrollback:       make(map[string]int),
		connectorJobs:           make(map[string]connectorapi.Job),
		connectorRunning:        make(map[string]bool),
		connectorInputs:         make(map[string]map[string]map[string]string),
		connectorSessions:       make(map[string]connectorapi.Session),
		connectorSessionPending: make(map[string]bool),
		sectionViews:            make(map[section]sectionViewState),
		extensionViews:          make(map[string]extensionViewState),
		selectedConnector:       firstEnabledConnector(profile),
	}
}

func (m *Model) Init() tea.Cmd {
	return refreshCmd(m.ctx, m.profile)
}

func refreshCmd(parent context.Context, profile appconfig.Profile) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 12*time.Second)
		defer cancel()
		return refreshMsg{
			Inventory:  inventory.ScanSelected(ctx, inventorySelection(profile)),
			Connectors: connectorapi.Scan(ctx, profile.Connectors),
		}
	}
}

func navigationFor(profile appconfig.Profile) []navItem {
	var items []navItem
	for _, item := range navigation {
		enabled := false
		switch item.Section {
		case sectionOverview:
			enabled = profile.Feature("overview")
		case sectionExplorer:
			enabled = profile.Feature("files")
		case sectionAgents:
			enabled = profile.Feature("agents") && anyAgentEnabled(profile)
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

func initialView(items []navItem) (section, page) {
	if len(items) == 0 {
		return sectionDisabled, pageDisabled
	}
	selected := items[0].Section
	switch selected {
	case sectionOverview:
		return selected, pageOverview
	case sectionExplorer:
		return selected, pageFile
	case sectionAgents:
		return selected, pageAgent
	case sectionTerminal:
		return selected, pageTerminal
	case sectionExtensions:
		return selected, pageConnector
	default:
		return sectionDisabled, pageDisabled
	}
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
	for _, id := range appconfig.AgentIDs() {
		if profile.Agent(id) {
			return true
		}
	}
	return false
}

func inventorySelection(profile appconfig.Profile) inventory.Selection {
	ids := map[string]bool{}
	if profile.Feature("tools") {
		for _, option := range appconfig.Options {
			if option.Category == appconfig.Tools {
				ids[option.ID] = profile.Tool(option.ID)
			}
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
		for _, id := range appconfig.AgentIDs() {
			ids[id] = profile.Agent(id)
		}
	}
	return inventory.Selection{IDs: ids, APTManual: profile.Feature("tools")}
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 1)
		m.height = max(msg.Height, 1)
		m.clampCursor()
		m.resizeVisibleSession()
		m.clampExtensionScroll()
		return m, m.resizeConnectorSession()
	case refreshMsg:
		m.inventory = msg.Inventory
		m.connectors = msg.Connectors
		for _, extension := range m.connectors {
			for _, group := range []string{"views", "actions", "sessions", "artifacts"} {
				key := extensionGroupKey(extension.Config.ID, group)
				if _, exists := m.expanded[key]; !exists {
					m.expanded[key] = true
				}
			}
			m.seedConnectorInputs(extension)
		}
		m.loading = false
		online := 0
		for _, connector := range m.connectors {
			if connector.Online {
				online++
			}
		}
		if runtime.GOOS == "linux" {
			m.status = fmt.Sprintf("Detected %d tools, %d manual apt packages, %d/%d extensions online", len(m.inventory.Tools), len(m.inventory.APTManual), online, len(m.connectors))
		} else {
			m.status = fmt.Sprintf("Detected %d tools, %d/%d extensions online", len(m.inventory.Tools), online, len(m.connectors))
		}
		m.clampCursor()
		if m.section == sectionAgents {
			if m.selectedAgent == "" {
				m.selectedAgent = m.firstAgentID()
			}
			return m, m.ensureAgent(m.selectedAgent)
		}
		if m.section == sectionExplorer {
			return m, m.ensureEditor("")
		}
		if m.section == sectionTerminal {
			return m, m.ensureShell()
		}
		if m.section == sectionExtensions {
			m.ensureSelectedExtensionItems()
			m.clampExtensionScroll()
		}
		return m, nil
	case connectorActionMsg:
		if msg.Err != nil {
			delete(m.connectorRunning, msg.ConnectorID)
			m.status = "Extension action failed: " + msg.Err.Error()
			return m, nil
		}
		m.connectorJobs[msg.ConnectorID] = msg.Job
		m.extensionScrollY = 0
		m.clampExtensionScroll()
		if msg.Job.Terminal() {
			delete(m.connectorRunning, msg.ConnectorID)
			m.status = fmt.Sprintf("Extension action %s %s", msg.ActionID, msg.Job.Status)
			return m, refreshConnectorViewsCmd(m.ctx, msg.ConnectorID, m.connectorEndpoint(msg.ConnectorID), m.connectorManifest(msg.ConnectorID).Views)
		}
		m.status = "Extension job " + msg.Job.ID + " " + msg.Job.Status
		return m, pollConnectorJobCmd(m.ctx, msg.ConnectorID, m.connectorEndpoint(msg.ConnectorID), msg.Job.ID)
	case connectorJobPollMsg:
		if msg.Err != nil {
			delete(m.connectorRunning, msg.ConnectorID)
			m.status = "Extension job polling failed: " + msg.Err.Error()
			return m, nil
		}
		m.connectorJobs[msg.ConnectorID] = msg.Job
		m.clampExtensionScroll()
		if msg.Job.Terminal() {
			delete(m.connectorRunning, msg.ConnectorID)
			m.status = "Extension job " + msg.Job.Status
			return m, refreshConnectorViewsCmd(m.ctx, msg.ConnectorID, m.connectorEndpoint(msg.ConnectorID), m.connectorManifest(msg.ConnectorID).Views)
		}
		return m, pollConnectorJobCmd(m.ctx, msg.ConnectorID, m.connectorEndpoint(msg.ConnectorID), msg.Job.ID)
	case connectorSessionMsg:
		delete(m.connectorSessionPending, msg.ConnectorID)
		if msg.Err != nil {
			m.status = "Extension session failed: " + msg.Err.Error()
			return m, nil
		}
		m.connectorSessions[msg.ConnectorID] = msg.Session
		if msg.ClearInput {
			m.extensionSessionInput = ""
			m.extensionSessionCursor = 0
		}
		if msg.Capture {
			m.extensionSessionCapture = msg.Session.Status == "open"
		}
		m.status = "Extension session " + msg.Session.Status
		return m, nil
	case connectorArtifactMsg:
		if msg.Err != nil {
			m.status = "Artifact export failed: " + msg.Err.Error()
		} else {
			m.status = "Artifact exported to " + msg.Path
		}
		return m, nil
	case connectorViewsMsg:
		if msg.Err != nil {
			m.status = "Extension view refresh failed: " + msg.Err.Error()
			return m, nil
		}
		for index := range m.connectors {
			if m.connectors[index].Config.ID == msg.ConnectorID {
				m.connectors[index].Views = msg.Views
				break
			}
		}
		m.clampExtensionScroll()
		return m, nil
	case terminalStartedMsg:
		return m, m.handleTerminalStarted(msg)
	case terminalEventMsg:
		return m, m.handleTerminalEvent(msg)
	case tea.ClipboardMsg:
		return m, m.handlePaste(msg.Content)
	case tea.FocusMsg:
		if session, _ := m.visibleSession(); session != nil && m.capture {
			_ = session.Focus()
		}
		return m, nil
	case tea.BlurMsg:
		if session, _ := m.visibleSession(); session != nil && m.capture {
			_ = session.Blur()
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.copy.Active {
			return m, m.handleCopyMouse(msg)
		}
		if msg.Button == tea.MouseLeft {
			if msg.Mod.Contains(tea.ModShift) {
				if session, _ := m.visibleSession(); session != nil {
					m.enterCopyMode()
					if _, ok := m.copyViewportPoint(msg.Mouse()); ok {
						return m, m.handleCopyMouse(msg)
					}
					m.leaveCopyMode(true)
				}
			}
			return m, m.handleClick(msg)
		}
		return m, m.forwardMouse(msg)
	case tea.MouseReleaseMsg:
		if m.copy.Active {
			return m, m.handleCopyMouse(msg)
		}
		return m, m.forwardMouse(msg)
	case tea.MouseWheelMsg:
		if m.copy.Active {
			return m, m.handleCopyMouse(msg)
		}
		return m, m.handleWheel(msg)
	case tea.MouseMotionMsg:
		if m.copy.Active {
			return m, m.handleCopyMouse(msg)
		}
		return m, m.forwardMouse(msg)
	case tea.PasteMsg:
		if m.copy.Active {
			m.status = "Exit copy mode before pasting"
			return m, nil
		}
		return m, m.handlePaste(msg.Content)
	case tea.KeyPressMsg:
		if m.copy.Active {
			return m, m.handleCopyKey(msg.String())
		}
		if msg.String() == "ctrl+shift+c" {
			return m, m.enterCopyMode()
		}
		if msg.String() == "ctrl+shift+v" {
			return m, func() tea.Msg { return tea.ReadClipboard() }
		}
		if m.page == pageTerminal {
			switch msg.String() {
			case "ctrl+shift+t":
				return m, m.newTerminalTab()
			case "ctrl+shift+w":
				return m, m.closeActiveTerminal()
			case "ctrl+pgup":
				return m, m.cycleTerminalTab(-1)
			case "ctrl+pgdown":
				return m, m.cycleTerminalTab(1)
			}
		}
		if msg.String() == "ctrl+c" {
			// Embedded terminals own Ctrl-C while they have input capture so a
			// user can interrupt a child process. Everywhere else, including
			// Overview with stale extension capture, Ctrl-C quits Lisan.
			if session, _ := m.visibleSession(); session == nil || !m.capture {
				return m, tea.Quit
			}
		}
		if m.extensionInputEdit != "" {
			return m, m.handleExtensionInputKey(msg.String(), msg.Key().Text)
		}
		if m.extensionSessionCapture {
			return m, m.handleExtensionSessionKey(msg.String(), msg.Key().Text)
		}
		if session, id := m.visibleSession(); session != nil && m.capture {
			if msg.String() == "ctrl+shift+g" {
				if err := session.SendKey(uv.KeyPressEvent(uv.Key{Code: 'g', Mod: uv.ModCtrl})); err != nil {
					m.status = "Terminal input unavailable: " + err.Error()
				}
				return m, nil
			}
			if msg.String() == "ctrl+g" {
				m.capture = false
				m.focusSidebar = m.sidebar
				session.Blur()
				m.status = "Wrapper controls active; Ctrl-G or click the pane to return"
				return m, nil
			}
			if m.sessionScrollY[id] > 0 {
				m.moveSessionVertically(true)
			}
			key := uv.Key(msg.Key())
			if err := session.SendKey(uv.KeyPressEvent(key)); err != nil {
				m.status = "Terminal input unavailable: " + err.Error()
			}
			return m, nil
		}
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *Model) handlePaste(content string) tea.Cmd {
	if content == "" {
		return nil
	}
	if m.extensionInputEdit != "" {
		m.extensionInputText, m.extensionInputCursor = insertLineText(
			m.extensionInputText, m.extensionInputCursor, singleLinePaste(content),
		)
		return nil
	}
	if m.extensionSessionCapture {
		m.extensionSessionInput, m.extensionSessionCursor = insertLineText(
			m.extensionSessionInput, m.extensionSessionCursor, singleLinePaste(content),
		)
		return nil
	}
	if session, id := m.visibleSession(); session != nil && m.capture {
		if m.sessionScrollY[id] > 0 {
			m.moveSessionVertically(true)
		}
		if err := session.Paste(content); err != nil {
			m.status = "Could not queue paste: " + err.Error()
		}
		return nil
	}
	if session, _ := m.visibleSession(); session != nil {
		m.status = "Activate the embedded pane with Ctrl-G or a click before pasting"
	}
	return nil
}

func singleLinePaste(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Map(func(value rune) rune {
		switch {
		case value == '\n' || value == '\r' || value == '\t':
			return ' '
		case value < 0x20 || value == 0x7f:
			return -1
		default:
			return value
		}
	}, content)
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
	case "c":
		if session, _ := m.visibleSession(); session != nil {
			return m.enterCopyMode()
		}
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
			if len(m.navigation) > 0 {
				return m.selectSection(m.navigation[0].Section)
			}
			m.status = "No pages are enabled; run lisan config to add one"
		}
		return nil
	case "r":
		if m.loading {
			m.status = "A refresh is already running…"
			return nil
		}
		m.loading = true
		m.status = "Refreshing dynamic inventory…"
		return refreshCmd(m.ctx, m.profile)
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
	if m.page == pageConnector {
		switch key {
		case "j", "down":
			if m.moveExtensionControl(1) {
				return nil
			}
		case "k", "up":
			if m.moveExtensionControl(-1) {
				return nil
			}
		}
	}
	switch key {
	case "j", "down":
		if m.scrollMainVertically(1) {
			return nil
		}
	case "k", "up":
		if m.scrollMainVertically(-1) {
			return nil
		}
	case "pgdown":
		if m.scrollMainVertically(max(m.mainContentHeight()-1, 1)) {
			return nil
		}
	case "pgup":
		if m.scrollMainVertically(-max(m.mainContentHeight()-1, 1)) {
			return nil
		}
	case "g", "home":
		if m.moveMainVertically(false) {
			return nil
		}
	case "G", "shift+g", "end":
		if m.moveMainVertically(true) {
			return nil
		}
	}
	switch m.page {
	case pageFile:
		if key == "enter" || key == "i" {
			return m.ensureEditor(m.editorPath)
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
		if key == "enter" || key == " " || key == "i" {
			if len(m.extensionMainControls()) > 0 {
				return m.activateExtensionControl(m.extensionControlCursor)
			}
			if m.selectedSession != "" {
				return m.openOrCaptureConnectorSession()
			}
		}
		if key == "c" {
			return m.cancelConnectorJob()
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
			if next == m.section && next == sectionOverview {
				if !m.profile.Feature("tools") {
					m.status = "Tools are disabled in the active config profile"
					return nil
				}
				m.sidebar = !m.sidebar
				m.focusSidebar = m.sidebar
				if !m.sidebar {
					m.page = pageOverview
				}
				m.resizeVisibleSession()
				state := "expanded"
				if !m.sidebar {
					state = "collapsed"
				}
				m.status = "Overview tools pane " + state
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
		if m.section != sectionOverview && y == topHeight && x >= sidebarWidth-4 {
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
	if m.page == pageTerminal {
		mainX := 0
		if m.sidebarDrawn() {
			mainX = sidebarWidth
		}
		localX, localY := x-mainX, y-topHeight
		if localY == 0 {
			if control, ok := m.terminalWorkspace.toolbarAt(localX, m.mainPaneWidth()); ok {
				switch control.Kind {
				case terminalToolbarTab:
					return m.activateTerminalTab(control.Tab)
				case terminalToolbarNew:
					return m.newTerminalTab()
				case terminalToolbarSplitVertical:
					return m.splitTerminal(terminalSplitVertical)
				case terminalToolbarSplitHorizontal:
					return m.splitTerminal(terminalSplitHorizontal)
				case terminalToolbarClose:
					return m.closeActiveTerminal()
				}
			}
			return nil
		}
		for _, pane := range m.terminalWorkspace.paneRects(m.mainPaneWidth(), m.mainContentHeight()) {
			if !pane.contains(localX, localY) {
				continue
			}
			m.activateTerminalPane(pane.SessionID, true)
			if m.sessionScrollY[pane.SessionID] > 0 {
				m.moveSessionVertically(true)
			}
			if !m.starting[pane.SessionID] {
				session := m.sessions[pane.SessionID]
				exited := session == nil
				if session != nil {
					exited, _ = session.Exited()
				}
				if exited {
					return m.ensureShellSession(pane.SessionID)
				}
			}
			return m.forwardMouse(msg)
		}
		return nil
	}
	if m.page == pageConnector {
		mainX := 0
		if m.sidebarDrawn() {
			mainX = sidebarWidth
		}
		if control, ok := m.extensionControlAt(x-mainX, y-topHeight); ok {
			m.extensionControlCursor = control
			return m.activateExtensionControl(control)
		}
	}
	if session, id := m.visibleSession(); session != nil {
		if m.sessionScrollY[id] > 0 {
			m.moveSessionVertically(true)
		}
		m.capture = true
		session.Focus()
		m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		return m.forwardMouse(msg)
	}
	return nil
}

func (m *Model) handleWheel(msg tea.MouseWheelMsg) tea.Cmd {
	mouse := msg.Mouse()
	if m.page == pageTerminal {
		localX, localY := mouse.X, mouse.Y-m.topHeight()
		for _, pane := range m.terminalWorkspace.paneRects(m.mainPaneWidth(), m.mainContentHeight()) {
			if pane.contains(localX, localY) {
				m.activateTerminalPane(pane.SessionID, m.capture)
				break
			}
		}
	}
	delta := verticalScrollStep
	if mouse.Button == tea.MouseWheelUp {
		delta = -verticalScrollStep
	}
	if m.sidebarDrawn() && mouse.X < sidebarWidth {
		m.sidebarScroll = max(m.sidebarScroll+delta, 0)
		maxScroll := max(len(m.sidebarRows())-m.sidebarVisibleHeight(), 0)
		m.sidebarScroll = min(m.sidebarScroll, maxScroll)
		return nil
	}
	if (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) && m.scrollMainVertically(delta) {
		return nil
	}
	if session, _ := m.visibleSession(); session != nil {
		return m.forwardMouse(msg)
	}
	return nil
}

func (m *Model) extensionVerticalLimit() int {
	if m.page != pageConnector {
		return 0
	}
	return max(len(m.wrappedConnectorLines(max(m.mainPaneWidth()-1, 1)))-m.mainContentHeight(), 0)
}

func (m *Model) clampExtensionScroll() {
	m.extensionScrollY = min(max(m.extensionScrollY, 0), m.extensionVerticalLimit())
}

func (m *Model) scrollMainVertically(delta int) bool {
	if m.scrollSessionVertically(-delta) {
		return true
	}
	if m.page != pageConnector {
		return false
	}
	limit := m.extensionVerticalLimit()
	m.extensionScrollY = min(max(m.extensionScrollY+delta, 0), limit)
	if limit == 0 {
		m.status = "Extension output fits the current height"
	} else {
		m.status = fmt.Sprintf("Extension vertical %d/%d · Home/End jumps", m.extensionScrollY, limit)
	}
	return true
}

func (m *Model) moveMainVertically(toBottom bool) bool {
	if m.moveSessionVertically(toBottom) {
		return true
	}
	if m.page != pageConnector {
		return false
	}
	if toBottom {
		m.extensionScrollY = m.extensionVerticalLimit()
		m.status = "Extension output bottom"
	} else {
		m.extensionScrollY = 0
		m.status = "Extension output top"
	}
	return true
}

func (m *Model) selectSection(next section) tea.Cmd {
	if next != sectionExtensions {
		m.extensionInputEdit = ""
		m.extensionInputText = ""
		m.extensionInputCursor = 0
		m.extensionSessionCapture = false
		m.extensionSessionInput = ""
		m.extensionSessionCursor = 0
	}
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
		m.sidebar = false
		m.focusSidebar = false
		m.page = pageOverview
		m.capture = false
	case sectionExplorer:
		m.sidebar = false
		m.focusSidebar = false
		m.page = pageFile
		m.resizeVisibleSession()
		return m.ensureEditor("")
	case sectionAgents:
		m.page = pageAgent
		if m.selectedAgent == "" {
			m.selectedAgent = m.firstAgentID()
		}
		m.resizeVisibleSession()
		return m.ensureAgent(m.selectedAgent)
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
			m.extensionSessionCapture = false
			m.extensionSessionInput = ""
			m.extensionSessionCursor = 0
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

func (m *Model) ensureSelectedExtensionItems() {
	state, ok := m.connectorState(m.selectedConnector)
	if !ok || !state.Online {
		return
	}
	viewExists := false
	for _, descriptor := range state.Manifest.Views {
		if descriptor.ID == m.selectedView {
			viewExists = true
		}
	}
	actionExists := false
	for _, descriptor := range state.Manifest.Actions {
		actionExists = actionExists || descriptor.ID == m.selectedAction
	}
	sessionExists := false
	for _, descriptor := range state.Manifest.Sessions {
		sessionExists = sessionExists || descriptor.ID == m.selectedSession
	}
	artifactExists := false
	for _, artifact := range m.connectorJobs[m.selectedConnector].Artifacts {
		artifactExists = artifactExists || artifact.ID == m.selectedArtifact
	}
	if viewExists || actionExists || sessionExists || artifactExists {
		m.clampExtensionControl()
		m.clampCursor()
		return
	}
	m.selectedView, m.selectedAction, m.selectedSession, m.selectedArtifact = "", "", "", ""
	for _, descriptor := range state.Manifest.Views {
		if descriptor.Default {
			m.selectedView = descriptor.ID
			break
		}
	}
	if m.selectedView == "" && len(state.Manifest.Views) > 0 {
		m.selectedView = state.Manifest.Views[0].ID
	} else if m.selectedView == "" && len(state.Manifest.Actions) > 0 {
		m.selectedAction = state.Manifest.Actions[0].ID
	} else if m.selectedView == "" && len(state.Manifest.Sessions) > 0 {
		m.selectedSession = state.Manifest.Sessions[0].ID
	}
	m.extensionControlCursor = 0
	m.clampCursor()
}

func defaultSectionView(value section) sectionViewState {
	switch value {
	case sectionOverview, sectionExplorer, sectionTerminal, sectionDisabled:
		return sectionViewState{}
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
		SelectedView:     m.selectedView,
		SelectedAction:   m.selectedAction,
		SelectedSession:  m.selectedSession,
		SelectedArtifact: m.selectedArtifact,
		Control:          m.extensionControlCursor,
		Cursor:           m.sidebarCursor,
		Scroll:           m.sidebarScroll,
		Vertical:         m.extensionScrollY,
	}
}

func (m *Model) restoreExtensionView(id string) {
	if state, ok := m.extensionViews[id]; ok {
		m.selectedView = state.SelectedView
		m.selectedAction = state.SelectedAction
		m.selectedSession = state.SelectedSession
		m.selectedArtifact = state.SelectedArtifact
		m.extensionControlCursor = state.Control
		m.sidebarCursor = state.Cursor
		m.sidebarScroll = state.Scroll
		m.extensionScrollY = state.Vertical
	} else {
		m.selectedView = ""
		m.selectedAction = ""
		m.selectedSession = ""
		m.selectedArtifact = ""
		m.extensionControlCursor = 0
		m.sidebarCursor = 0
		m.sidebarScroll = 0
		m.extensionScrollY = 0
	}
	m.ensureSelectedExtensionItems()
}

func (m *Model) blurVisibleSession() {
	if m.copy.Active {
		m.leaveCopyMode(false)
	}
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
	return m.section != sectionOverview && m.section != sectionExplorer && m.section != sectionTerminal && m.section != sectionDisabled
}

func (m *Model) cycleSection(delta int) tea.Cmd {
	if len(m.navigation) == 0 {
		m.status = "No pages are enabled; run lisan config to add one"
		return nil
	}
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
	if session := m.sessions[editorSessionID]; session != nil && session.Name() == "NvChad" {
		pairedTheme := current.NeovimTheme()
		session.SetBackgroundColor(lipgloss.Color(pairedTheme.Background))
		if exited, _ := session.Exited(); !exited {
			if err := session.SendText(vimThemeCommand(pairedTheme.Name)); err != nil {
				m.status += " (NvChad theme unavailable: " + err.Error() + ")"
			}
		}
	}
}

func (m *Model) activateRow(rows []sidebarRow, index int) tea.Cmd {
	if index < 0 || index >= len(rows) {
		return nil
	}
	row := rows[index]
	if row.Kind == rowConnectorView || row.Kind == rowConnectorAction || row.Kind == rowConnectorSession || row.Kind == rowConnectorArtifact {
		m.extensionSessionCapture = false
		m.extensionSessionInput = ""
		m.extensionSessionCursor = 0
	}
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
	case rowConnectorView:
		m.selectedView = row.ID
		m.selectedAction, m.selectedSession, m.selectedArtifact = "", "", ""
		m.extensionControlCursor = 0
		m.page = pageConnector
		m.focusSidebar = false
		m.capture = false
	case rowConnectorAction:
		m.selectedAction = row.ID
		m.selectedView, m.selectedSession, m.selectedArtifact = "", "", ""
		m.extensionControlCursor = 0
		m.extensionScrollY = 0
		m.page = pageConnector
		m.focusSidebar = false
		m.capture = false
		if action, ok := m.connectorAction(row.ID); ok {
			m.status = "Configure " + action.Name + " in the main pane"
		}
		m.clampCursor()
		return nil
	case rowConnectorSession:
		m.selectedSession = row.ID
		m.selectedView, m.selectedAction, m.selectedArtifact = "", "", ""
		m.extensionControlCursor = 0
		m.extensionScrollY = 0
		m.page = pageConnector
		m.focusSidebar = false
		m.status = "Session selected · use Open in the main pane"
		return nil
	case rowConnectorArtifact:
		m.selectedArtifact = row.ID
		m.selectedView, m.selectedAction, m.selectedSession = "", "", ""
		m.extensionControlCursor = 0
		m.extensionScrollY = 0
		m.page = pageConnector
		m.focusSidebar = false
		m.status = "Artifact selected · use Save in the main pane"
		return nil
	}
	m.clampCursor()
	return nil
}

func (m *Model) revealRow(rows []sidebarRow, index int) tea.Cmd {
	if index < 0 || index >= len(rows) {
		return nil
	}
	switch rows[index].Kind {
	case rowTool, rowPackage, rowAgent, rowConnectorView:
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
