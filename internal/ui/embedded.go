package ui

import (
	"context"
	"encoding/hex"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"lisanalgaib/internal/appconfig"
	connectorapi "lisanalgaib/internal/connectors"
	"lisanalgaib/internal/files"
	"lisanalgaib/internal/nvimconfig"
	terminalhost "lisanalgaib/internal/terminal"
	"lisanalgaib/internal/theme"
)

const (
	editorSessionID = "editor"
	shellSessionID  = "shell"
)

type terminalStartedMsg struct {
	ID      string
	Session *terminalhost.Session
	Err     error
}

type terminalEventMsg struct {
	ID      string
	Session *terminalhost.Session
	Event   terminalhost.Event
}

func startTerminalCmd(spec terminalhost.Spec, width, height int) tea.Cmd {
	return func() tea.Msg {
		session, err := terminalhost.Start(spec, width, height)
		return terminalStartedMsg{ID: spec.ID, Session: session, Err: err}
	}
}

func waitTerminalCmd(session *terminalhost.Session) tea.Cmd {
	return func() tea.Msg {
		return terminalEventMsg{ID: session.ID(), Session: session, Event: session.NextEvent()}
	}
}

func (m *Model) handleTerminalStarted(msg terminalStartedMsg) tea.Cmd {
	delete(m.starting, msg.ID)
	if terminalSessionID(msg.ID) && !m.terminalWorkspace.contains(msg.ID) {
		if msg.Session != nil {
			msg.Session.Close()
		}
		return nil
	}
	if msg.Err != nil {
		m.capture = false
		m.status = fmt.Sprintf("Could not embed %s: %v", msg.ID, msg.Err)
		return nil
	}
	if previous := m.sessions[msg.ID]; previous != nil {
		previous.Close()
	}
	m.sessions[msg.ID] = msg.Session
	m.sessionScrollY[msg.ID] = 0
	m.sessionScrollback[msg.ID] = msg.Session.ScrollbackLen()
	if m.currentSessionID() == msg.ID {
		m.capture = true
		m.focusSidebar = false
		m.resizeVisibleSession()
		msg.Session.Focus()
		m.status = msg.Session.Name() + " input active; Ctrl-G returns to the wrapper"
	} else {
		msg.Session.Blur()
	}
	return waitTerminalCmd(msg.Session)
}

func (m *Model) handleTerminalEvent(msg terminalEventMsg) tea.Cmd {
	current := m.sessions[msg.ID]
	if current == nil || current != msg.Session {
		return nil
	}
	if msg.Event.Kind == terminalhost.FrameEvent {
		m.syncSessionVerticalScroll(msg.Session)
		return waitTerminalCmd(msg.Session)
	}
	delete(m.sessionScrollY, msg.ID)
	delete(m.sessionScrollback, msg.ID)
	if msg.ID == editorSessionID {
		m.editorPath = ""
		if m.page == pageFile && msg.Event.Err == nil {
			m.capture = false
			m.status = "NvChad closed; returning to its dashboard…"
			return m.ensureEditor("")
		}
	}
	if m.currentSessionID() == msg.ID {
		m.capture = false
		m.focusSidebar = m.sidebar
	}
	if msg.Event.Err != nil {
		m.status = fmt.Sprintf("%s exited: %v; revisit its page to restart", msg.Session.Name(), msg.Event.Err)
	} else {
		m.status = msg.Session.Name() + " closed; revisit its page to restart"
	}
	return nil
}

func (m *Model) beginSession(spec terminalhost.Spec) tea.Cmd {
	if spec.ID == "" || spec.Path == "" {
		return nil
	}
	if session := m.sessions[spec.ID]; session != nil {
		exited, _ := session.Exited()
		if !exited {
			if err := session.Resume(); err != nil {
				m.status = fmt.Sprintf("Resume %s: %v", session.Name(), err)
				return nil
			}
			m.capture = true
			m.focusSidebar = false
			m.resizeSession(session)
			session.Focus()
			m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
			return nil
		}
		session.Close()
		delete(m.sessions, spec.ID)
	}
	if m.starting[spec.ID] {
		return nil
	}

	currentTheme := theme.All[m.themeIndex]
	if spec.Foreground == nil {
		spec.Foreground = lipgloss.Color(currentTheme.Text)
	}
	if spec.Background == nil {
		spec.Background = lipgloss.Color(currentTheme.Background)
	}
	if spec.Env == nil {
		spec.Env = childEnvironment()
	}
	m.starting[spec.ID] = true
	m.status = "Starting embedded " + spec.Name + "…"
	width, height := m.sessionDimensions(spec.ID)
	return startTerminalCmd(spec, width, height)
}

