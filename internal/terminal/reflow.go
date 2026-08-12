package terminal

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

type reflowLogicalLine struct {
	cells        []uv.Cell
	columns      int
	cursorColumn int
	hasCursor    bool
}

type reflowPhysicalLine struct {
	cells       uv.Line
	softWrapped bool
}

// resizePrimaryScreen compensates for the upstream cell buffer's destructive
// slice-on-shrink behavior. It reconstructs logical lines from the primary
// screen and scrollback, reflows them at the new width, then restores styled
// cells, history, and the cursor. Alternate-screen applications are excluded:
// they own their layout and repaint in response to SIGWINCH.
func resizePrimaryScreen(emulator *vt.SafeEmulator, width, height int) {
	width, height = max(width, 1), max(height, 1)
	oldWidth, oldHeight := max(emulator.Width(), 1), max(emulator.Height(), 1)
	if oldWidth == width && oldHeight == height {
		return
	}
	if emulator.IsAltScreen() {
		emulator.Resize(width, height)
		return
	}

	physical, cursorLine, cursorColumn := snapshotPrimaryLines(emulator, oldWidth, oldHeight)
	logical := joinPrimaryLines(physical, cursorLine, cursorColumn, oldWidth)
	rows, cursorRow, cursorX := wrapPrimaryLines(logical, width)

	emulator.Resize(width, height)
	emulator.ClearScrollback()
	visibleStart := max(len(rows)-height, 0)
	historyStart := max(visibleStart-emulator.Scrollback().MaxLines(), 0)
	for index := historyStart; index < visibleStart; index++ {
		emulator.Scrollback().Push(rows[index])
	}

	blank := uv.EmptyCell
	for y := range height {
		for x := range width {
			emulator.SetCell(x, y, &blank)
		}
	}
	for y, row := range rows[visibleStart:] {
		if y >= height {
			break
		}
		for x := range min(len(row), width) {
			cell := row[x]
			emulator.SetCell(x, y, &cell)
		}
	}

	cursorRow = min(max(cursorRow-visibleStart, 0), height-1)
	cursorX = min(max(cursorX, 0), width-1)
	_, _ = emulator.Write([]byte(ansi.CursorPosition(cursorX+1, cursorRow+1)))
}

func snapshotPrimaryLines(emulator *vt.SafeEmulator, width, height int) ([]reflowPhysicalLine, int, int) {
	history := emulator.Scrollback()
	physical := make([]reflowPhysicalLine, 0, history.Len()+height)
	for y := 0; y < history.Len(); y++ {
		line := cloneViewportLine(history.Line(y), width)
		physical = append(physical, reflowPhysicalLine{cells: line, softWrapped: lineLooksSoftWrapped(line, width)})
	}
	cursor := emulator.CursorPosition()
	lastActive := min(max(cursor.Y, 0), height-1)
	active := make([]uv.Line, height)
	for y := range height {
		active[y] = make(uv.Line, width)
		for x := range width {
			if cell := emulator.CellAt(x, y); cell != nil {
				active[y][x] = *cell
			}
		}
		if lastVisibleCell(active[y]) >= 0 {
			lastActive = y
		}
	}
	cursorLine := len(physical) + min(max(cursor.Y, 0), lastActive)
	for y := 0; y <= lastActive; y++ {
		physical = append(physical, reflowPhysicalLine{
			cells:       active[y],
			softWrapped: lineLooksSoftWrapped(active[y], width),
		})
	}
	return physical, cursorLine, min(max(cursor.X, 0), width-1)
}

func joinPrimaryLines(physical []reflowPhysicalLine, cursorLine, cursorColumn, width int) []reflowLogicalLine {
	logical := make([]reflowLogicalLine, 0, len(physical))
	current := reflowLogicalLine{cursorColumn: -1}
	for index, line := range physical {
		end := width
		if !line.softWrapped {
			end = lastVisibleCell(line.cells) + 1
		}
		if index == cursorLine {
			end = max(end, cursorColumn+1)
			current.cursorColumn = current.columns + cursorColumn
			current.hasCursor = true
		}
		appendReflowCells(&current, line.cells, end)
		if !line.softWrapped {
			logical = append(logical, current)
			current = reflowLogicalLine{cursorColumn: -1}
		}
	}
	if len(current.cells) > 0 || current.hasCursor {
		logical = append(logical, current)
	}
	if len(logical) == 0 {
		logical = append(logical, reflowLogicalLine{hasCursor: true})
	}
	return logical
}

func appendReflowCells(line *reflowLogicalLine, cells uv.Line, end int) {
	end = min(max(end, 0), len(cells))
	for x := 0; x < end; x++ {
		cell := cells[x]
		if cell.Width == 0 {
			continue
		}
		if cell.IsZero() {
			cell = uv.EmptyCell
		}
		if cell.Width < 1 {
			cell.Width = 1
		}
		line.cells = append(line.cells, cell)
		line.columns += cell.Width
	}
}

func wrapPrimaryLines(logical []reflowLogicalLine, width int) ([]uv.Line, int, int) {
	var rows []uv.Line
	cursorRow, cursorX := 0, 0
	for _, logicalLine := range logical {
		lineStart := len(rows)
		row := uv.NewLine(width)
		column := 0
		for _, source := range logicalLine.cells {
			cell := source
			if cell.Width > width {
				cell = uv.EmptyCell
			}
			if column+cell.Width > width {
				rows = append(rows, row)
				row = uv.NewLine(width)
				column = 0
			}
			row.Set(column, &cell)
			column += cell.Width
			if column == width {
				rows = append(rows, row)
				row = uv.NewLine(width)
				column = 0
			}
		}
		if column > 0 || len(logicalLine.cells) == 0 {
			rows = append(rows, row)
		}
		if logicalLine.hasCursor {
			cursorRow = lineStart + logicalLine.cursorColumn/width
			cursorX = logicalLine.cursorColumn % width
			if cursorRow >= len(rows) {
				cursorRow = len(rows) - 1
				cursorX = min(cursorX, width-1)
			}
		}
	}
	return rows, cursorRow, cursorX
}

func lastVisibleCell(line uv.Line) int {
	for x := len(line) - 1; x >= 0; x-- {
		cell := &line[x]
		if !cell.IsZero() && !cell.Equal(&uv.EmptyCell) {
			return x
		}
	}
	return -1
}
