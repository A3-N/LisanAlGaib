package ui

import "fmt"

const (
	terminalToolbarHeight = 1
	terminalDividerSize   = 1
	terminalMinPaneWidth  = 18
	terminalMinPaneHeight = embeddedHeaderHeight + 2
)

type terminalSplitAxis uint8

const (
	terminalSplitVertical terminalSplitAxis = iota
	terminalSplitHorizontal
)

type terminalLayoutNode struct {
	SessionID string
	Axis      terminalSplitAxis
	First     *terminalLayoutNode
	Second    *terminalLayoutNode
}

func (n *terminalLayoutNode) leaf() bool {
	return n != nil && n.SessionID != ""
}

type terminalTab struct {
	Root   *terminalLayoutNode
	Active string
}

type terminalWorkspace struct {
	Tabs      []terminalTab
	ActiveTab int
	NextID    int
}

func newTerminalWorkspace() terminalWorkspace {
	return terminalWorkspace{ActiveTab: -1, NextID: 1}
}

func (w *terminalWorkspace) nextSessionID() string {
	id := shellSessionID
	if w.NextID > 1 {
		id = fmt.Sprintf("%s:%d", shellSessionID, w.NextID)
	}
	w.NextID++
	return id
}

func (w *terminalWorkspace) newTab() string {
	id := w.nextSessionID()
	w.Tabs = append(w.Tabs, terminalTab{
		Root:   &terminalLayoutNode{SessionID: id},
		Active: id,
	})
	w.ActiveTab = len(w.Tabs) - 1
	return id
}

func (w *terminalWorkspace) activeTab() *terminalTab {
	if w.ActiveTab < 0 || w.ActiveTab >= len(w.Tabs) {
		return nil
	}
	return &w.Tabs[w.ActiveTab]
}

func (w *terminalWorkspace) activeSessionID() string {
	if tab := w.activeTab(); tab != nil {
		return tab.Active
	}
	return ""
}

func (w *terminalWorkspace) activateTab(index int) string {
	if index < 0 || index >= len(w.Tabs) {
		return ""
	}
	w.ActiveTab = index
	return w.Tabs[index].Active
}

func (w *terminalWorkspace) activatePane(id string) bool {
	for index := range w.Tabs {
		if !terminalNodeContains(w.Tabs[index].Root, id) {
			continue
		}
		w.ActiveTab = index
		w.Tabs[index].Active = id
		return true
	}
	return false
}

func (w *terminalWorkspace) contains(id string) bool {
	for index := range w.Tabs {
		if terminalNodeContains(w.Tabs[index].Root, id) {
			return true
		}
	}
	return false
}

func terminalNodeContains(node *terminalLayoutNode, id string) bool {
	if node == nil {
		return false
	}
	if node.leaf() {
		return node.SessionID == id
	}
	return terminalNodeContains(node.First, id) || terminalNodeContains(node.Second, id)
}

func (w *terminalWorkspace) splitActive(axis terminalSplitAxis) (string, bool) {
	tab := w.activeTab()
	if tab == nil || tab.Active == "" {
		return "", false
	}
	id := w.nextSessionID()
	if !splitTerminalNode(&tab.Root, tab.Active, id, axis) {
		return "", false
	}
	tab.Active = id
	return id, true
}

func splitTerminalNode(node **terminalLayoutNode, target, id string, axis terminalSplitAxis) bool {
	if node == nil || *node == nil {
		return false
	}
	current := *node
	if current.leaf() {
		if current.SessionID != target {
			return false
		}
		*node = &terminalLayoutNode{
			Axis:   axis,
			First:  current,
			Second: &terminalLayoutNode{SessionID: id},
		}
		return true
	}
	return splitTerminalNode(&current.First, target, id, axis) ||
		splitTerminalNode(&current.Second, target, id, axis)
}

func (w *terminalWorkspace) closeActive() (closed, next string) {
	tab := w.activeTab()
	if tab == nil || tab.Active == "" {
		return "", ""
	}
	closed = tab.Active
	root, removed := removeTerminalNode(tab.Root, closed)
	if !removed {
		return "", tab.Active
	}
	if root != nil {
		tab.Root = root
		tab.Active = firstTerminalLeaf(root)
		return closed, tab.Active
	}

	w.Tabs = append(w.Tabs[:w.ActiveTab], w.Tabs[w.ActiveTab+1:]...)
	if len(w.Tabs) == 0 {
		w.ActiveTab = -1
		return closed, ""
	}
	if w.ActiveTab >= len(w.Tabs) {
		w.ActiveTab = len(w.Tabs) - 1
	}
	return closed, w.Tabs[w.ActiveTab].Active
}

func removeTerminalNode(node *terminalLayoutNode, target string) (*terminalLayoutNode, bool) {
	if node == nil {
		return nil, false
	}
	if node.leaf() {
		if node.SessionID == target {
			return nil, true
		}
		return node, false
	}
	first, removed := removeTerminalNode(node.First, target)
	if removed {
		if first == nil {
			return node.Second, true
		}
		node.First = first
		return node, true
	}
	second, removed := removeTerminalNode(node.Second, target)
	if removed {
		if second == nil {
			return node.First, true
		}
		node.Second = second
		return node, true
	}
	return node, false
}

