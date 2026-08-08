package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/files"
	"lisanalgaib/internal/inventory"
	"lisanalgaib/internal/safefile"
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
	viewsKey := extensionGroupKey(id, "views")
	m.defaultExpanded(viewsKey)
	rows = append(rows, sidebarRow{Kind: rowCategory, ID: viewsKey, Label: fmt.Sprintf("Views (%d)", len(state.Manifest.Views)), Expanded: m.expanded[viewsKey]})
	if m.expanded[viewsKey] {
		for _, view := range state.Manifest.Views {
			active := view.ID == m.selectedView
			mark := "○"
			if active {
				mark = "●"
			}
			rows = append(rows, sidebarRow{Kind: rowConnectorView, ID: view.ID, Label: mark + " VIEW  " + view.Title, Depth: 1, Active: active})
		}
	}
	actionsKey := extensionGroupKey(id, "actions")
	m.defaultExpanded(actionsKey)
	rows = append(rows, sidebarRow{Kind: rowCategory, ID: actionsKey, Label: fmt.Sprintf("Actions (%d)", len(state.Manifest.Actions)), Expanded: m.expanded[actionsKey]})
	if m.expanded[actionsKey] {
		for _, action := range state.Manifest.Actions {
			active := action.ID == m.selectedAction
			mark := "○"
			if active {
				mark = "●"
			}
			rows = append(rows, sidebarRow{Kind: rowConnectorAction, ID: action.ID, Label: mark + " " + action.Name, Depth: 1, Active: active})
		}
	}
	sessionsKey := extensionGroupKey(id, "sessions")
	m.defaultExpanded(sessionsKey)
	rows = append(rows, sidebarRow{Kind: rowCategory, ID: sessionsKey, Label: fmt.Sprintf("Sessions (%d)", len(state.Manifest.Sessions)), Expanded: m.expanded[sessionsKey]})
	if m.expanded[sessionsKey] {
		for _, session := range state.Manifest.Sessions {
			active := session.ID == m.selectedSession
			mark := "○"
			if active {
				mark = "●"
			}
			rows = append(rows, sidebarRow{Kind: rowConnectorSession, ID: session.ID, Label: mark + " " + session.Name, Depth: 1, Active: active})
		}
	}
	artifactsKey := extensionGroupKey(id, "artifacts")
	m.defaultExpanded(artifactsKey)
	job := m.connectorJobs[id]
	rows = append(rows, sidebarRow{Kind: rowCategory, ID: artifactsKey, Label: fmt.Sprintf("Artifacts (%d)", len(job.Artifacts)), Expanded: m.expanded[artifactsKey]})
	if m.expanded[artifactsKey] {
		for _, artifact := range job.Artifacts {
			active := artifact.ID == m.selectedArtifact
			mark := "○"
			if active {
				mark = "●"
			}
			rows = append(rows, sidebarRow{Kind: rowConnectorArtifact, ID: artifact.ID, Label: mark + " " + artifact.Name, Subtitle: fmt.Sprintf("%d B", artifact.Size), Depth: 1, Active: active})
		}
	}
	return rows
}

func connectorInputAffordance(input connectorapi.InputSpec, value string) (string, string) {
	value = emptyDash(value)
	switch input.Kind {
	case connectorapi.InputText:
		return "TEXT    " + input.Label, "[" + value + "]"
	case connectorapi.InputNumber:
		return "NUMBER  " + input.Label, "[# " + value + "]"
	case connectorapi.InputBoolean:
		if value == "true" {
			return "TOGGLE  " + input.Label, "● ON"
		}
		return "TOGGLE  " + input.Label, "○ OFF"
	case connectorapi.InputSelect:
		return "SELECT  " + input.Label, "‹ " + value + " ▾ ›"
	default:
		return "INPUT   " + input.Label, "[" + value + "]"
	}
}

func (m *Model) defaultExpanded(key string) {
	if _, exists := m.expanded[key]; !exists {
		m.expanded[key] = true
	}
}

func extensionGroupKey(extensionID, group string) string {
	return "extension:" + extensionID + ":" + group
}