func childEnvironment() []string {
	base := terminalhost.EnvironmentWithout(
		os.Environ(),
		"KITTY_LISTEN_ON",
		"KITTY_PID",
		"KITTY_PUBLIC_KEY",
		"KITTY_WINDOW_ID",
		"NVIM",
		"NVIM_LISTEN_ADDRESS",
		"STY",
		"TMUX",
	)
	return terminalhost.Environment(
		base,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"TERM_PROGRAM=LisanAlGaib",
		"LISAN_EMBEDDED=1",
	)
}

func (m *Model) ensureEditor(path string) tea.Cmd {
	if !m.profile.Feature("files") {
		m.capture = false
		m.status = "Files + NvChad is disabled in the active config profile"
		return nil
	}
	previousPath := m.editorPath
	resolved := ""
	if path != "" {
		var safe bool
		resolved, safe = m.resolveEditorPath(path)
		if !safe {
			m.capture = false
			m.status = "Refused editor path outside the workspace boundary"
			return nil
		}
		m.editorPath = resolved
	}
	m.page = pageFile

	if session := m.sessions[editorSessionID]; session != nil {
		exited, _ := session.Exited()
		if !exited {
			if err := session.Resume(); err != nil {
				m.capture = false
				m.status = fmt.Sprintf("Resume %s: %v", session.Name(), err)
				return nil
			}
			if resolved != "" && resolved != previousPath {
				session.SendText(vimEditCommand(resolved))
			}
			m.capture = true
			m.focusSidebar = false
			m.resizeSession(session)
			session.Focus()
			m.status = "NvChad input active; Ctrl-G returns to the wrapper"
			return nil
		}
	}

	nvim, err := exec.LookPath("nvim")
	if err != nil {
		m.capture = false
		m.status = "Neovim is not installed"
		return nil
	}
	name := "Neovim"
	if nvimconfig.NvChadInstalled() {
		name = "NvChad"
	}
	var args []string
	if resolved != "" {
		args = []string{"--", resolved}
	}
	spec := terminalhost.Spec{
		ID:   editorSessionID,
		Name: name,
		Path: nvim,
		Args: args,
		Dir:  m.root,
	}
	if name == "NvChad" {
		spec.Background = lipgloss.Color(nvimconfig.ChocolateBackground)
	}
	return m.beginSession(spec)
}

func (m *Model) resolveEditorPath(path string) (string, bool) {
	if resolved, safe := files.ResolveWithin(m.root, path); safe {
		return resolved, true
	}
	return "", false
}

func vimEditCommand(path string) string {
	encoded := hex.EncodeToString([]byte(path))
	return "\x1b:lua local h='" + encoded + "'; local p=h:gsub('..',function(x) return string.char(tonumber(x,16)) end); vim.cmd('edit '..vim.fn.fnameescape(p))\r"
}

func directory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (m *Model) ensureShell() tea.Cmd {
	id := m.terminalWorkspace.activeSessionID()
	if id == "" {
		id = m.terminalWorkspace.newTab()
	}
	return m.ensureShellSession(id)
}

func (m *Model) ensureShellSession(id string) tea.Cmd {
	if !m.profile.Feature("terminal") {
		m.capture = false
		m.status = "Terminal is disabled in the active config profile"
		return nil
	}
	shell, name := shellForContext(m.profile)
	if shell == "" {
		m.capture = false
		if os.Getenv("LISAN_CONTAINER") == "1" {
			m.status = "Configured Docker shell " + m.profile.Terminal.DockerShell + " is not installed"
		} else {
			m.status = "Could not resolve the default host shell from SHELL or COMSPEC"
		}
		return nil
	}
	return m.beginSession(terminalhost.Spec{
		ID:   id,
		Name: fmt.Sprintf("%s %d", name, terminalSessionNumber(id)),
		Path: shell,
		Dir:  m.root,
		Env:  terminalhost.Environment(childEnvironment(), "SHELL="+shell),
	})
}

func terminalSessionID(id string) bool {
	return id == shellSessionID || strings.HasPrefix(id, shellSessionID+":")
}

