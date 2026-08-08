package ui

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/skills"
)

func (m *Model) sidebarRows() []sidebarRow {
	switch m.section {
	case sectionExplorer:
		return nil
	case sectionTools:
		return m.toolRows(false)
	case sectionAgents:
		return m.toolRows(true)
	case sectionSkills:
		return m.skillRows()
	case sectionOverview:
		return m.overviewRows()
	case sectionExtensions:
		return m.connectorRows()
	default:
		return nil
	}
}

func (m *Model) overviewRows() []sidebarRow {
	var rows []sidebarRow
	status := func(label string, ready, total int) {
		if m.loading {
			rows = append(rows, sidebarRow{Kind: rowInfo, Label: "◌ Scanning " + label + "…"})
			return
		}
		mark := "✓"
		if ready < total {
			mark = "✗"
		}
		rows = append(rows, sidebarRow{Kind: rowInfo, Label: fmt.Sprintf("%s %s  %d/%d ready", mark, label, ready, total)})
	}
	if m.profile.Feature("tools") || m.profile.Feature("files") || m.profile.Feature("terminal") {
		ready := 0
		for _, tool := range m.inventory.Tools {
			if !tool.Agent && tool.Installed {
				ready++
			}
		}
		total := 0
		for _, tool := range m.inventory.Tools {
			if !tool.Agent {
				total++
			}
		}
		status("tools", ready, total)
	}
	if m.profile.Feature("agents") {
		ready, total := 0, 0
		for _, tool := range m.inventory.Tools {
			if tool.Agent {
				total++
				if tool.Installed {
					ready++
				}
			}
		}
		status("agents", ready, total)
	}
	if m.profile.Feature("skills") {
		valid := 0
		for _, skill := range m.skills {
			if skill.Valid {
				valid++
			}
		}
		status("skills", valid, len(m.skills))
	}
	if len(m.enabledExtensions()) > 0 {
		status("extensions", onlineConnectors(m.connectors), len(m.enabledExtensions()))
	}
	rows = append(rows,
		sidebarRow{Kind: rowInfo, Label: ""},
		sidebarRow{Kind: rowInfo, Label: "SHORTCUTS"},
		sidebarRow{Kind: rowInfo, Label: "Tab / Shift-Tab  pages"},
		sidebarRow{Kind: rowInfo, Label: "F2  theme"},
		sidebarRow{Kind: rowInfo, Label: "Ctrl-B  sidebar"},
		sidebarRow{Kind: rowInfo, Label: "R  rescan"},
		sidebarRow{Kind: rowInfo, Label: "?  help"},
		sidebarRow{Kind: rowInfo, Label: "Ctrl-C  quit"},
	)
	return rows
}