func (m *Model) runSelectedConnectorAction() tea.Cmd {
	connectorID := m.selectedConnector
	state, ok := m.connectorState(connectorID)
	if !ok || !state.Online || m.selectedAction == "" || m.connectorRunning[connectorID] {
		return nil
	}
	actionID := m.selectedAction
	action, ok := m.connectorAction(actionID)
	if !ok {
		return nil
	}
	inputs := m.connectorActionInputs(connectorID, actionID)
	if err := validateConnectorActionInputs(action, inputs); err != nil {
		m.status = "Extension input: " + err.Error()
		return nil
	}
	endpoint := state.Config.Endpoint
	m.extensionScrollY = 0
	m.connectorRunning[connectorID] = true
	m.status = "Running " + actionID + " in extension " + state.Config.Name + "…"
	return func() tea.Msg {
		request := connectorapi.StartJobRequest{ActionID: actionID, Inputs: inputs}
		job, err := connectorapi.StartJob(m.ctx, endpoint, request)
		return connectorActionMsg{ConnectorID: connectorID, ActionID: actionID, Job: job, Err: err}
	}
}

func validateConnectorActionInputs(action connectorapi.ActionDescriptor, values map[string]string) error {
	for _, input := range action.Inputs {
		value := values[input.ID]
		if input.Required && strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", input.Label)
		}
		if strings.TrimSpace(value) == "" && !input.Required {
			continue
		}
		switch input.Kind {
		case connectorapi.InputText:
			if input.Pattern != "" {
				pattern, err := regexp.Compile(input.Pattern)
				if err != nil || !pattern.MatchString(value) {
					return fmt.Errorf("%s has an invalid value", input.Label)
				}
			}
		case connectorapi.InputNumber:
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("%s must be a whole number", input.Label)
			}
			if input.Min != 0 && number < input.Min {
				return fmt.Errorf("%s must be at least %d", input.Label, input.Min)
			}
			if input.Max != 0 && number > input.Max {
				return fmt.Errorf("%s must be at most %d", input.Label, input.Max)
			}
		case connectorapi.InputBoolean:
			if value != "true" && value != "false" {
				return fmt.Errorf("%s must be true or false", input.Label)
			}
		case connectorapi.InputSelect:
			valid := false
			for _, option := range input.Options {
				valid = valid || option.Value == value
			}
			if !valid {
				return fmt.Errorf("%s must use an advertised option", input.Label)
			}
		}
	}
	return nil
}

func pollConnectorJobCmd(parent context.Context, connectorID, endpoint, jobID string) tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-parent.Done():
			return connectorJobPollMsg{ConnectorID: connectorID, Err: parent.Err()}
		case <-timer.C:
		}
		job, err := connectorapi.FetchJob(parent, endpoint, jobID)
		return connectorJobPollMsg{ConnectorID: connectorID, Job: job, Err: err}
	}
}

func (m *Model) connectorEndpoint(id string) string {
	state, _ := m.connectorState(id)
	return state.Config.Endpoint
}

func (m *Model) connectorManifest(id string) connectorapi.Manifest {
	state, _ := m.connectorState(id)
	return state.Manifest
}

func refreshConnectorViewsCmd(parent context.Context, connectorID, endpoint string, descriptors []connectorapi.ViewDescriptor) tea.Cmd {
	return func() tea.Msg {
		views := make(map[string]connectorapi.View, len(descriptors))
		for _, descriptor := range descriptors {
			view, err := connectorapi.FetchView(parent, endpoint, descriptor.ID)
			if err != nil {
				return connectorViewsMsg{ConnectorID: connectorID, Err: err}
			}
			views[descriptor.ID] = view
		}
		return connectorViewsMsg{ConnectorID: connectorID, Views: views}
	}
}

func (m *Model) seedConnectorInputs(state connectorapi.State) {
	if !state.Online {
		return
	}
	if m.connectorInputs[state.Config.ID] == nil {
		m.connectorInputs[state.Config.ID] = map[string]map[string]string{}
	}
	for _, action := range state.Manifest.Actions {
		if m.connectorInputs[state.Config.ID][action.ID] == nil {
			m.connectorInputs[state.Config.ID][action.ID] = map[string]string{}
		}
		for _, input := range action.Inputs {
			if _, exists := m.connectorInputs[state.Config.ID][action.ID][input.ID]; !exists {
				m.connectorInputs[state.Config.ID][action.ID][input.ID] = input.Default
			}
		}
	}
}

func (m *Model) connectorInputValue(connectorID, actionID, inputID string) string {
	return m.connectorInputs[connectorID][actionID][inputID]
}

func (m *Model) connectorActionInputs(connectorID, actionID string) map[string]string {
	source := m.connectorInputs[connectorID][actionID]
	result := make(map[string]string, len(source))
	for id, value := range source {
		result[id] = value
	}
	return result
}

func (m *Model) connectorAction(id string) (connectorapi.ActionDescriptor, bool) {
	state, ok := m.connectorState(m.selectedConnector)
	if !ok {
		return connectorapi.ActionDescriptor{}, false
	}
	for _, action := range state.Manifest.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return connectorapi.ActionDescriptor{}, false
}

