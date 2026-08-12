package terminal

import (
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
)

// encodeCompatibleKey handles modified editing, navigation, and function keys
// that are representable by the widely-supported PC/xterm escape sequences.
// Printable text and unmodified keys remain owned by the emulator so input
// methods, international layouts, application cursor mode, and keypad mode keep
// their existing behavior.
func encodeCompatibleKey(key uv.Key) (string, bool) {
	mod := compatibleModifier(key.Mod)
	if mod == 1 {
		return "", false
	}

	// There is no universally negotiated legacy representation for
	// Ctrl-Backspace. Readline, fish, zsh, and most interactive line editors use
	// the terminal word-erase character, Ctrl-W. Preserve Alt as an escape
	// prefix so Alt-Ctrl-Backspace remains a distinct operation.
	if key.Code == uv.KeyBackspace && key.Mod.Contains(uv.ModCtrl) {
		sequence := "\x17"
		if key.Mod.Contains(uv.ModAlt) {
			sequence = "\x1b" + sequence
		}
		return sequence, true
	}

	if key.Code == uv.KeyTab && key.Mod == uv.ModShift {
		return "\x1b[Z", true
	}

	if final, ok := map[rune]byte{
		uv.KeyUp:    'A',
		uv.KeyDown:  'B',
		uv.KeyRight: 'C',
		uv.KeyLeft:  'D',
		uv.KeyBegin: 'E',
		uv.KeyEnd:   'F',
		uv.KeyHome:  'H',
	}[key.Code]; ok {
		return fmt.Sprintf("\x1b[1;%d%c", mod, final), true
	}

	if number, ok := map[rune]int{
		uv.KeyInsert: 2,
		uv.KeyDelete: 3,
		uv.KeyPgUp:   5,
		uv.KeyPgDown: 6,
	}[key.Code]; ok {
		return fmt.Sprintf("\x1b[%d;%d~", number, mod), true
	}

	if key.Code >= uv.KeyF1 && key.Code <= uv.KeyF4 {
		return fmt.Sprintf("\x1b[1;%d%c", mod, byte('P'+key.Code-uv.KeyF1)), true
	}
	if number, ok := map[rune]int{
		uv.KeyF5:  15,
		uv.KeyF6:  17,
		uv.KeyF7:  18,
		uv.KeyF8:  19,
		uv.KeyF9:  20,
		uv.KeyF10: 21,
		uv.KeyF11: 23,
		uv.KeyF12: 24,
	}[key.Code]; ok {
		return fmt.Sprintf("\x1b[%d;%d~", number, mod), true
	}

	return "", false
}

// compatibleModifier follows the conventional parameter layout: one plus the
// Shift, Alt, Ctrl, and Meta bit values. Lock states do not affect the emitted
// sequence. Super and Hyper have no interoperable legacy representation and
// are folded into Meta rather than silently discarding the key.
func compatibleModifier(mod uv.KeyMod) int {
	value := 1
	if mod.Contains(uv.ModShift) {
		value += 1
	}
	if mod.Contains(uv.ModAlt) {
		value += 2
	}
	if mod.Contains(uv.ModCtrl) {
		value += 4
	}
	if mod&(uv.ModMeta|uv.ModSuper|uv.ModHyper) != 0 {
		value += 8
	}
	return value
}
