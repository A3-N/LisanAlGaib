package configui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/teaprogram"
	"lisanalgaib/internal/textsafe"
	"lisanalgaib/internal/theme"
)

type rowKind int

const (
	rowDropdown rowKind = iota
	rowPreset
	rowProfile
	rowOption
	rowConnector
	rowConnectorGrant
	rowTerminal
)

const contentStart = 6

var dropdowns = []string{
	"agents",
	"presets",
	"profiles",
	"features",
	"connectors",
	"terminal-docker-shell",
	"tools",
}

var expandedByDefault = map[string]bool{
	"agents":     true,
	"connectors": true,
	"tools":      true,
}

type row struct {
	kind        rowKind
	label       string
	description string
	preset      int
	profile     int
	option      int
	connector   int
	grant       string
	setting     string
	value       string
}

type Model struct {
	document appconfig.Document
	path     string
	working  appconfig.Profile
	source   string
	expanded map[string]bool
	cursor   int
	scroll   int
	width    int
	height   int
	status   string
	err      error
	saved    bool
}

func New(document appconfig.Document, path string) *Model {
	active, ok := document.Active()
	if !ok {
		active = appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	}
	expanded := make(map[string]bool, len(dropdowns))
	for _, id := range dropdowns {
		expanded[id] = expandedByDefault[id]
	}
	model := &Model{
		document: document, path: path, working: active.Clone(),
		source: "profile:" + active.ID, expanded: expanded,
		width: 100, height: 36,
		status: "Enter/Space selects or toggles · Ctrl-S saves and activates · Esc cancels",
	}
	model.cursor = model.clampIndex(0)
	return model
}

func Run(document appconfig.Document, path string) (appconfig.Document, bool, error) {
	model := New(document, path)
	result, err := teaprogram.Run(model)
	if err != nil {
		return document, false, err
	}
	final, ok := result.(*Model)
	if !ok {
		return document, false, fmt.Errorf("unexpected config UI result %T", result)
	}
	return final.document, final.saved, final.err
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 1)
		m.height = max(msg.Height, 1)
		m.clampScroll()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit
		case "j", "down":
			m.cursor = m.clampIndex(m.cursor + 1)
			m.clampScroll()
		case "k", "up":
			m.cursor = m.clampIndex(m.cursor - 1)
			m.clampScroll()
		case "g", "home":
			m.cursor = m.clampIndex(0)
			m.clampScroll()
		case "G", "shift+g", "end":
			m.cursor = m.clampIndex(len(m.rows()) - 1)
			m.clampScroll()
		case " ", "space", "enter":
			return m, m.activate(m.cursor)
		case "ctrl+s":
			return m, m.save()
		case "1", "2", "3", "4":
			index := int(msg.String()[0] - '1')
			if index < len(appconfig.Presets) {
				m.applyPreset(index)
			}
		}
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			m.scroll = max(m.scroll-3, 0)
		} else {
			m.scroll = min(m.scroll+3, m.maximumScroll())
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			index := m.scroll + msg.Y - contentStart
			rows := m.rows()
			if index >= 0 && index < len(rows) {
				m.cursor = index
				return m, m.activate(index)
			}
		}
	}
	return m, nil
}

