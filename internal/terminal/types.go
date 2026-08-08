package terminal

import (
	"image/color"

	"github.com/charmbracelet/x/vt"
)

type EventKind uint8

const (
	FrameEvent EventKind = iota
	ExitEvent
)

type Event struct {
	Kind EventKind
	Err  error
}

type Spec struct {
	ID         string
	Name       string
	Path       string
	Args       []string
	Dir        string
	Env        []string
	Foreground color.Color
	Background color.Color
}

type Cursor struct {
	X       int
	Y       int
	Visible bool
	Style   vt.CursorStyle
	Blink   bool
	Color   color.Color
}
