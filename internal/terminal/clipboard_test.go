package terminal

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

func TestClipboardHandlerAcceptsBoundedWrites(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	values := make(chan string, 1)
	registerClipboardHandler(emulator, func(value string) { values <- value })
	want := "spice 🌶"
	encoded := base64.StdEncoding.EncodeToString([]byte(want))
	_, _ = emulator.Write([]byte("\x1b]52;c;" + encoded + "\x07"))
	select {
	case got := <-values:
		if got != want {
			t.Fatalf("clipboard write = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard write was not delivered")
	}
}

func TestClipboardHandlerBlocksReadsAndOversizedWrites(t *testing.T) {
	for _, data := range []string{
		"52;c;?",
		"52;c;not-base64!",
		"52;c;" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", maxClipboardBytes+1))),
	} {
		if value, ok := decodeClipboardWrite([]byte(data)); ok {
			t.Fatalf("unsafe clipboard data was accepted: %d bytes", len(value))
		}
	}
}