func (m *Model) rows() []row {
	var rows []row

	rows = m.appendOptions(rows, "agents", "MENTATS", appconfig.Agents)

	presets := make([]row, 0, len(appconfig.Presets))
	for index, preset := range appconfig.Presets {
		presets = append(presets, row{kind: rowPreset, label: fmt.Sprintf("%d  %s", index+1, preset.Name), description: preset.Description, preset: index})
	}
	rows = m.appendDropdown(rows, "presets", "PRESETS", presets)

	profiles := make([]row, 0, len(m.document.Profiles))
	for index := len(m.document.Profiles) - 1; index >= 0; index-- {
		profile := m.document.Profiles[index]
		profiles = append(profiles, row{kind: rowProfile, label: fmt.Sprintf("v%03d  %s", profile.Revision, profile.Name), description: profile.UpdatedAt.Local().Format("2006-01-02 15:04") + " · " + profile.Signature(), profile: index})
	}
	rows = m.appendDropdown(rows, "profiles", "SAVED PROFILES", profiles)

	rows = m.appendOptions(rows, "features", "FEATURES", appconfig.Features)

	connectors := make([]row, 0, len(m.working.Connectors))
	for index, connector := range m.working.Connectors {
		connectors = append(connectors, row{kind: rowConnector, label: connector.Name, description: connector.Description + " · disabled by default", connector: index})
		connectors = append(connectors, requestedGrantRows(connector, index)...)
	}
	rows = m.appendDropdown(rows, "connectors", "EXTENSION CONTAINERS", connectors)

	var dockerShells []row
	for _, option := range []struct{ value, description string }{
		{"fish", "Friendly interactive shell (default)"},
		{"bash", "Bourne Again Shell"},
		{"zsh", "Z shell"},
		{"sh", "Minimal POSIX shell"},
	} {
		dockerShells = append(dockerShells, row{kind: rowTerminal, label: option.value, description: option.description, setting: "docker-shell", value: option.value})
	}
	rows = m.appendDropdown(rows, "terminal-docker-shell", "SIETCH TABR SHELL", dockerShells)

	return m.appendOptions(rows, "tools", "TOOLS", appconfig.Tools)
}

func (m *Model) appendOptions(rows []row, id, label string, category appconfig.Category) []row {
	var options []row
	for index, option := range appconfig.Options {
		if option.Category == category {
			options = append(options, row{kind: rowOption, label: option.Label, description: option.Description, option: index})
		}
	}
	return m.appendDropdown(rows, id, label, options)
}

func (m *Model) appendDropdown(rows []row, id, label string, children []row) []row {
	rows = append(rows, row{kind: rowDropdown, label: label, value: id})
	if m.expanded[id] {
		rows = append(rows, children...)
	}
	return rows
}

func (m *Model) clampIndex(index int) int {
	rows := m.rows()
	if len(rows) == 0 {
		return 0
	}
	return min(max(index, 0), len(rows)-1)
}

func (m *Model) activate(index int) tea.Cmd {
	rows := m.rows()
	if index < 0 || index >= len(rows) {
		return nil
	}
	selected := rows[index]
	switch selected.kind {
	case rowDropdown:
		m.expanded[selected.value] = !m.expanded[selected.value]
		state := "collapsed"
		if m.expanded[selected.value] {
			state = "expanded"
		}
		m.status = selected.label + " " + state
		m.clampScroll()
	case rowPreset:
		m.applyPreset(selected.preset)
	case rowProfile:
		profile := m.document.Profiles[selected.profile]
		m.working = profile.Clone()
		m.source = "profile:" + profile.ID
		m.status = profile.Name + " loaded; press Ctrl-S to save and activate"
	case rowOption:
		option := appconfig.Options[selected.option]
		enabled := !m.working.Enabled(option.Category, option.ID)
		m.working.Set(option.Category, option.ID, enabled)
		m.working.Preset = ""
		m.source = "custom"
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		m.status = option.Label + " " + state
	case rowConnector:
		connector := &m.working.Connectors[selected.connector]
		connector.Enabled = !connector.Enabled
		m.working.Preset = ""
		m.source = "custom"
		state := "disabled"
		if connector.Enabled {
			state = "enabled"
		}
		m.status = connector.Name + " extension " + state
	case rowConnectorGrant:
		connector := &m.working.Connectors[selected.connector]
		enabled := toggleGrant(&connector.Grants, selected.grant)
		m.working.Preset = ""
		m.source = "custom"
		state := "denied"
		if enabled {
			state = "granted"
		}
		m.status = connector.Name + " · " + selected.label + " " + state
	case rowTerminal:
		m.working.Terminal.DockerShell = selected.value
		m.working.Preset = ""
		m.source = "custom"
		m.status = selected.label + " selected"
	}
	return nil
}

