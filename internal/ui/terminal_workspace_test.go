package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"lisanalgaib/internal/theme"
)

func TestTerminalWorkspaceTabsSplitsAndClose(t *testing.T) {
	workspace := newTerminalWorkspace()
	first := workspace.newTab()
	if first != shellSessionID || workspace.activeSessionID() != first {
		t.Fatalf("first tab = %q, active %q", first, workspace.activeSessionID())
	}
	second, ok := workspace.splitActive(terminalSplitVertical)
	if !ok || second != "shell:2" || workspace.activeSessionID() != second {
		t.Fatalf("vertical split = %q/%v, active %q", second, ok, workspace.activeSessionID())
	}
	third, ok := workspace.splitActive(terminalSplitHorizontal)
	if !ok || third != "shell:3" {
		t.Fatalf("horizontal split = %q/%v", third, ok)
	}

	panes := workspace.paneRects(101, 31)
	if len(panes) != 3 {
		t.Fatalf("split workspace has %d panes: %#v", len(panes), panes)
	}
	for _, pane := range panes {
		if pane.Width <= 0 || pane.Height <= 0 || pane.Y < terminalToolbarHeight {
			t.Fatalf("invalid pane rectangle: %#v", pane)
		}
	}

	fourth := workspace.newTab()
	if fourth != "shell:4" || len(workspace.Tabs) != 2 || workspace.ActiveTab != 1 {
		t.Fatalf("second tab state: id=%q tabs=%d active=%d", fourth, len(workspace.Tabs), workspace.ActiveTab)
	}
	if got := workspace.activateTab(0); got == "" || workspace.ActiveTab != 0 {
		t.Fatalf("could not reactivate split tab: %q", got)
	}
	closed, next := workspace.closeActive()
	if closed == "" || next == "" || workspace.contains(closed) {
		t.Fatalf("close pane = closed %q next %q", closed, next)
	}
}

func TestTerminalToolbarExposesTabsAndActions(t *testing.T) {
	workspace := newTerminalWorkspace()
	workspace.newTab()
	workspace.newTab()
	spans := workspace.toolbarSpans(100)
	seen := map[terminalToolbarKind]int{}
	for _, span := range spans {
		seen[span.Kind]++
		middle := span.Start + (span.End-span.Start)/2
		got, ok := workspace.toolbarAt(middle, 100)
		if !ok || got.Kind != span.Kind || got.Tab != span.Tab {
			t.Fatalf("toolbar hit at %d = %#v/%v, want %#v", middle, got, ok, span)
		}
	}
	if seen[terminalToolbarTab] != 2 || seen[terminalToolbarNew] != 1 ||
		seen[terminalToolbarSplitVertical] != 1 || seen[terminalToolbarSplitHorizontal] != 1 ||
		seen[terminalToolbarClose] != 1 {
		t.Fatalf("toolbar spans missing controls: %#v", spans)
	}
}

func TestTerminalWorkspaceRendersExactViewport(t *testing.T) {
	model := NewModel(t.TempDir())
	model.section, model.page = sectionTerminal, pageTerminal
	model.terminalWorkspace.newTab()
	model.terminalWorkspace.splitActive(terminalSplitVertical)
	model.terminalWorkspace.splitActive(terminalSplitHorizontal)
	const width, height = 100, 24
	content := model.terminalWorkspaceContent(theme.All[model.themeIndex], width, height)
	lines := strings.Split(content, "\n")
	if len(lines) != height {
		t.Fatalf("terminal workspace height = %d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("terminal workspace row %d width = %d, want %d: %q", index, got, width, line)
		}
	}
	for _, label := range []string{"NEW", "VERT", "HOR", "CLOSE", "Terminal 1", "Terminal 2", "Terminal 3"} {
		if !strings.Contains(content, label) {
			t.Fatalf("terminal workspace omitted %q", label)
		}
	}
}

func TestTerminalWorkspaceRendersExactViewportAfterDeepSplitAndShrink(t *testing.T) {
	model := NewModel(t.TempDir())
	model.section, model.page = sectionTerminal, pageTerminal
	model.terminalWorkspace.newTab()
	for _, axis := range []terminalSplitAxis{
		terminalSplitHorizontal,
		terminalSplitVertical,
		terminalSplitHorizontal,
		terminalSplitVertical,
		terminalSplitHorizontal,
	} {
		if _, ok := model.terminalWorkspace.splitActive(axis); !ok {
			t.Fatal("could not build deeply split terminal workspace")
		}
	}

	const width, height = 16, 5
	content := model.terminalWorkspaceContent(theme.All[model.themeIndex], width, height)
	lines := strings.Split(content, "\n")
	if len(lines) != height {
		t.Fatalf("shrunken workspace height = %d, want %d: %q", len(lines), height, content)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("shrunken workspace row %d width = %d, want %d: %q", index, got, width, line)
		}
	}
	for _, pane := range model.terminalWorkspace.paneRects(width, height) {
		if pane.X < 0 || pane.Y < terminalToolbarHeight || pane.X+pane.Width > width || pane.Y+pane.Height > height {
			t.Fatalf("shrunken pane falls outside viewport: %#v", pane)
		}
	}
}

func TestTerminalToolbarButtonsMutateWorkspace(t *testing.T) {
	model := NewModel(t.TempDir())
	model.section, model.page = sectionTerminal, pageTerminal
	model.width, model.height = 100, 30
	model.terminalWorkspace.newTab()

	click := func(kind terminalToolbarKind) tea.Cmd {
		t.Helper()
		for _, span := range model.terminalWorkspace.toolbarSpans(model.mainPaneWidth()) {
			if span.Kind == kind {
				return model.handleClick(tea.MouseClickMsg{X: span.Start, Y: model.topHeight(), Button: tea.MouseLeft})
			}
		}
		t.Fatalf("toolbar control %d not found", kind)
		return nil
	}

	click(terminalToolbarNew)
	if len(model.terminalWorkspace.Tabs) != 2 {
		t.Fatalf("New created %d tabs", len(model.terminalWorkspace.Tabs))
	}
	click(terminalToolbarSplitVertical)
	if panes := model.terminalWorkspace.paneRects(model.mainPaneWidth(), model.mainContentHeight()); len(panes) != 2 {
		t.Fatalf("vertical split created %d panes", len(panes))
	}
	click(terminalToolbarSplitHorizontal)
	if panes := model.terminalWorkspace.paneRects(model.mainPaneWidth(), model.mainContentHeight()); len(panes) != 3 {
		t.Fatalf("horizontal split created %d panes", len(panes))
	}
	click(terminalToolbarClose)
	if panes := model.terminalWorkspace.paneRects(model.mainPaneWidth(), model.mainContentHeight()); len(panes) != 2 {
		t.Fatalf("Close left %d panes", len(panes))
	}
}