func (m *Model) connectorInput(actionID, inputID string) (connectorapi.InputSpec, bool) {
	action, ok := m.connectorAction(actionID)
	if !ok {
		return connectorapi.InputSpec{}, false
	}
	for _, input := range action.Inputs {
		if input.ID == inputID {
			return input, true
		}
	}
	return connectorapi.InputSpec{}, false
}

func (m *Model) beginConnectorInput(actionID, inputID string) tea.Cmd {
	input, ok := m.connectorInput(actionID, inputID)
	if !ok {
		return nil
	}
	values := m.connectorInputs[m.selectedConnector][actionID]
	switch input.Kind {
	case connectorapi.InputBoolean:
		values[inputID] = fmt.Sprintf("%t", values[inputID] != "true")
	case connectorapi.InputSelect:
		index := 0
		for optionIndex, option := range input.Options {
			if option.Value == values[inputID] {
				index = (optionIndex + 1) % len(input.Options)
				break
			}
		}
		values[inputID] = input.Options[index].Value
	default:
		m.extensionInputEdit = actionID + ":" + inputID
		m.extensionInputText = values[inputID]
		m.status = "Editing " + input.Label + " · Enter commits · Esc cancels"
	}
	return nil
}

func (m *Model) extensionMainControls() []extensionControl {
	if m.selectedArtifact != "" {
		return []extensionControl{{Kind: extensionControlSave, ArtifactID: m.selectedArtifact}}
	}
	if m.selectedSession != "" {
		return []extensionControl{{Kind: extensionControlOpen}}
	}
	if m.selectedAction == "" {
		return nil
	}
	action, ok := m.connectorAction(m.selectedAction)
	if !ok {
		return nil
	}
	controls := make([]extensionControl, 0, len(action.Inputs)+1)
	for _, input := range action.Inputs {
		controls = append(controls, extensionControl{Kind: extensionControlInput, ActionID: action.ID, InputID: input.ID})
	}
	return append(controls, extensionControl{Kind: extensionControlRun, ActionID: action.ID})
}

func (m *Model) clampExtensionControl() {
	controls := m.extensionMainControls()
	m.extensionControlCursor = min(max(m.extensionControlCursor, 0), max(len(controls)-1, 0))
}

func (m *Model) moveExtensionControl(delta int) bool {
	controls := m.extensionMainControls()
	if len(controls) == 0 {
		return false
	}
	m.extensionControlCursor = min(max(m.extensionControlCursor+delta, 0), len(controls)-1)
	m.ensureExtensionControlVisible()
	m.status = "Main control selected · Enter/Space activates · H returns to the sidebar"
	return true
}

func (m *Model) activateExtensionControl(index int) tea.Cmd {
	controls := m.extensionMainControls()
	if index < 0 || index >= len(controls) {
		return nil
	}
	m.extensionControlCursor = index
	control := controls[index]
	switch control.Kind {
	case extensionControlInput:
		return m.beginConnectorInput(control.ActionID, control.InputID)
	case extensionControlRun:
		return m.runSelectedConnectorAction()
	case extensionControlOpen:
		return m.openOrCaptureConnectorSession()
	case extensionControlSave:
		return m.exportConnectorArtifact(control.ArtifactID)
	default:
		return nil
	}
}

func (m *Model) handleExtensionInputKey(key string) tea.Cmd {
	parts := strings.SplitN(m.extensionInputEdit, ":", 2)
	if len(parts) != 2 {
		m.extensionInputEdit = ""
		return nil
	}
	switch key {
	case "esc":
		m.extensionInputEdit = ""
		m.extensionInputText = ""
		m.status = "Extension input edit cancelled"
	case "enter":
		m.connectorInputs[m.selectedConnector][parts[0]][parts[1]] = m.extensionInputText
		m.extensionInputEdit = ""
		m.extensionInputText = ""
		m.status = "Extension input updated"
	case "backspace":
		runes := []rune(m.extensionInputText)
		if len(runes) > 0 {
			m.extensionInputText = string(runes[:len(runes)-1])
		}
	case "space":
		m.extensionInputText += " "
	default:
		if runes := []rune(key); len(runes) == 1 && runes[0] >= 0x20 {
			m.extensionInputText += key
		}
	}
	return nil
}

func (m *Model) cancelConnectorJob() tea.Cmd {
	job, ok := m.connectorJobs[m.selectedConnector]
	if !ok || job.Terminal() {
		return nil
	}
	connectorID, endpoint := m.selectedConnector, m.connectorEndpoint(m.selectedConnector)
	return func() tea.Msg {
		cancelled, err := connectorapi.CancelJob(m.ctx, endpoint, job.ID)
		return connectorJobPollMsg{ConnectorID: connectorID, Job: cancelled, Err: err}
	}
}