func terminalSessionNumber(id string) int {
	if id == shellSessionID {
		return 1
	}
	var number int
	if _, err := fmt.Sscanf(id, shellSessionID+":%d", &number); err == nil && number > 0 {
		return number
	}
	return 1
}

func (m *Model) newTerminalTab() tea.Cmd {
	if previous, _ := m.visibleSession(); previous != nil {
		previous.Blur()
	}
	id := m.terminalWorkspace.newTab()
	m.capture = true
	m.focusSidebar = false
	m.status = fmt.Sprintf("Starting terminal %d…", terminalSessionNumber(id))
	return m.ensureShellSession(id)
}

func (m *Model) splitTerminal(axis terminalSplitAxis) tea.Cmd {
	active := m.terminalWorkspace.activeSessionID()
	if active == "" {
		return m.newTerminalTab()
	}
	pane, ok := m.terminalWorkspace.paneRect(active, m.mainPaneWidth(), m.mainContentHeight())
	if !ok {
		return nil
	}
	if axis == terminalSplitVertical && pane.Width < terminalMinPaneWidth*2+terminalDividerSize {
		m.status = fmt.Sprintf("Terminal pane needs at least %d columns for a vertical split", terminalMinPaneWidth*2+terminalDividerSize)
		return nil
	}
	if axis == terminalSplitHorizontal && pane.Height < terminalMinPaneHeight*2+terminalDividerSize {
		m.status = fmt.Sprintf("Terminal pane needs at least %d rows for a horizontal split", terminalMinPaneHeight*2+terminalDividerSize)
		return nil
	}
	if previous := m.sessions[active]; previous != nil {
		previous.Blur()
	}
	id, ok := m.terminalWorkspace.splitActive(axis)
	if !ok {
		return nil
	}
	m.capture = true
	m.focusSidebar = false
	m.resizeVisibleSession()
	return m.ensureShellSession(id)
}

func (m *Model) closeActiveTerminal() tea.Cmd {
	closed, next := m.terminalWorkspace.closeActive()
	if closed == "" {
		m.status = "No terminal pane to close"
		return nil
	}
	if session := m.sessions[closed]; session != nil {
		session.Close()
		delete(m.sessions, closed)
	}
	delete(m.starting, closed)
	delete(m.sessionScrollY, closed)
	delete(m.sessionScrollback, closed)
	if next == "" {
		m.capture = false
		m.status = "All terminal panes closed · New opens another"
		return nil
	}
	m.capture = true
	m.resizeVisibleSession()
	if session := m.sessions[next]; session != nil {
		_ = session.Resume()
		session.Focus()
		m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		return nil
	}
	return m.ensureShellSession(next)
}

func (m *Model) activateTerminalTab(index int) tea.Cmd {
	previous := m.terminalWorkspace.activeSessionID()
	next := m.terminalWorkspace.activateTab(index)
	if next == "" {
		return nil
	}
	if previous != next {
		if session := m.sessions[previous]; session != nil {
			session.Blur()
		}
	}
	m.capture = true
	m.resizeVisibleSession()
	if session := m.sessions[next]; session != nil {
		_ = session.Resume()
		session.Focus()
		m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		return nil
	}
	return m.ensureShellSession(next)
}

func (m *Model) activateTerminalPane(id string, capture bool) bool {
	previous := m.terminalWorkspace.activeSessionID()
	if !m.terminalWorkspace.activatePane(id) {
		return false
	}
	if previous != id {
		if session := m.sessions[previous]; session != nil {
			session.Blur()
		}
	}
	m.capture = capture
	m.focusSidebar = false
	if session := m.sessions[id]; session != nil {
		_ = session.Resume()
		if capture {
			session.Focus()
			m.status = session.Name() + " input active; Ctrl-G returns to the wrapper"
		} else {
			session.Blur()
		}
	}
	return true
}

func shellForContext(profile appconfig.Profile) (string, string) {
	if os.Getenv("LISAN_CONTAINER") == "1" {
		return firstAvailableShell(profile.Terminal.DockerShell)
	}
	return hostContextShell()
}

func hostContextShell() (string, string) {
	candidates := []string{strings.TrimSpace(os.Getenv("SHELL"))}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, strings.TrimSpace(os.Getenv("COMSPEC")), "pwsh", "powershell", "cmd")
	} else {
		candidates = append(candidates, "sh")
	}
	return firstAvailableShell(candidates...)
}

