package terminal

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// sendKey preserves the text decoded by the outer terminal. The VT package's
// key encoder currently drops printable keys carrying Shift or lock modifiers,
// even though their Text field already contains the intended character.
func sendKey(emulator *vt.SafeEmulator, event uv.KeyEvent) {
	if _, pressed := event.(uv.KeyPressEvent); pressed {
		if text := event.Key().Text; text != "" {
			emulator.SendText(text)
			return
		}
	}
	emulator.SendKey(event)
}