func firstTerminalLeaf(node *terminalLayoutNode) string {
	if node == nil {
		return ""
	}
	if node.leaf() {
		return node.SessionID
	}
	if id := firstTerminalLeaf(node.First); id != "" {
		return id
	}
	return firstTerminalLeaf(node.Second)
}

func terminalLeafCount(node *terminalLayoutNode) int {
	if node == nil {
		return 0
	}
	if node.leaf() {
		return 1
	}
	return terminalLeafCount(node.First) + terminalLeafCount(node.Second)
}

type terminalPaneRect struct {
	SessionID     string
	X, Y          int
	Width, Height int
}

func (r terminalPaneRect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func (w *terminalWorkspace) paneRects(width, height int) []terminalPaneRect {
	tab := w.activeTab()
	if tab == nil || tab.Root == nil || width <= 0 || height <= terminalToolbarHeight {
		return nil
	}
	area := terminalPaneRect{X: 0, Y: terminalToolbarHeight, Width: width, Height: height - terminalToolbarHeight}
	var panes []terminalPaneRect
	appendTerminalPaneRects(tab.Root, area, &panes)
	return panes
}

func appendTerminalPaneRects(node *terminalLayoutNode, area terminalPaneRect, panes *[]terminalPaneRect) {
	if node == nil || area.Width <= 0 || area.Height <= 0 {
		return
	}
	if node.leaf() {
		area.SessionID = node.SessionID
		*panes = append(*panes, area)
		return
	}
	if node.Axis == terminalSplitVertical {
		firstWidth, divider, secondWidth := terminalSplitSizes(area.Width)
		appendTerminalPaneRects(node.First, terminalPaneRect{X: area.X, Y: area.Y, Width: firstWidth, Height: area.Height}, panes)
		appendTerminalPaneRects(node.Second, terminalPaneRect{X: area.X + firstWidth + divider, Y: area.Y, Width: secondWidth, Height: area.Height}, panes)
		return
	}
	firstHeight, divider, secondHeight := terminalSplitSizes(area.Height)
	appendTerminalPaneRects(node.First, terminalPaneRect{X: area.X, Y: area.Y, Width: area.Width, Height: firstHeight}, panes)
	appendTerminalPaneRects(node.Second, terminalPaneRect{X: area.X, Y: area.Y + firstHeight + divider, Width: area.Width, Height: secondHeight}, panes)
}

func terminalSplitSizes(total int) (first, divider, second int) {
	if total <= 0 {
		return 0, 0, 0
	}
	if total >= 3 {
		divider = terminalDividerSize
	}
	available := total - divider
	first = available / 2
	if first == 0 {
		first = 1
	}
	second = available - first
	return first, divider, second
}

func (w *terminalWorkspace) paneRect(id string, width, height int) (terminalPaneRect, bool) {
	for _, pane := range w.paneRects(width, height) {
		if pane.SessionID == id {
			return pane, true
		}
	}
	return terminalPaneRect{}, false
}

type terminalToolbarKind uint8

const (
	terminalToolbarTab terminalToolbarKind = iota
	terminalToolbarNew
	terminalToolbarSplitVertical
	terminalToolbarSplitHorizontal
	terminalToolbarClose
)

type terminalToolbarSpan struct {
	Kind       terminalToolbarKind
	Tab        int
	Label      string
	Start, End int
}

func (w *terminalWorkspace) toolbarSpans(width int) []terminalToolbarSpan {
	if width <= 0 {
		return nil
	}
	actions := []terminalToolbarSpan{
		{Kind: terminalToolbarNew, Label: " ＋ NEW "},
		{Kind: terminalToolbarSplitVertical, Label: " ↔ VERT "},
		{Kind: terminalToolbarSplitHorizontal, Label: " ↕ HOR "},
		{Kind: terminalToolbarClose, Label: " × CLOSE "},
	}
	total := 0
	for _, action := range actions {
		total += visibleWidth(action.Label)
	}
	if total > width {
		actions = []terminalToolbarSpan{
			{Kind: terminalToolbarNew, Label: " ＋ "},
			{Kind: terminalToolbarSplitVertical, Label: " ↔ "},
			{Kind: terminalToolbarSplitHorizontal, Label: " ↕ "},
			{Kind: terminalToolbarClose, Label: " × "},
		}
		total = 0
		for _, action := range actions {
			total += visibleWidth(action.Label)
		}
	}
	actionStart := max(width-total, 0)
	spans := make([]terminalToolbarSpan, 0, len(w.Tabs)+len(actions))
	x := 0
	for index, tab := range w.Tabs {
		label := fmt.Sprintf(" %d ", index+1)
		if panes := terminalLeafCount(tab.Root); panes > 1 {
			label = fmt.Sprintf(" %d:%d ", index+1, panes)
		}
		end := x + visibleWidth(label)
		if end > actionStart {
			break
		}
		spans = append(spans, terminalToolbarSpan{Kind: terminalToolbarTab, Tab: index, Label: label, Start: x, End: end})
		x = end
	}
	x = actionStart
	for _, action := range actions {
		end := min(x+visibleWidth(action.Label), width)
		if end <= x {
			continue
		}
		action.Start, action.End = x, end
		spans = append(spans, action)
		x = end
	}
	return spans
}

func (w *terminalWorkspace) toolbarAt(x, width int) (terminalToolbarSpan, bool) {
	for _, span := range w.toolbarSpans(width) {
		if x >= span.Start && x < span.End {
			return span, true
		}
	}
	return terminalToolbarSpan{}, false
}
