package terminal

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// ViewportPoint is a zero-based cell position in a rendered terminal viewport.
type ViewportPoint struct {
	X int
	Y int
}

// ViewportSelection is an inclusive, linear cell selection. Start and End may
// be supplied in either order.
type ViewportSelection struct {
	Start ViewportPoint
	End   ViewportPoint
}

type viewportLine struct {
	cells       uv.Line
	softWrapped bool
}

// Render returns an immutable cell snapshot of the live terminal. Rendering
// cells directly keeps the emulator's grapheme widths authoritative instead of
// parsing and measuring its ANSI serialization a second time.
func (s *Session) Render() string {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	return renderActiveScreen(s.emulator)
}

// ScrollbackLen returns the number of primary-screen history rows available to
// the wrapper. Alternate-screen applications own their viewport and therefore
// deliberately expose no wrapper scrollback.
func (s *Session) ScrollbackLen() int {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	if s.emulator.IsAltScreen() {
		return 0
	}
	return s.emulator.ScrollbackLen()
}

// RenderViewport returns a terminal-height snapshot scrolled up from the live
// screen. The effective offset and limit are returned so callers can clamp
// state if history was cleared between frames.
func (s *Session) RenderViewport(scrollUp int) (screen string, effective, limit int) {
	screen, _, effective, limit = s.RenderViewportSelection(scrollUp, nil)
	return screen, effective, limit
}

// RenderViewportSelection renders a viewport and optionally highlights and
// extracts a selection from the same immutable cell snapshot. Keeping both
// operations together prevents copied text from drifting from the displayed
// frame while a child is producing output.
func (s *Session) RenderViewportSelection(scrollUp int, selection *ViewportSelection) (screen, selected string, effective, limit int) {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()

	lines, effective, limit := viewportSnapshot(s.emulator, scrollUp)
	if selection != nil {
		selected = selectViewportText(lines, *selection)
		applyViewportSelection(lines, *selection)
	}
	rows := make([]string, len(lines))
	for index := range lines {
		rows[index] = renderCellLine(lines[index].cells, s.emulator.Width())
	}
	return strings.Join(rows, "\n"), selected, effective, limit
}

func viewportSnapshot(emulator *vt.SafeEmulator, scrollUp int) ([]viewportLine, int, int) {
	width, height := max(emulator.Width(), 1), max(emulator.Height(), 1)
	active := make([]viewportLine, height)
	for y := range height {
		active[y].cells = make(uv.Line, width)
		for x := range width {
			if cell := emulator.CellAt(x, y); cell != nil {
				active[y].cells[x] = *cell
			}
		}
		active[y].softWrapped = lineLooksSoftWrapped(active[y].cells, width)
	}
	if emulator.IsAltScreen() {
		return active, 0, 0
	}

	limit := emulator.ScrollbackLen()
	effective := min(max(scrollUp, 0), limit)
	if effective == 0 {
		return active, 0, limit
	}

	start := limit - effective
	lines := make([]viewportLine, height)
	scrollback := emulator.Scrollback()
	for row := range lines {
		position := start + row
		if position < limit {
			lines[row].cells = cloneViewportLine(scrollback.Line(position), width)
			lines[row].softWrapped = lineLooksSoftWrapped(lines[row].cells, width)
			continue
		}
		index := position - limit
		if index >= 0 && index < len(active) {
			lines[row] = active[index]
		} else {
			lines[row].cells = make(uv.Line, width)
		}
	}
	return lines, effective, limit
}

func cloneViewportLine(source uv.Line, width int) uv.Line {
	line := make(uv.Line, width)
	copy(line, source[:min(len(source), width)])
	return line
}

func lineLooksSoftWrapped(line uv.Line, width int) bool {
	if width <= 0 || len(line) < width {
		return false
	}
	last := &line[width-1]
	return !last.IsZero() && !last.Equal(&uv.EmptyCell)
}

