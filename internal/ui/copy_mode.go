package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	terminalhost "lisanalgaib/internal/terminal"
)

type terminalCopyState struct {
	Active        bool
	SessionID     string
	Anchor        terminalhost.ViewportPoint
	Focus         terminalhost.ViewportPoint
	HasSelection  bool
	Dragging      bool
	ReturnCapture bool
}

func (m *Model) enterCopyMode() tea.Cmd {
	session, id := m.visibleSession()
	if session == nil {
		m.status = "No terminal viewport is available to copy"
		return nil
	}
	width, height := m.sessionDimensions(id)
	cursor := session.Cursor()
	point := terminalhost.ViewportPoint{
		X: min(max(cursor.X, 0), max(width-1, 0)),
		Y: min(max(cursor.Y, 0), max(height-1, 0)),
	}
	if m.sessionScrollY[id] > 0 {
		point = terminalhost.ViewportPoint{X: 0, Y: max(height-1, 0)}
	}
	m.copy = terminalCopyState{
		Active:        true,
		SessionID:     id,
		Anchor:        point,
		Focus:         point,
		ReturnCapture: m.capture,
	}
	m.capture = false
	_ = session.Blur()
	m.status = "Copy mode · drag or use arrows to select · Enter/Y copies · Esc cancels"
	return nil
}

func (m *Model) leaveCopyMode(restoreCapture bool) {
	state := m.copy
	m.copy = terminalCopyState{}
	if !restoreCapture || !state.ReturnCapture {
		return
	}
	if session, id := m.visibleSession(); session != nil && id == state.SessionID {
		m.capture = true
		_ = session.Focus()
	}
}

func (m *Model) copySelection() tea.Cmd {
	state := m.copy
	if !state.Active || !state.HasSelection {
		m.status = "Select terminal text before copying"
		return nil
	}
	session := m.sessions[state.SessionID]
	if session == nil {
		m.leaveCopyMode(false)
		m.status = "The copied terminal session is no longer available"
		return nil
	}
	_, selected, _, _ := session.RenderViewportSelection(m.sessionScrollY[state.SessionID], &terminalhost.ViewportSelection{
		Start: state.Anchor,
		End:   state.Focus,
	})
	if selected == "" {
		m.status = "The selected cells contain no text"
		return nil
	}
	m.leaveCopyMode(true)
	m.status = fmt.Sprintf("Copied %d bytes from terminal selection", len(selected))
	return tea.SetClipboard(selected)
}

func (m *Model) handleCopyKey(key string) tea.Cmd {
	if !m.copy.Active {
		return nil
	}
	switch key {
	case "esc", "q":
		m.leaveCopyMode(true)
		m.status = "Copy mode cancelled"
		return nil
	case "enter", "y", "ctrl+shift+c":
		return m.copySelection()
	case "v", "space":
		m.copy.Anchor = m.copy.Focus
		m.copy.HasSelection = true
		m.status = "Selection anchor moved"
		return nil
	case "pgup":
		m.scrollCopyViewport(max(m.mainContentHeight()-2, 1))
		return nil
	case "pgdown":
		m.scrollCopyViewport(-max(m.mainContentHeight()-2, 1))
		return nil
	}

	width, height := m.sessionDimensions(m.copy.SessionID)
	point := m.copy.Focus
	switch key {
	case "left", "shift+left":
		point.X--
	case "right", "shift+right":
		point.X++
	case "ctrl+left", "ctrl+shift+left":
		point.X -= 5
	case "ctrl+right", "ctrl+shift+right":
		point.X += 5
	case "up", "shift+up", "k":
		point.Y--
	case "down", "shift+down", "j":
		point.Y++
	case "home":
		point.X = 0
	case "end":
		point.X = width - 1
	default:
		return nil
	}
	point.X = min(max(point.X, 0), max(width-1, 0))
	point.Y = min(max(point.Y, 0), max(height-1, 0))
	m.copy.Focus = point
	m.copy.HasSelection = true
	return nil
}

func (m *Model) scrollCopyViewport(delta int) {
	if !m.copy.Active {
		return
	}
	session := m.sessions[m.copy.SessionID]
	if session == nil {
		return
	}
	limit := session.ScrollbackLen()
	m.sessionScrollY[m.copy.SessionID] = min(max(m.sessionScrollY[m.copy.SessionID]+delta, 0), limit)
	m.sessionScrollback[m.copy.SessionID] = limit
	m.copy.HasSelection = false
	m.copy.Dragging = false
	m.status = fmt.Sprintf("Copy viewport %d/%d", m.sessionScrollY[m.copy.SessionID], limit)
}

func (m *Model) handleCopyMouse(message tea.MouseMsg) tea.Cmd {
	if !m.copy.Active {
		return nil
	}
	if msg, ok := message.(tea.MouseWheelMsg); ok {
		delta := verticalScrollStep
		if msg.Button == tea.MouseWheelUp {
			delta = -verticalScrollStep
		}
		m.scrollCopyViewport(-delta)
		return nil
	}
	point, ok := m.copyViewportPoint(message.Mouse())
	if !ok {
		if _, released := message.(tea.MouseReleaseMsg); released {
			m.copy.Dragging = false
		}
		return nil
	}
	switch msg := message.(type) {
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		m.copy.Anchor = point
		m.copy.Focus = point
		m.copy.HasSelection = true
		m.copy.Dragging = true
	case tea.MouseMotionMsg:
		if m.copy.Dragging {
			m.copy.Focus = point
			m.copy.HasSelection = true
		}
	case tea.MouseReleaseMsg:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		if m.copy.Dragging {
			m.copy.Focus = point
			m.copy.HasSelection = true
			m.copy.Dragging = false
		}
	}
	return nil
}

func (m *Model) copyViewportPoint(mouse tea.Mouse) (terminalhost.ViewportPoint, bool) {
	mainX := 0
	if m.sidebarDrawn() {
		mainX = sidebarWidth
	}
	paneX, paneY := 0, 0
	width, height := m.embeddedDimensions()
	if m.page == pageTerminal {
		pane, ok := m.terminalWorkspace.paneRect(m.copy.SessionID, m.mainPaneWidth(), m.mainContentHeight())
		if !ok {
			return terminalhost.ViewportPoint{}, false
		}
		paneX, paneY = pane.X, pane.Y
		width, height = pane.Width, pane.Height-embeddedHeaderHeight
	}
	point := terminalhost.ViewportPoint{
		X: mouse.X - mainX - paneX,
		Y: mouse.Y - m.topHeight() - paneY - embeddedHeaderHeight,
	}
	if point.X < 0 || point.X >= width || point.Y < 0 || point.Y >= height {
		return terminalhost.ViewportPoint{}, false
	}
	return point, true
}