func (m *Model) toolRows(agentsOnly bool) []sidebarRow {
	if agentsOnly {
		var rows []sidebarRow
		for _, tool := range m.inventory.Tools {
			if tool.Agent {
				status := "missing"
				if tool.Installed {
					status = "ready"
				}
				rows = append(rows, sidebarRow{Kind: rowAgent, ID: tool.ID, Label: tool.Name, Subtitle: status})
			}
		}
		return rows
	}

	groups := make(map[string][]inventory.Tool)
	for _, tool := range m.inventory.Tools {
		groups[tool.Category] = append(groups[tool.Category], tool)
	}
	var categories []string
	for category := range groups {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	var rows []sidebarRow
	for _, category := range categories {
		rows = append(rows, sidebarRow{Kind: rowCategory, ID: category, Label: category, Expanded: m.expanded[category]})
		if m.expanded[category] {
			for _, tool := range groups[category] {
				status := ""
				if tool.Installed {
					status = ""
				}
				rows = append(rows, sidebarRow{Kind: rowTool, ID: tool.ID, Label: status + " " + tool.Name, Depth: 1})
			}
		}
	}
	if runtime.GOOS == "linux" {
		aptID := "APT manual"
		rows = append(rows, sidebarRow{Kind: rowCategory, ID: aptID, Label: fmt.Sprintf("APT manual (%d)", len(m.inventory.APTManual)), Expanded: m.expanded[aptID]})
		if m.expanded[aptID] {
			for _, pkg := range m.inventory.APTManual {
				rows = append(rows, sidebarRow{Kind: rowPackage, ID: "apt:" + pkg.Name, Label: pkg.Name, Subtitle: pkg.Version, Depth: 1})
			}
		}
	}
	return rows
}

func (m *Model) skillRows() []sidebarRow {
	groups := map[string][]skills.Skill{}
	for _, skill := range m.skills {
		groups[skill.Scope] = append(groups[skill.Scope], skill)
	}
	order := []string{"workspace", "user", "admin", "system"}
	var rows []sidebarRow
	for _, scope := range order {
		group := groups[scope]
		if len(group) == 0 {
			continue
		}
		rows = append(rows, sidebarRow{Kind: rowCategory, ID: scope, Label: fmt.Sprintf("%s (%d)", strings.ToUpper(scope), len(group)), Expanded: m.expanded[scope]})
		if m.expanded[scope] {
			for _, skill := range group {
				icon := ""
				if !skill.Valid {
					icon = ""
				}
				rows = append(rows, sidebarRow{Kind: rowSkill, ID: skill.Path, Label: icon + " " + skill.Name, Subtitle: skill.Provider, Depth: 1})
			}
		}
	}
	return rows
}

func (m *Model) connectorState(id string) (connectorapi.State, bool) {
	for _, state := range m.connectors {
		if state.Config.ID == id {
			return state, true
		}
	}
	return connectorapi.State{}, false
}

func (m *Model) connectorRows() []sidebarRow {
	id := m.selectedConnector
	state, ok := m.connectorState(id)
	if !ok || !state.Online {
		return nil
	}
	var rows []sidebarRow
	for _, panel := range state.Manifest.UI.Sidebar {
		key := extensionPanelKey(id, panel.ID)
		if _, exists := m.expanded[key]; !exists {
			m.expanded[key] = panel.Expanded
		}
		count := len(state.Manifest.Tools)
		if panel.Kind == connectorapi.PanelActions {
			count = len(state.Manifest.Actions)
		}
		rows = append(rows, sidebarRow{Kind: rowCategory, ID: key, Label: fmt.Sprintf("%s (%d)", panel.Title, count), Expanded: m.expanded[key]})
		if !m.expanded[key] {
			continue
		}
		switch panel.Kind {
		case connectorapi.PanelTools:
			for _, tool := range state.Manifest.Tools {
				mark := ""
				if tool.Ready {
					mark = ""
				}
				rows = append(rows, sidebarRow{Kind: rowConnectorTool, ID: tool.ID, Label: mark + " " + tool.Name, Subtitle: tool.Version, Depth: 1})
			}
		case connectorapi.PanelActions:
			for _, action := range state.Manifest.Actions {
				rows = append(rows, sidebarRow{Kind: rowConnectorAction, ID: action.ID, Label: "▷ " + action.Name, Depth: 1})
			}
		}
	}
	return rows
}

func extensionPanelKey(extensionID, panelID string) string {
	return "extension:" + extensionID + ":" + panelID
}

func (m *Model) runSelectedConnectorAction() tea.Cmd {
	connectorID := m.selectedConnector
	state, ok := m.connectorState(connectorID)
	if !ok || !state.Online || m.selectedAction == "" || m.connectorRunning[connectorID] {
		return nil
	}
	actionID := m.selectedAction
	endpoint := state.Config.Endpoint
	m.connectorRunning[connectorID] = true
	m.status = "Running " + actionID + " in extension " + state.Config.Name + "…"
	return func() tea.Msg {
		result, err := connectorapi.RunAction(m.ctx, endpoint, actionID)
		return connectorActionMsg{ConnectorID: connectorID, ActionID: actionID, Result: result, Err: err}
	}
}

func (m *Model) launchSelectedSkill() tea.Cmd {
	if m.selectedSkill == "" {
		return nil
	}
	m.rememberSectionView()
	m.blurVisibleSession()
	m.page = pageFile
	m.section = sectionExplorer
	m.restoreSectionView(sectionExplorer)
	m.capture = true
	return m.ensureEditor(m.selectedSkill)
}

func (m *Model) findTool(id string) (inventory.Tool, bool) {
	for _, tool := range m.inventory.Tools {
		if tool.ID == id {
			return tool, true
		}
	}
	return inventory.Tool{}, false
}

func (m *Model) findSkill(path string) (skills.Skill, bool) {
	for _, skill := range m.skills {
		if skill.Path == path {
			return skill, true
		}
	}
	return skills.Skill{}, false
}

func (m *Model) clampCursor() {
	rows := m.sidebarRows()
	m.sidebarCursor = min(max(m.sidebarCursor, 0), max(len(rows)-1, 0))
	m.ensureSidebarVisible()
}

func (m *Model) ensureSidebarVisible() {
	visible := m.sidebarVisibleHeight()
	if m.sidebarCursor < m.sidebarScroll {
		m.sidebarScroll = m.sidebarCursor
	}
	if m.sidebarCursor >= m.sidebarScroll+visible {
		m.sidebarScroll = m.sidebarCursor - visible + 1
	}
	m.sidebarScroll = max(m.sidebarScroll, 0)
}

func (m *Model) sidebarVisibleHeight() int {
	return max(m.height-m.topHeight()-footerHeight-panelHeader, 1)
}

func (m *Model) mainContentHeight() int {
	return max(m.height-m.topHeight()-footerHeight, 1)
}