func (m *Model) openOrCaptureConnectorSession() tea.Cmd {
	connectorID := m.selectedConnector
	if m.connectorSessionPending[connectorID] {
		m.status = "Extension session is already opening…"
		return nil
	}
	current, hasCurrent := m.connectorSessions[connectorID]
	if hasCurrent && current.SessionID == m.selectedSession && current.Status == "open" {
		m.extensionSessionCapture = true
		m.status = "Extension session input active · Ctrl-G returns to wrapper"
		return nil
	}
	endpoint := m.connectorEndpoint(connectorID)
	request := connectorapi.OpenSessionRequest{SessionID: m.selectedSession, Rows: max(m.mainContentHeight()-4, 1), Columns: max(m.mainPaneWidth()-4, 1)}
	m.connectorSessionPending[connectorID] = true
	return func() tea.Msg {
		if hasCurrent && current.Status == "open" {
			if _, err := connectorapi.CloseSession(m.ctx, endpoint, current.ID); err != nil {
				return connectorSessionMsg{ConnectorID: connectorID, Err: fmt.Errorf("close previous session: %w", err)}
			}
		}
		session, err := connectorapi.OpenSession(m.ctx, endpoint, request)
		return connectorSessionMsg{ConnectorID: connectorID, Session: session, ClearInput: true, Capture: true, Err: err}
	}
}

func (m *Model) handleExtensionSessionKey(key string) tea.Cmd {
	if key == "ctrl+g" || key == "esc" {
		m.extensionSessionCapture = false
		m.status = "Wrapper controls active"
		return nil
	}
	if key == "backspace" {
		runes := []rune(m.extensionSessionInput)
		if len(runes) > 0 {
			m.extensionSessionInput = string(runes[:len(runes)-1])
		}
		return nil
	}
	if key == "enter" {
		current, ok := m.connectorSessions[m.selectedConnector]
		if !ok || current.Status != "open" {
			return nil
		}
		connectorID, endpoint, input := m.selectedConnector, m.connectorEndpoint(m.selectedConnector), m.extensionSessionInput
		return func() tea.Msg {
			session, err := connectorapi.SendSessionInput(m.ctx, endpoint, current.ID, input+"\n")
			return connectorSessionMsg{ConnectorID: connectorID, Session: session, ClearInput: true, Capture: true, Err: err}
		}
	}
	if key == "space" {
		m.extensionSessionInput += " "
	} else if runes := []rune(key); len(runes) == 1 && runes[0] >= 0x20 {
		m.extensionSessionInput += key
	}
	return nil
}

func (m *Model) resizeConnectorSession() tea.Cmd {
	connectorID := m.selectedConnector
	session, ok := m.connectorSessions[connectorID]
	if !ok || session.Status != "open" {
		return nil
	}
	endpoint := m.connectorEndpoint(connectorID)
	rows, columns := max(m.mainContentHeight()-4, 1), max(m.mainPaneWidth()-4, 1)
	return func() tea.Msg {
		resized, err := connectorapi.ResizeSession(m.ctx, endpoint, session.ID, rows, columns)
		return connectorSessionMsg{ConnectorID: connectorID, Session: resized, Err: err}
	}
}

func (m *Model) exportConnectorArtifact(artifactID string) tea.Cmd {
	connectorID := m.selectedConnector
	job := m.connectorJobs[connectorID]
	var metadata connectorapi.Artifact
	for _, artifact := range job.Artifacts {
		if artifact.ID == artifactID {
			metadata = artifact
			break
		}
	}
	endpoint := m.connectorEndpoint(connectorID)
	return func() tea.Msg {
		data, err := connectorapi.FetchArtifact(m.ctx, endpoint, job.ID, metadata)
		if err != nil {
			return connectorArtifactMsg{ConnectorID: connectorID, Err: err}
		}
		directory := strings.TrimSpace(os.Getenv("LISAN_SHARED_DIR"))
		if directory == "" {
			directory = filepath.Join(m.root, "shared")
		}
		name := files.SafeName(metadata.Name, metadata.ID+".bin")
		path := filepath.Join(directory, name)
		if err := safefile.Write(path, data, 0o755, 0o644); err != nil {
			return connectorArtifactMsg{ConnectorID: connectorID, Err: err}
		}
		return connectorArtifactMsg{ConnectorID: connectorID, Path: path}
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
