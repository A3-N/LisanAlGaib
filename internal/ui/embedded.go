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
	if msg.Err != nil {
		m.capture = false
		m.status = fmt.Sprintf("Could not embed %s: %v", msg.ID, msg.Err)
		return nil
	}
	if previous := m.sessions[msg.ID]; previous != nil {
		previous.Close()
	}
	m.sessions[msg.ID] = msg.Session
	if msg.ID == shellSessionID {
		m.terminalScrollY = 0
		m.terminalScrollback = msg.Session.ScrollbackLen()
	}
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
		if msg.ID == shellSessionID {
			m.syncTerminalVerticalScroll(msg.Session)
		}
		return waitTerminalCmd(msg.Session)
	}
	if msg.ID == shellSessionID {
		m.terminalScrollY = 0
		m.terminalScrollback = 0
	}
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
	width, height := m.embeddedDimensions()
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
		ID:   shellSessionID,
		Name: name,
		Path: shell,
		Dir:  m.root,
		Env:  terminalhost.Environment(childEnvironment(), "SHELL="+shell),
	})
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
		return shellSessionID
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

func (m *Model) resizeSession(session *terminalhost.Session) {
	width, height := m.embeddedDimensions()
	if err := session.Resize(width, height); err != nil {
		m.status = fmt.Sprintf("Resize %s: %v", session.Name(), err)
	}
}

func (m *Model) resizeVisibleSession() {
	if session, _ := m.visibleSession(); session != nil {
		m.resizeSession(session)
		if session.ID() == shellSessionID {
			m.syncTerminalVerticalScroll(session)
		}
	}
	m.clampExtensionScroll()
}

func (m *Model) syncTerminalVerticalScroll(session *terminalhost.Session) {
	if session == nil || session.ID() != shellSessionID {
		return
	}
	limit := session.ScrollbackLen()
	if m.terminalScrollY > 0 && limit > m.terminalScrollback {
		m.terminalScrollY += limit - m.terminalScrollback
	}
	m.terminalScrollY = min(max(m.terminalScrollY, 0), limit)
	m.terminalScrollback = limit
}

// scrollTerminalVertically uses a bottom-relative offset: positive values move
// into history and negative values move back toward the live prompt.
func (m *Model) scrollTerminalVertically(delta int) bool {
	if m.page != pageTerminal {
		return false
	}
	session := m.sessions[shellSessionID]
	if session == nil {
		return false
	}
	limit := session.ScrollbackLen()
	previous := m.terminalScrollY
	m.terminalScrollY = min(max(m.terminalScrollY+delta, 0), limit)
	m.terminalScrollback = limit
	if limit == 0 && previous == 0 {
		// Let an alternate-screen program receive the wheel itself.
		return false
	}
	m.status = fmt.Sprintf("Terminal history %d/%d · Home/End jumps", m.terminalScrollY, limit)
	return true
}

func (m *Model) moveTerminalVertically(toBottom bool) bool {
	if m.page != pageTerminal {
		return false
	}
	limit := 0
	if session := m.sessions[shellSessionID]; session != nil {
		limit = session.ScrollbackLen()
	}
	if toBottom {
		m.terminalScrollY = 0
		m.status = "Terminal returned to live output"
	} else {
		m.terminalScrollY = limit
		m.status = "Terminal history top"
	}
	m.terminalScrollback = limit
	return true
}

func (m *Model) forwardMouse(message tea.MouseMsg) tea.Cmd {
	session, _ := m.visibleSession()
	if session == nil {
		return nil
	}
	mouse := message.Mouse()
	mainX := 0
	if m.sidebarDrawn() {
		mainX = sidebarWidth
	}
	local := uv.Mouse{
		X:      mouse.X - mainX,
		Y:      mouse.Y - m.topHeight() - embeddedHeaderHeight,
		Button: mouse.Button,
		Mod:    mouse.Mod,
	}
	width, height := m.embeddedDimensions()
	if local.X < 0 || local.X >= width || local.Y < 0 || local.Y >= height {
		return nil
	}
	if session.ID() == shellSessionID {
		if m.terminalScrollY > 0 {
			return nil
		}
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
	session, _ := m.visibleSession()
	if session == nil {
		return "", false
	}
	mode := "WRAPPER"
	if m.capture {
		mode = "INPUT"
	}
	if exited, _ := session.Exited(); exited {
		mode = "EXITED"
	}
	title := session.Name()
	if childTitle := session.Title(); childTitle != "" && childTitle != title {
		title += " · " + childTitle
	}
	screen := session.Render()
	if session.ID() == shellSessionID {
		var effective, limit int
		screen, effective, limit = session.RenderViewport(m.terminalScrollY)
		m.terminalScrollY = effective
		m.terminalScrollback = limit
	}
	header := fmt.Sprintf(" %s  [%s]  Ctrl-G toggles wrapper control", title, mode)
	if session.ID() == shellSessionID {
		if m.terminalScrollback > 0 {
			header += fmt.Sprintf("  y:%d/%d", m.terminalScrollY, m.terminalScrollback)
		}
	}
	headerStyle := lipgloss.NewStyle().Width(width).Height(embeddedHeaderHeight).
		Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Panel)).Bold(true)
	background := session.BackgroundColor()
	if background == nil {
		background = lipgloss.Color(t.Background)
	}
	terminalScreen := renderTerminalScreen(screen, background, width, height-embeddedHeaderHeight)
	content := lipgloss.JoinVertical(lipgloss.Left, headerStyle.Render(trimRunes(header, width)), terminalScreen)
	return content, true
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
	if id == shellSessionID {
		y += m.terminalScrollY
		width, height := m.embeddedDimensions()
		if x < 0 || x >= width {
			return nil
		}
		if y < 0 || y >= height {
			return nil
		}
	}
	if m.sidebarDrawn() {
		x += sidebarWidth
	}
	y += m.topHeight() + embeddedHeaderHeight
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