func firstAvailableShell(candidates ...string) (string, string) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, filepath.Base(candidate)
		}
	}
	return "", ""
}

func (m *Model) ensureAgent(id string) tea.Cmd {
	if !m.profile.Feature("agents") || !m.profile.Agent(id) {
		m.capture = false
		m.status = "This AI agent is disabled in the active config profile"
		return nil
	}
	tool, ok := m.findTool(id)
	if !ok || !tool.Agent {
		m.capture = false
		return nil
	}
	if !tool.Installed {
		m.capture = false
		m.status = tool.Name + " is not installed; its wrapper page shows setup guidance"
		return nil
	}
	environment := childEnvironment()
	if shell, _ := shellForContext(m.profile); shell != "" {
		environment = terminalhost.Environment(environment, "SHELL="+shell)
	}
	return m.beginSession(terminalhost.Spec{
		ID:   agentSessionID(tool.ID),
		Name: tool.Name,
		Path: tool.Path,
		Dir:  agentWorkingDir(m.root, tool.ID),
		Env:  environment,
	})
}

func agentWorkingDir(root, id string) string {
	candidate := filepath.Join(root, "agents", id)
	if directory(candidate) {
		return candidate
	}
	return root
}

func agentSessionID(id string) string {
	return "agent:" + id
}

func (m *Model) firstAgentID() string {
	first := ""
	for _, tool := range m.inventory.Tools {
		if !tool.Agent {
			continue
		}
		if first == "" {
			first = tool.ID
		}
		if tool.Installed {
			return tool.ID
		}
	}
	return first
}

func (m *Model) currentSessionID() string {
	switch m.page {
	case pageFile:
		return editorSessionID
	case pageAgent:
		if m.selectedAgent != "" {
			return agentSessionID(m.selectedAgent)
		}
	case pageTerminal:
		return m.terminalWorkspace.activeSessionID()
	}
	return ""
}

func (m *Model) visibleSession() (*terminalhost.Session, string) {
	id := m.currentSessionID()
	if id == "" {
		return nil, ""
	}
	return m.sessions[id], id
}

// wrapSafeWidth leaves the terminal body's final column untouched. Some host
// terminals defer wrapping after a write in that column until the next printable
// cell, which can carry a pane's background into column zero of the following
// row. This must be host-agnostic: a Linux process inside Docker may still be
// drawn by Windows Terminal. The View background paints the reserved cell.
func (m *Model) wrapSafeWidth() int {
	return max(m.width-1, 1)
}

func (m *Model) mainPaneWidth() int {
	width := m.wrapSafeWidth()
	if m.sidebarDrawn() {
		width -= sidebarWidth
	}
	return max(width, 1)
}

func (m *Model) sidebarDrawn() bool {
	return m.sidebar && m.wrapSafeWidth() >= sidebarWidth+8
}

func (m *Model) embeddedDimensions() (int, int) {
	return m.mainPaneWidth(), max(m.mainContentHeight()-embeddedHeaderHeight, 2)
}

func (m *Model) sessionDimensions(id string) (int, int) {
	if terminalSessionID(id) {
		if pane, ok := m.terminalWorkspace.paneRect(id, m.mainPaneWidth(), m.mainContentHeight()); ok {
			return max(pane.Width, 2), max(pane.Height-embeddedHeaderHeight, 2)
		}
	}
	return m.embeddedDimensions()
}

func (m *Model) resizeSession(session *terminalhost.Session) {
	width, height := m.sessionDimensions(session.ID())
	if err := session.Resize(width, height); err != nil {
		m.status = fmt.Sprintf("Resize %s: %v", session.Name(), err)
	}
}

func (m *Model) resizeVisibleSession() {
	if m.page == pageTerminal {
		for _, pane := range m.terminalWorkspace.paneRects(m.mainPaneWidth(), m.mainContentHeight()) {
			if session := m.sessions[pane.SessionID]; session != nil {
				m.resizeSession(session)
				m.syncSessionVerticalScroll(session)
			}
		}
		m.clampExtensionScroll()
		return
	}
	if session, _ := m.visibleSession(); session != nil {
		m.resizeSession(session)
		m.syncSessionVerticalScroll(session)
	}
	m.clampExtensionScroll()
}