func (m *Model) applyPreset(index int) {
	preset := appconfig.Presets[index]
	configuredConnectors := m.working.Clone().Connectors
	for connectorIndex := range configuredConnectors {
		configuredConnectors[connectorIndex].Enabled = false
	}
	m.working = appconfig.ProfileFromPreset(preset, time.Now())
	// Presets include every discovered extension definition but always start
	// with its lifecycle disabled. Enabling a sidecar remains an explicit step.
	m.working.Connectors = append(m.working.Connectors, configuredConnectors...)
	m.source = "preset:" + preset.ID
	m.status = preset.Name + " loaded; press Ctrl-S to save and activate"
}

func (m *Model) save() tea.Cmd {
	selected := m.document.SaveSelection(m.working, time.Now())
	if err := appconfig.Save(m.path, m.document); err != nil {
		m.err = err
		m.status = "Could not save: " + err.Error()
		return nil
	}
	m.working = selected
	m.source = "profile:" + selected.ID
	m.saved = true
	m.status = selected.Name + " saved and activated"
	return tea.Quit
}

func (m *Model) visibleRows() int { return max(m.height-contentStart-1, 1) }

func (m *Model) maximumScroll() int { return max(len(m.rows())-m.visibleRows(), 0) }

func (m *Model) clampScroll() {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor = 0
		m.scroll = 0
		return
	}
	m.cursor = min(max(m.cursor, 0), len(rows)-1)
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+m.visibleRows() {
		m.scroll = m.cursor - m.visibleRows() + 1
	}
	m.scroll = min(max(m.scroll, 0), m.maximumScroll())
}

