package ui

import (
	"fmt"
	"runtime"
	"sort"

	tea "charm.land/bubbletea/v2"

	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/inventory"
)

func (m *Model) sidebarRows() []sidebarRow {
	switch m.section {
	case sectionExplorer:
		return nil
	case sectionTools:
		return m.toolRows(false)
	case sectionAgents:
		return m.toolRows(true)
	case sectionExtensions:
		return m.connectorRows()
	default:
		return nil
	}
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
	m.extensionScrollY = 0
	m.connectorRunning[connectorID] = true
	m.status = "Running " + actionID + " in extension " + state.Config.Name + "…"
	return func() tea.Msg {
		result, err := connectorapi.RunAction(m.ctx, endpoint, actionID)
		return connectorActionMsg{ConnectorID: connectorID, ActionID: actionID, Result: result, Err: err}
	}
}

func (m *Model) findTool(id string) (inventory.Tool, bool) {
	for _, tool := range m.inventory.Tools {
		if tool.ID == id {
			return tool, true
		}
	}
	return inventory.Tool{}, false
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
