package launchui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestKeyboardAndMouseSelectLaunchModes(t *testing.T) {
	model := &Model{width: 72, height: 18}
	model.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Text: "v"}))
	if model.choice != VM {
		t.Fatalf("VM shortcut selected %q", model.choice)
	}

	model.choice = Cancel
	model.Update(tea.MouseClickMsg{X: 4, Y: 6 + 2, Button: tea.MouseLeft})
	if model.choice != Config {
		t.Fatalf("config mouse row selected %q", model.choice)
	}
}

func TestLauncherUsesActualSmallTerminalSize(t *testing.T) {
	model := &Model{width: 72, height: 18}
	model.Update(tea.WindowSizeMsg{Width: 14, Height: 4})
	lines := strings.Split(model.View().Content, "\n")
	if len(lines) != 4 {
		t.Fatalf("launcher has %d rows, want 4", len(lines))
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width != 14 {
			t.Fatalf("launcher row %d has width %d, want 14", index, width)
		}
	}
}
