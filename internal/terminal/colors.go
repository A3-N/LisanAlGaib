package terminal

import "image/color"

func (s *Session) BackgroundColor() color.Color {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	return s.background
}

// SetBackgroundColor keeps wrapper-painted blank cells aligned with a child
// application whose theme changed after the terminal session was started.
func (s *Session) SetBackgroundColor(background color.Color) {
	s.screenMu.Lock()
	s.background = background
	s.emulator.SetDefaultBackgroundColor(background)
	s.emulator.SetBackgroundColor(background)
	s.screenMu.Unlock()
	s.signalFrame()
}