func (m *Model) syncSessionVerticalScroll(session *terminalhost.Session) {
	if session == nil {
		return
	}
	id := session.ID()
	limit := session.ScrollbackLen()
	if m.sessionScrollY[id] > 0 && limit > m.sessionScrollback[id] {
		m.sessionScrollY[id] += limit - m.sessionScrollback[id]
	}
	m.sessionScrollY[id] = min(max(m.sessionScrollY[id], 0), limit)
	m.sessionScrollback[id] = limit
}

// scrollSessionVertically uses a bottom-relative offset: positive values move
// into history and negative values move back toward the live prompt.
func (m *Model) scrollSessionVertically(delta int) bool {
	session, id := m.visibleSession()
	if session == nil {
		return false
	}
	limit := session.ScrollbackLen()
	previous := m.sessionScrollY[id]
	m.sessionScrollY[id] = min(max(m.sessionScrollY[id]+delta, 0), limit)
	m.sessionScrollback[id] = limit
	if limit == 0 && previous == 0 {
		// Let an alternate-screen program receive the wheel itself.
		return false
	}
	m.status = fmt.Sprintf("%s history %d/%d · Home/End jumps", session.Name(), m.sessionScrollY[id], limit)
	return true
}

func (m *Model) moveSessionVertically(toBottom bool) bool {
	session, id := m.visibleSession()
	if session == nil {
		return false
	}
	limit := session.ScrollbackLen()
	if toBottom {
		m.sessionScrollY[id] = 0
		m.status = session.Name() + " returned to live output"
	} else {
		m.sessionScrollY[id] = limit
		m.status = session.Name() + " history top"
	}
	m.sessionScrollback[id] = limit
	return true
}

func (m *Model) forwardMouse(message tea.MouseMsg) tea.Cmd {
	session, id := m.visibleSession()
	if session == nil {
		return nil
	}
	mouse := message.Mouse()
	mainX := 0
	if m.sidebarDrawn() {
		mainX = sidebarWidth
	}
	paneX, paneY := 0, 0
	width, height := m.embeddedDimensions()
	if m.page == pageTerminal {
		pane, ok := m.terminalWorkspace.paneRect(id, m.mainPaneWidth(), m.mainContentHeight())
		if !ok {
			return nil
		}
		paneX, paneY = pane.X, pane.Y
		width, height = pane.Width, pane.Height-embeddedHeaderHeight
		if height <= 0 {
			return nil
		}
	}
	local := uv.Mouse{
		X:      mouse.X - mainX - paneX,
		Y:      mouse.Y - m.topHeight() - paneY - embeddedHeaderHeight,
		Button: mouse.Button,
		Mod:    mouse.Mod,
	}
	if local.X < 0 || local.X >= width || local.Y < 0 || local.Y >= height {
		return nil
	}
	if m.sessionScrollY[session.ID()] > 0 {
		return nil
	}
	switch message.(type) {
	case tea.MouseClickMsg:
		session.SendMouse(uv.MouseClickEvent(local))
	case tea.MouseReleaseMsg:
		session.SendMouse(uv.MouseReleaseEvent(local))
	case tea.MouseWheelMsg:
		session.SendMouse(uv.MouseWheelEvent(local))
	case tea.MouseMotionMsg:
		session.SendMouse(uv.MouseMotionEvent(local))
	}
	return nil
}

func (m *Model) embeddedContent(t theme.Theme, width, height int) (string, bool) {
	if m.page == pageTerminal {
		return m.terminalWorkspaceContent(t, width, height), true
	}
	session, id := m.visibleSession()
	if session == nil {
		return "", false
	}
	return m.embeddedSessionContent(session, id, t, width, height, true), true
}

