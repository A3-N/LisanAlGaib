package terminal

import "strings"

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
	s.screenMu.Lock()
	defer s.screenMu.Unlock()

	screen = s.emulator.Render()
	if s.emulator.IsAltScreen() {
		return screen, 0, 0
	}

	limit = s.emulator.ScrollbackLen()
	effective = min(max(scrollUp, 0), limit)
	if effective == 0 {
		return screen, 0, limit
	}

	height := max(s.emulator.Height(), 1)
	active := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	for len(active) < height {
		active = append(active, "")
	}
	if len(active) > height {
		active = active[:height]
	}

	start := limit - effective
	rows := make([]string, height)
	scrollback := s.emulator.Scrollback()
	for row := range rows {
		position := start + row
		if position < limit {
			if line := scrollback.Line(position); line != nil {
				rows[row] = line.Render()
			}
			continue
		}
		if index := position - limit; index >= 0 && index < len(active) {
			rows[row] = active[index]
		}
	}
	return strings.Join(rows, "\n"), effective, limit
}
