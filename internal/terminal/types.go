package terminal

import (
	"image/color"
	"time"

	"github.com/charmbracelet/x/vt"
)

const (
	defaultPasteReadyTimeout = 1500 * time.Millisecond
	defaultPendingInputBytes = 64 << 20
)

// InputPolicy describes the guarantees required by an interactive child.
// Terminal sessions remain transparent by default; Mentat sessions opt into
// waiting for the child's bracketed-paste negotiation before the first paste.
type InputPolicy struct {
	WaitForBracketedPaste bool
	PasteReadyTimeout     time.Duration
	MaxPendingBytes       int
}

func (p InputPolicy) withDefaults() InputPolicy {
	if p.PasteReadyTimeout <= 0 {
		p.PasteReadyTimeout = defaultPasteReadyTimeout
	}
	if p.MaxPendingBytes <= 0 {
		p.MaxPendingBytes = defaultPendingInputBytes
	}
	return p
}

type EventKind uint8

const (
	FrameEvent EventKind = iota
	ClipboardEvent
	ExitEvent
)

type Event struct {
	Kind EventKind
	Err  error
	Text string
}

type Spec struct {
	ID         string
	Name       string
	Path       string
	Args       []string
	Dir        string
	Env        []string
	Input      InputPolicy
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