func (m *Model) embeddedSessionContent(session *terminalhost.Session, id string, t theme.Theme, width, height int, active bool) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	mode := "WRAPPER"
	if !active {
		mode = "IDLE"
	} else if m.capture {
		mode = "INPUT"
	}
	if exited, _ := session.Exited(); exited {
		mode = "EXITED"
	}
	title := session.Name()
	if childTitle := session.Title(); childTitle != "" && childTitle != title {
		title += " · " + childTitle
	}
	screen, effective, limit := session.RenderViewport(m.sessionScrollY[id])
	m.sessionScrollY[id] = effective
	m.sessionScrollback[id] = limit
	header := fmt.Sprintf(" %s  [%s]", title, mode)
	if !terminalSessionID(id) {
		header += "  Ctrl-G toggles wrapper control"
	}
	if m.sessionScrollback[id] > 0 {
		header += fmt.Sprintf("  y:%d/%d", m.sessionScrollY[id], m.sessionScrollback[id])
	}
	headerColor := t.Muted
	if active {
		headerColor = t.Primary
	}
	headerStyle := lipgloss.NewStyle().Width(width).Height(embeddedHeaderHeight).
		Foreground(lipgloss.Color(headerColor)).Background(lipgloss.Color(t.Panel)).Bold(active)
	renderedHeader := headerStyle.Render(trimRunes(header, width))
	if height <= embeddedHeaderHeight {
		return renderedHeader
	}
	background := session.BackgroundColor()
	if background == nil {
		background = lipgloss.Color(t.Background)
	}
	terminalScreen := renderTerminalScreen(screen, background, width, height-embeddedHeaderHeight)
	return lipgloss.JoinVertical(lipgloss.Left, renderedHeader, terminalScreen)
}

func (m *Model) terminalWorkspaceContent(t theme.Theme, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	toolbar := m.renderTerminalToolbar(t, width)
	if height <= terminalToolbarHeight {
		return toolbar
	}
	tab := m.terminalWorkspace.activeTab()
	if tab == nil || tab.Root == nil {
		body := renderTerminalScreen("\n  No terminal panes · click ＋ NEW", lipgloss.Color(t.Background), width, height-terminalToolbarHeight)
		return lipgloss.JoinVertical(lipgloss.Left, toolbar, body)
	}
	body := m.renderTerminalNode(tab.Root, t, width, height-terminalToolbarHeight)
	return lipgloss.JoinVertical(lipgloss.Left, toolbar, body)
}

func (m *Model) renderTerminalToolbar(t theme.Theme, width int) string {
	spans := m.terminalWorkspace.toolbarSpans(width)
	parts := make([]string, 0, len(spans)*2+1)
	x := 0
	background := lipgloss.Color(t.Panel)
	for _, span := range spans {
		if span.Start > x {
			parts = append(parts, lipgloss.NewStyle().Width(span.Start-x).Background(background).Render(""))
		}
		foreground := t.Secondary
		segmentBackground := t.Panel
		bold := true
		if span.Kind == terminalToolbarTab {
			foreground = t.Muted
			bold = false
			if span.Tab == m.terminalWorkspace.ActiveTab {
				foreground = t.Primary
				segmentBackground = t.Selection
				bold = true
			}
		} else if span.Kind == terminalToolbarClose {
			foreground = t.Danger
		}
		parts = append(parts, lipgloss.NewStyle().Width(span.End-span.Start).
			Foreground(lipgloss.Color(foreground)).Background(lipgloss.Color(segmentBackground)).Bold(bold).
			Render(trimRunes(span.Label, span.End-span.Start)))
		x = span.End
	}
	if x < width {
		parts = append(parts, lipgloss.NewStyle().Width(width-x).Background(background).Render(""))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m *Model) renderTerminalNode(node *terminalLayoutNode, t theme.Theme, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if node == nil {
		return renderTerminalScreen("", lipgloss.Color(t.Background), width, height)
	}
	if node.leaf() {
		if session := m.sessions[node.SessionID]; session != nil {
			return m.embeddedSessionContent(session, node.SessionID, t, width, height, node.SessionID == m.terminalWorkspace.activeSessionID())
		}
		mode := "CLOSED"
		message := "Click this pane to restart"
		if m.starting[node.SessionID] {
			mode = "STARTING"
			message = "Starting embedded shell…"
		}
		headerColor := t.Muted
		if node.SessionID == m.terminalWorkspace.activeSessionID() {
			headerColor = t.Primary
		}
		headerText := fmt.Sprintf(" Terminal %d  [%s]", terminalSessionNumber(node.SessionID), mode)
		header := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color(headerColor)).Background(lipgloss.Color(t.Panel)).Bold(true).
			Render(trimRunes(headerText, width))
		if height <= embeddedHeaderHeight {
			return header
		}
		body := renderTerminalScreen("\n  "+message, lipgloss.Color(t.Background), width, height-embeddedHeaderHeight)
		return lipgloss.JoinVertical(lipgloss.Left, header, body)
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Border)).Background(lipgloss.Color(t.Panel))
	if node.Axis == terminalSplitVertical {
		firstWidth, dividerWidth, secondWidth := terminalSplitSizes(width)
		first := m.renderTerminalNode(node.First, t, firstWidth, height)
		second := m.renderTerminalNode(node.Second, t, secondWidth, height)
		if firstWidth == 0 {
			return second
		}
		if secondWidth == 0 {
			return first
		}
		if dividerWidth == 0 {
			return lipgloss.JoinHorizontal(lipgloss.Top, first, second)
		}
		divider := dividerStyle.Width(dividerWidth).Height(height).Render(strings.Repeat("│\n", max(height-1, 0)) + "│")
		return lipgloss.JoinHorizontal(lipgloss.Top, first, divider, second)
	}
	firstHeight, dividerHeight, secondHeight := terminalSplitSizes(height)
	first := m.renderTerminalNode(node.First, t, width, firstHeight)
	second := m.renderTerminalNode(node.Second, t, width, secondHeight)
	if firstHeight == 0 {
		return second
	}
	if secondHeight == 0 {
		return first
	}
	if dividerHeight == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, first, second)
	}
	divider := dividerStyle.Width(width).Render(strings.Repeat("─", max(width, 1)))
	return lipgloss.JoinVertical(lipgloss.Left, first, divider, second)
}