func (m *Model) View() tea.View {
	t := theme.All[0]
	if m.width < 32 || m.height < 10 {
		return compactView(m.width, m.height, " Resize terminal for config", "LisanAlGaib Configuration")
	}
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Background))
	fit := func(value string) string { return ansi.Truncate(value, m.width, "…") }
	title := lipgloss.NewStyle().Width(m.width).Bold(true).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Panel)).Render(fit("  LISANALGAIB  //  GOLDEN PATH"))
	active, _ := m.document.Active()
	header := []string{
		title,
		base.Width(m.width).Render(fit("  Config  " + textsafe.Label(m.path, 1000))),
		base.Width(m.width).Render(fit("  Active  " + active.Name + "  ·  Working  " + m.working.Signature())),
		base.Width(m.width).Render(fit("  Sietch Tabr  " + m.working.Terminal.DockerUser + "@tabr:" + m.working.Terminal.DockerWorkdir + " · shell=" + m.working.Terminal.DockerShell + " · host terminal stays current · host shell uses the system default")),
		base.Width(m.width).Foreground(lipgloss.Color(t.Muted)).Render(fit("  ■/□ multiple choice · ●/○ one choice · ▾/▸ dropdown")),
		base.Width(m.width).Foreground(lipgloss.Color(t.Muted)).Render(fit("  ↑/↓ or mouse · Enter/Space select or toggle · Ctrl-S save + activate · Esc cancel")),
	}
	rows := m.rows()
	end := min(m.scroll+m.visibleRows(), len(rows))
	for index := m.scroll; index < end; index++ {
		current := rows[index]
		line := ""
		switch current.kind {
		case rowDropdown:
			mark := "▸"
			if m.expanded[current.value] {
				mark = "▾"
			}
			line = "  " + mark + " " + current.label
		case rowPreset:
			mark := "○"
			if m.source == "preset:"+appconfig.Presets[current.preset].ID {
				mark = "●"
			}
			line = "    " + mark + " " + current.label
		case rowProfile:
			mark := "○"
			if m.source == "profile:"+m.document.Profiles[current.profile].ID {
				mark = "●"
			}
			line = "    " + mark + " " + current.label
		case rowOption:
			option := appconfig.Options[current.option]
			mark := "□"
			if m.working.Enabled(option.Category, option.ID) {
				mark = "■"
			}
			line = "    " + mark + " " + current.label
		case rowConnector:
			mark := "□"
			if m.working.Connectors[current.connector].Enabled {
				mark = "■"
			}
			line = "    " + mark + " " + current.label
		case rowConnectorGrant:
			mark := "□"
			if grantEnabled(m.working.Connectors[current.connector].Grants, current.grant) {
				mark = "■"
			}
			line = "      " + mark + " " + current.label
		case rowTerminal:
			selected := current.setting == "docker-shell" && m.working.Terminal.DockerShell == current.value
			mark := "○"
			if selected {
				mark = "●"
			}
			line = "    " + mark + " " + current.label
		}
		if current.description != "" {
			available := max(m.width-ansi.StringWidth(line)-5, 0)
			if available > 8 {
				description := ansi.Truncate(current.description, available, "…")
				line += strings.Repeat(" ", max(m.width-ansi.StringWidth(line)-ansi.StringWidth(description)-2, 2)) + description
			}
		}
		style := base.Width(m.width)
		if current.kind == rowDropdown {
			style = style.Bold(true).Foreground(lipgloss.Color(t.Secondary))
		}
		if index == m.cursor {
			style = style.Bold(true).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Selection))
		}
		header = append(header, style.Render(fit(line)))
	}
	for len(header) < m.height-1 {
		header = append(header, base.Width(m.width).Render(""))
	}
	footer := base.Width(m.width).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Panel)).Render(fit("  " + textsafe.Label(m.status, 1000)))
	header = append(header, footer)
	view := tea.NewView(strings.Join(header, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "LisanAlGaib Configuration"
	view.BackgroundColor = lipgloss.Color(t.Background)
	view.ForegroundColor = lipgloss.Color(t.Text)
	return view
}

func requestedGrantRows(connector appconfig.ConnectorConfig, index int) []row {
	definitions := []struct {
		requested   bool
		id          string
		label       string
		description string
	}{
		{connector.Requests.Internet, "internet", "Internet access", "Attach the sidecar to the outbound Docker network"},
		{connector.Requests.PersistentState, "persistent-state", "Persistent state", "Keep extension-owned data in a managed volume"},
		{connector.Requests.SharedRead, "shared-read", "Read shared files", "Mount Lisan's shared exchange directory read-only"},
		{connector.Requests.SharedWrite, "shared-write", "Write shared files", "Allow writes to Lisan's shared exchange directory"},
	}
	var rows []row
	for _, definition := range definitions {
		if definition.requested {
			rows = append(rows, row{kind: rowConnectorGrant, label: definition.label, description: definition.description, connector: index, grant: definition.id})
		}
	}
	return rows
}

func grantEnabled(grants appconfig.ExtensionGrants, id string) bool {
	switch id {
	case "internet":
		return grants.Internet
	case "persistent-state":
		return grants.PersistentState
	case "shared-read":
		return grants.SharedRead
	case "shared-write":
		return grants.SharedWrite
	default:
		return false
	}
}

func toggleGrant(grants *appconfig.ExtensionGrants, id string) bool {
	switch id {
	case "internet":
		grants.Internet = !grants.Internet
		return grants.Internet
	case "persistent-state":
		grants.PersistentState = !grants.PersistentState
		return grants.PersistentState
	case "shared-read":
		grants.SharedRead = !grants.SharedRead
		if !grants.SharedRead {
			grants.SharedWrite = false
		}
		return grants.SharedRead
	case "shared-write":
		grants.SharedWrite = !grants.SharedWrite
		if grants.SharedWrite {
			grants.SharedRead = true
		}
		return grants.SharedWrite
	default:
		return false
	}
}

func compactView(width, height int, message, title string) tea.View {
	t := theme.All[0]
	content := lipgloss.NewStyle().Width(width).Height(height).
		Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Background)).
		Render(ansi.Truncate(message, width, "…"))
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = title
	view.BackgroundColor = lipgloss.Color(t.Background)
	view.ForegroundColor = lipgloss.Color(t.Text)
	return view
}
