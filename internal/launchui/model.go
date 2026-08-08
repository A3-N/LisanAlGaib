package launchui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"lisanalgaib/internal/teaprogram"
	"lisanalgaib/internal/theme"
)

type Choice string

const (
	Docker Choice = "docker"
	VM     Choice = "vm"
	Config Choice = "config"
	Cancel Choice = ""
)

type Model struct {
	cursor int
	choice Choice
	width  int
	height int
}

var choices = []struct {
	choice      Choice
	label       string
	description string
}{
	{Docker, "docker", "SIETCH TABR // isolated fremen@tabr workspace (recommended)"},
	{VM, "vm", "WORMSIGN // current host user; unsandboxed host access"},
	{Config, "config", "GOLDEN PATH // presets, saved profiles, and features"},
}

func Run() (Choice, error) {
	result, err := teaprogram.Run(&Model{width: 72, height: 18})
	if err != nil {
		return Cancel, err
	}
	model, ok := result.(*Model)
	if !ok {
		return Cancel, fmt.Errorf("unexpected launcher UI result %T", result)
	}
	return model.choice, nil
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(msg.Width, 1), max(msg.Height, 1)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit
		case "j", "down":
			m.cursor = (m.cursor + 1) % len(choices)
		case "k", "up":
			m.cursor = (m.cursor - 1 + len(choices)) % len(choices)
		case "enter", " ":
			m.choice = choices[m.cursor].choice
			return m, tea.Quit
		case "d":
			m.choice = Docker
			return m, tea.Quit
		case "v":
			m.choice = VM
			return m, tea.Quit
		case "c":
			m.choice = Config
			return m, tea.Quit
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			index := msg.Y - 6
			if index >= 0 && index < len(choices) {
				m.cursor, m.choice = index, choices[index].choice
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *Model) View() tea.View {
	t := theme.All[0]
	if m.width < 32 || m.height < 12 {
		content := lipgloss.NewStyle().Width(m.width).Height(m.height).
			Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Background)).
			Render(ansi.Truncate(" Resize terminal for launcher", m.width, "…"))
		view := tea.NewView(content)
		view.AltScreen = true
		view.WindowTitle = "LisanAlGaib Launcher"
		view.BackgroundColor = lipgloss.Color(t.Background)
		view.ForegroundColor = lipgloss.Color(t.Text)
		return view
	}
	fit := func(value string) string { return ansi.Truncate(value, m.width, "…") }
	base := lipgloss.NewStyle().Width(m.width).Foreground(lipgloss.Color(t.Text)).Background(lipgloss.Color(t.Background))
	lines := []string{
		lipgloss.NewStyle().Width(m.width).Bold(true).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Panel)).Render(fit("  LISANALGAIB  //  LAUNCH")),
		base.Render(""),
		base.Render(fit("  Choose one launch mode. The latest selected config profile is used.")),
		base.Foreground(lipgloss.Color(t.Muted)).Render(fit("  ↑/↓ or mouse · Enter · D Docker · V vm · C config · Esc cancel")),
		base.Render(""), base.Render(""),
	}
	for index, choice := range choices {
		line := "    " + choice.label + "    " + choice.description
		style := base
		if index == m.cursor {
			style = style.Bold(true).Foreground(lipgloss.Color(t.Primary)).Background(lipgloss.Color(t.Selection))
		}
		lines = append(lines, style.Render(fit(line)))
	}
	for len(lines) < m.height {
		lines = append(lines, base.Render(""))
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "LisanAlGaib Launcher"
	view.BackgroundColor = lipgloss.Color(t.Background)
	view.ForegroundColor = lipgloss.Color(t.Text)
	return view
}