// renderTerminalScreen makes the embedded terminal a complete rectangle. The
// VT renderer omits trailing blank cells and resets SGR attributes at the end
// of styled text. An outer Lip Gloss background is therefore not sufficient:
// a child's reset can expose the wrapper background in the rest of that row.
// Paint every row and its trailing cells explicitly with the child's default
// background so hiding and restoring a full-screen session cannot leak the
// wrapper theme through it.
func renderTerminalScreen(screen string, background color.Color, width, height int) string {
	width = max(width, 1)
	height = max(height, 1)
	backgroundSequence := ansi.Style{}.BackgroundColor(background).String()
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	rows := make([]string, height)
	for index := range rows {
		line := ""
		if index < len(lines) {
			line = ansi.Cut(lines[index], 0, width)
		}
		padding := strings.Repeat(" ", max(width-ansi.StringWidth(line), 0))
		rows[index] = backgroundSequence + line
		if line != "" {
			// The child may have reset SGR at the end of its visible content.
			rows[index] += backgroundSequence
		}
		rows[index] += padding + ansi.ResetStyle
	}
	return strings.Join(rows, "\n")
}

func (m *Model) embeddedCursor() *tea.Cursor {
	session, id := m.visibleSession()
	if session == nil || !m.capture {
		return nil
	}
	if exited, _ := session.Exited(); exited {
		return nil
	}
	cursor := session.Cursor()
	if !cursor.Visible {
		return nil
	}
	x := cursor.X
	y := cursor.Y
	width, height := m.embeddedDimensions()
	paneX, paneY := 0, 0
	if m.page == pageTerminal {
		pane, ok := m.terminalWorkspace.paneRect(id, m.mainPaneWidth(), m.mainContentHeight())
		if !ok {
			return nil
		}
		paneX, paneY = pane.X, pane.Y
		width, height = pane.Width, pane.Height-embeddedHeaderHeight
		if height <= 0 {
			return nil
		}
	}
	if scrollY := m.sessionScrollY[id]; scrollY > 0 {
		y += scrollY
	}
	if x < 0 || x >= width {
		return nil
	}
	if y < 0 || y >= height {
		return nil
	}
	if m.sidebarDrawn() {
		x += sidebarWidth
	}
	x += paneX
	y += m.topHeight() + paneY + embeddedHeaderHeight
	result := tea.NewCursor(x, y)
	result.Blink = cursor.Blink
	result.Color = cursor.Color
	switch cursor.Style {
	case vt.CursorUnderline:
		result.Shape = tea.CursorUnderline
	case vt.CursorBar:
		result.Shape = tea.CursorBar
	default:
		result.Shape = tea.CursorBlock
	}
	return result
}

func (m *Model) sessionStarting() bool {
	id := m.currentSessionID()
	return id != "" && m.starting[id]
}

func (m *Model) Close() {
	for connectorID, session := range m.connectorSessions {
		if session.Status != "open" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = connectorapi.CloseSession(ctx, m.connectorEndpoint(connectorID), session.ID)
		cancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
	for id, session := range m.sessions {
		session.Close()
		delete(m.sessions, id)
	}
}
