package terminal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestResizePrimaryScreenReflowsWithoutLosingText(t *testing.T) {
	emulator := vt.NewSafeEmulator(6, 3)
	session := &Session{emulator: emulator, width: 6, height: 3}
	_, _ = emulator.Write([]byte("abcdefghijklmn"))

	resizePrimaryScreen(emulator, 4, 4)
	if got := strings.ReplaceAll(session.Render(), "\n", ""); !strings.Contains(got, "abcdefghijklmn") {
		t.Fatalf("narrow reflow lost content: %q", session.Render())
	}
	resizePrimaryScreen(emulator, 8, 3)
	if got := strings.ReplaceAll(session.Render(), "\n", ""); !strings.Contains(got, "abcdefghijklmn") {
		t.Fatalf("wide reflow lost content: %q", session.Render())
	}
}

func TestResizePrimaryScreenMovesOverflowIntoScrollback(t *testing.T) {
	emulator := vt.NewSafeEmulator(8, 4)
	session := &Session{emulator: emulator, width: 8, height: 4}
	_, _ = emulator.Write([]byte("one\r\ntwo\r\nthree\r\nfour"))

	resizePrimaryScreen(emulator, 8, 2)
	if emulator.ScrollbackLen() == 0 {
		t.Fatal("height shrink discarded rows instead of preserving history")
	}
	all := session.Render()
	for index := 0; index < emulator.ScrollbackLen(); index++ {
		if line := emulator.Scrollback().Line(index); line != nil {
			all = line.String() + "\n" + all
		}
	}
	for _, want := range []string{"one", "two", "three", "four"} {
		if !strings.Contains(all, want) {
			t.Fatalf("height reflow lost %q: %q", want, all)
		}
	}
}

func TestResizeAlternateScreenRemainsApplicationOwned(t *testing.T) {
	emulator := vt.NewSafeEmulator(6, 3)
	_, _ = emulator.Write([]byte("\x1b[?1049habcdef"))
	resizePrimaryScreen(emulator, 3, 2)
	if emulator.Width() != 3 || emulator.Height() != 2 || !emulator.IsAltScreen() {
		t.Fatalf("alternate resize = %dx%d alt=%v", emulator.Width(), emulator.Height(), emulator.IsAltScreen())
	}
}