func normalizedViewportSelection(selection ViewportSelection, width, height int) (ViewportPoint, ViewportPoint, bool) {
	if width <= 0 || height <= 0 {
		return ViewportPoint{}, ViewportPoint{}, false
	}
	start, end := selection.Start, selection.End
	start.X, start.Y = min(max(start.X, 0), width-1), min(max(start.Y, 0), height-1)
	end.X, end.Y = min(max(end.X, 0), width-1), min(max(end.Y, 0), height-1)
	if start.Y > end.Y || start.Y == end.Y && start.X > end.X {
		start, end = end, start
	}
	return start, end, true
}

func applyViewportSelection(lines []viewportLine, selection ViewportSelection) {
	if len(lines) == 0 || len(lines[0].cells) == 0 {
		return
	}
	width := len(lines[0].cells)
	start, end, ok := normalizedViewportSelection(selection, width, len(lines))
	if !ok {
		return
	}
	for y := start.Y; y <= end.Y; y++ {
		from, to := 0, width-1
		if y == start.Y {
			from = start.X
		}
		if y == end.Y {
			to = end.X
		}
		from = wideCellStart(lines[y].cells, from)
		to = wideCellEnd(lines[y].cells, to)
		for x := from; x <= to; x++ {
			lines[y].cells[x].Style.Attrs |= uv.AttrReverse
		}
	}
}

func selectViewportText(lines []viewportLine, selection ViewportSelection) string {
	if len(lines) == 0 || len(lines[0].cells) == 0 {
		return ""
	}
	width := len(lines[0].cells)
	start, end, ok := normalizedViewportSelection(selection, width, len(lines))
	if !ok {
		return ""
	}
	var result strings.Builder
	for y := start.Y; y <= end.Y; y++ {
		from, to := 0, width-1
		if y == start.Y {
			from = start.X
		}
		if y == end.Y {
			to = end.X
		}
		from = wideCellStart(lines[y].cells, from)
		var row strings.Builder
		for x := from; x <= to; x++ {
			cell := &lines[y].cells[x]
			if cell.Width == 0 {
				continue
			}
			if cell.Content == "" {
				row.WriteByte(' ')
			} else {
				row.WriteString(cell.Content)
			}
		}
		result.WriteString(strings.TrimRight(row.String(), " "))
		if y < end.Y {
			continuesWrap := lines[y].softWrapped && to == width-1
			if !continuesWrap {
				result.WriteByte('\n')
			}
		}
	}
	return result.String()
}

func wideCellStart(line uv.Line, x int) int {
	for x > 0 && x < len(line) && line[x].Width == 0 {
		x--
	}
	return x
}

func wideCellEnd(line uv.Line, x int) int {
	if len(line) == 0 {
		return 0
	}
	x = min(max(x, 0), len(line)-1)
	start := wideCellStart(line, x)
	return min(start+max(line[start].Width, 1)-1, len(line)-1)
}

func renderActiveScreen(emulator interface {
	Width() int
	Height() int
	CellAt(x, y int) *uv.Cell
}) string {
	width, height := max(emulator.Width(), 1), max(emulator.Height(), 1)
	rows := make([]string, height)
	for y := range height {
		line := make(uv.Line, width)
		for x := range width {
			if cell := emulator.CellAt(x, y); cell != nil {
				line[x] = *cell
			}
		}
		rows[y] = renderCellLine(line, width)
	}
	return strings.Join(rows, "\n")
}

func renderCellLine(source uv.Line, width int) string {
	width = max(width, 1)
	line := make(uv.Line, min(len(source), width))
	copy(line, source[:len(line)])
	for x := range line {
		if line[x].Width > 1 && x+line[x].Width > width {
			// A width shrink can bisect a wide grapheme in the upstream
			// buffer. Paint the remaining cell as a styled blank instead
			// of emitting a glyph that extends past the pane boundary.
			line[x].Empty()
		}
	}
	last := -1
	for x := len(line) - 1; x >= 0; x-- {
		if !line[x].IsZero() && !line[x].Equal(&uv.EmptyCell) {
			last = x
			break
		}
	}
	return line[:last+1].Render()
}
