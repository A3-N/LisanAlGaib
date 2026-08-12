//go:build !windows

package terminal

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func TestEnvironmentOverridesWithoutDuplicates(t *testing.T) {
	base := EnvironmentWithout([]string{"OUTER_WINDOW_ID=3", "TERM=old", "A=1", "TERM=older"}, "OUTER_WINDOW_ID")
	environment := Environment(base, "TERM=xterm-256color", "B=2")
	if strings.Join(environment, ",") != "A=1,TERM=xterm-256color,B=2" {
		t.Fatalf("unexpected environment: %#v", environment)
	}
}

func TestEnvironmentWithoutPrefixIsCaseInsensitive(t *testing.T) {
	got := EnvironmentWithoutPrefix([]string{"EXAMPLETERM_WINDOW=1", "exampleterm_PID=2", "PATH=/bin"}, "ExampleTerm_")
	if strings.Join(got, ",") != "PATH=/bin" {
		t.Fatalf("prefix-filtered environment = %q", got)
	}
}

func TestCleanTitleRemovesTerminalControls(t *testing.T) {
	if got := cleanTitle("  safe\n\x1b[31m  "); got != "safe[31m" {
		t.Fatalf("unexpected title: %q", got)
	}
}

func TestTerminalDimensionIsBounded(t *testing.T) {
	for input, want := range map[int]uint16{-1: 2, 2: 2, 80: 80, maxTerminalDimension + 1: maxTerminalDimension} {
		if got := terminalDimension(input); got != want {
			t.Fatalf("terminalDimension(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestSessionRendersAndAcceptsInput(t *testing.T) {
	session, err := Start(Spec{
		ID:   "test",
		Name: "test shell",
		Path: "/bin/sh",
		Args: []string{"-c", "printf 'ready\\n'; IFS= read -r value; printf 'got:%s\\n' \"$value\""},
		Dir:  t.TempDir(),
		Env:  Environment(os.Environ(), "TERM=xterm-256color"),
	}, 40, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	deadline := time.After(3 * time.Second)
	for !strings.Contains(session.Render(), "ready") {
		select {
		case <-deadline:
			t.Fatalf("terminal never rendered command output: %q", session.Render())
		default:
			_ = session.NextEvent()
		}
	}

	session.SendText("sandworm\r")
	for {
		select {
		case <-deadline:
			t.Fatalf("terminal never rendered input response: %q", session.Render())
		default:
			event := session.NextEvent()
			if event.Kind == ExitEvent {
				if event.Err != nil {
					t.Fatal(event.Err)
				}
				if !strings.Contains(session.Render(), "got:sandworm") {
					t.Fatalf("missing response: %q", session.Render())
				}
				return
			}
		}
	}
}

func TestSessionKeyEncoding(t *testing.T) {
	session, err := Start(Spec{
		ID:   "key-test",
		Name: "key test",
		Path: "/bin/sh",
		Args: []string{"-c", "IFS= read -r value; printf '%s' \"$value\""},
		Dir:  t.TempDir(),
	}, 20, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	session.SendKey(uv.KeyPressEvent(uv.Key{
		Code:        'a',
		ShiftedCode: 'A',
		Text:        "A",
		Mod:         uv.ModShift,
	}))
	session.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("terminal did not accept shifted input: %q", session.Render())
		default:
			event := session.NextEvent()
			if event.Kind != ExitEvent {
				continue
			}
			if event.Err != nil {
				t.Fatal(event.Err)
			}
			if !strings.Contains(session.Render(), "A") {
				t.Fatalf("shifted input was not preserved: %q", session.Render())
			}
			return
		}
	}
}

func TestSessionPastePreservesBracketedPaste(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{})
	session := &Session{emulator: emulator, inputQueue: controller}
	controller.setMode(ansi.ModeBracketedPaste, true)
	defer closeTestInputController(emulator, controller)
	content := "water of life\nspice"
	want := ansi.BracketedPasteStart + content + ansi.BracketedPasteEnd

	type readResult struct {
		value string
		err   error
	}
	result := make(chan readResult, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, err := io.ReadFull(emulator, buffer)
		result <- readResult{value: string(buffer), err: err}
	}()
	if err := session.Paste(content); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.value != want {
			t.Fatalf("paste input = %q, want %q", got.value, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paste input was not emitted")
	}
}

func TestSessionInputEmitsModifiedEditingKeys(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{})
	session := &Session{emulator: emulator, inputQueue: controller}
	defer closeTestInputController(emulator, controller)
	want := "\x1b[1;5D\x1b[1;5C\x17"
	result := make(chan string, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, _ = io.ReadFull(emulator, buffer)
		result <- string(buffer)
	}()
	for _, key := range []uv.Key{
		{Code: uv.KeyLeft, Mod: uv.ModCtrl},
		{Code: uv.KeyRight, Mod: uv.ModCtrl},
		{Code: uv.KeyBackspace, Mod: uv.ModCtrl},
	} {
		if err := session.SendKey(uv.KeyPressEvent(key)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case got := <-result:
		if got != want {
			t.Fatalf("modified key stream = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("modified keys were not emitted")
	}
}

func TestMentatPasteWaitsForBracketedPasteNegotiation(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{
		WaitForBracketedPaste: true,
		PasteReadyTimeout:     time.Second,
	})
	session := &Session{emulator: emulator, inputQueue: controller}
	defer closeTestInputController(emulator, controller)

	content := "first line\nsecond line"
	want := ansi.BracketedPasteStart + content + ansi.BracketedPasteEnd
	// TUIs commonly reset optional modes before enabling their final set. An
	// initial explicit disable must not be mistaken for completed negotiation.
	controller.setMode(ansi.ModeBracketedPaste, false)
	result := make(chan string, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, _ = io.ReadFull(emulator, buffer)
		result <- string(buffer)
	}()
	if err := session.Paste(content); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		t.Fatalf("paste escaped before mode negotiation: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	controller.setMode(ansi.ModeBracketedPaste, true)
	select {
	case got := <-result:
		if got != want {
			t.Fatalf("negotiated paste = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("paste was not released after bracketed-paste negotiation")
	}
}

func TestMentatPasteFallsBackWhenBracketedPasteIsUnsupported(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{
		WaitForBracketedPaste: true,
		PasteReadyTimeout:     20 * time.Millisecond,
	})
	session := &Session{emulator: emulator, inputQueue: controller}
	defer closeTestInputController(emulator, controller)

	content := "plain fallback"
	result := make(chan string, 1)
	go func() {
		buffer := make([]byte, len(content))
		_, _ = io.ReadFull(emulator, buffer)
		result <- string(buffer)
	}()
	if err := session.Paste(content); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got != content {
			t.Fatalf("fallback paste = %q, want %q", got, content)
		}
	case <-time.After(time.Second):
		t.Fatal("unsupported bracketed-paste negotiation swallowed input")
	}
}

func TestChunkedPasteRemainsAtomicWithAdjacentInput(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{})
	session := &Session{emulator: emulator, inputQueue: controller}
	controller.setMode(ansi.ModeBracketedPaste, true)
	defer closeTestInputController(emulator, controller)

	content := strings.Repeat("spice-😀-", 8_000)
	want := "before" + ansi.BracketedPasteStart + content + ansi.BracketedPasteEnd + "after"
	type orderedResult struct {
		value string
		err   error
	}
	result := make(chan orderedResult, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, err := io.ReadFull(emulator, buffer)
		result <- orderedResult{value: string(buffer), err: err}
	}()
	if err := session.SendText("before"); err != nil {
		t.Fatal(err)
	}
	if err := session.Paste(content); err != nil {
		t.Fatal(err)
	}
	if err := session.SendText("after"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.value != want {
			t.Fatal("chunked paste interleaved with adjacent input")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("chunked paste was not delivered")
	}
}

func TestPasteBackpressureDoesNotBlockCaller(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	controller := newInputController(emulator, InputPolicy{MaxPendingBytes: 32})
	session := &Session{emulator: emulator, inputQueue: controller}
	defer closeTestInputController(emulator, controller)

	started := time.Now()
	if err := session.Paste(strings.Repeat("a", 24)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("paste enqueue blocked for %s", elapsed)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := session.Paste(strings.Repeat("b", 16))
		if errors.Is(err, ErrInputQueueFull) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected queue error: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded input queue never applied backpressure")
		}
		time.Sleep(time.Millisecond)
	}
}

func closeTestInputController(emulator *vt.SafeEmulator, controller *inputController) {
	controller.close()
	if input, ok := emulator.InputPipe().(io.Closer); ok {
		_ = input.Close()
	}
	select {
	case <-controller.done:
	case <-time.After(time.Second):
	}
	_ = emulator.Close()
}

func TestSessionRecoversFromOutOfBoundsScrollRegion(t *testing.T) {
	emulator := vt.NewSafeEmulator(20, 4)
	session := &Session{emulator: emulator, width: 20, height: 4}

	// A full-screen child can emit this sequence for its new height while an
	// older virtual screen is still visible. The upstream parser accepts the
	// oversized margin and panics when delete-line indexes it.
	session.applyOutput([]byte("\x1b[1;20r\x1b[4;1H\x1b[5M"))
	session.applyOutput([]byte("recovered"))

	if !strings.Contains(session.Render(), "recovered") {
		t.Fatalf("terminal did not continue after parser recovery: %q", session.Render())
	}
}

func TestSessionViewportUsesPrimaryScreenScrollback(t *testing.T) {
	emulator := vt.NewSafeEmulator(12, 3)
	session := &Session{emulator: emulator, width: 12, height: 3}
	session.applyOutput([]byte("one\r\ntwo\r\nthree\r\nfour"))

	if limit := session.ScrollbackLen(); limit == 0 {
		t.Fatal("terminal did not retain primary-screen history")
	}
	live, offset, limit := session.RenderViewport(0)
	if offset != 0 || !strings.Contains(live, "four") {
		t.Fatalf("live viewport = offset %d/%d, screen %q", offset, limit, live)
	}
	history, offset, limit := session.RenderViewport(10_000)
	if offset != limit || !strings.Contains(history, "one") || strings.Contains(history, "four") {
		t.Fatalf("history viewport = offset %d/%d, screen %q", offset, limit, history)
	}

	session.applyOutput([]byte("\x1b[?1049hALT"))
	alternate, offset, limit := session.RenderViewport(10_000)
	if offset != 0 || limit != 0 || !strings.Contains(alternate, "ALT") {
		t.Fatalf("alternate screen exposed wrapper history: offset %d/%d, screen %q", offset, limit, alternate)
	}
}

func TestSessionUsesItsWidthForNaturalWrapping(t *testing.T) {
	emulator := vt.NewSafeEmulator(4, 4)
	session := &Session{emulator: emulator, width: 4, height: 4}
	session.applyOutput([]byte("0123456789"))

	lines := strings.Split(session.Render(), "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	if lines[0] != "0123" || lines[1] != "4567" || lines[2] != "89" {
		t.Fatalf("terminal did not wrap at its real width: %#v", lines)
	}
}

func TestViewportSelectionCopiesSoftWrapsWithoutInventingNewlines(t *testing.T) {
	emulator := vt.NewSafeEmulator(5, 3)
	session := &Session{emulator: emulator, width: 5, height: 3}
	session.applyOutput([]byte("abcdefghi"))

	rendered, selected, _, _ := session.RenderViewportSelection(0, &ViewportSelection{
		Start: ViewportPoint{X: 0, Y: 0},
		End:   ViewportPoint{X: 3, Y: 1},
	})
	if selected != "abcdefghi" {
		t.Fatalf("soft-wrapped selection = %q", selected)
	}
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("selection was not highlighted: %q", rendered)
	}
	_, selected, _, _ = session.RenderViewportSelection(0, &ViewportSelection{
		Start: ViewportPoint{X: 2, Y: 0},
		End:   ViewportPoint{X: 3, Y: 1},
	})
	if selected != "cdefghi" {
		t.Fatalf("partial soft-wrapped selection = %q", selected)
	}
}

func TestViewportSelectionPreservesHardLineBreaksAndWideCells(t *testing.T) {
	emulator := vt.NewSafeEmulator(8, 3)
	session := &Session{emulator: emulator, width: 8, height: 3}
	session.applyOutput([]byte("a😀\r\ndef"))

	_, selected, _, _ := session.RenderViewportSelection(0, &ViewportSelection{
		Start: ViewportPoint{X: 2, Y: 0}, // continuation cell of the wide glyph
		End:   ViewportPoint{X: 2, Y: 1},
	})
	if selected != "😀\ndef" {
		t.Fatalf("wide multi-line selection = %q", selected)
	}
}

func TestRenderedSnapshotDoesNotEmitBisectedWideGrapheme(t *testing.T) {
	emulator := vt.NewSafeEmulator(4, 2)
	session := &Session{emulator: emulator, width: 4, height: 2}
	session.applyOutput([]byte("ab😀"))
	emulator.Resize(3, 2)

	for index, line := range strings.Split(session.Render(), "\n") {
		if width := ansi.StringWidth(line); width > 3 {
			t.Fatalf("rendered row %d exceeded its three-cell viewport: width=%d row=%q", index, width, line)
		}
	}
}

func TestSessionResizeUpdatesChildPTYGeometry(t *testing.T) {
	session, err := Start(Spec{
		ID: "resize-test", Name: "resize test", Path: "/bin/sh",
		Args: []string{"-c", `trap 'printf "\r\nsize:%s\r\n" "$(stty size)"' WINCH; printf ready; while :; do sleep 1; done`},
		Dir:  t.TempDir(),
	}, 40, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	waitForRenderedText(t, session, "ready", 3*time.Second)
	if err := session.Resize(13, 7); err != nil {
		t.Fatal(err)
	}
	waitForRenderedText(t, session, "size:7 13", 3*time.Second)
	for index, line := range strings.Split(session.Render(), "\n") {
		if width := ansi.StringWidth(line); width > 13 {
			t.Fatalf("resized child row %d exceeded PTY width: width=%d row=%q", index, width, line)
		}
	}
}

func waitForRenderedText(t *testing.T, session *Session, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(session.Render(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal never rendered %q: %q", want, session.Render())
}

func TestSessionPausePreservesStateWithoutBackgroundWork(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	session, err := Start(Spec{
		ID: "pause-test", Name: "pause test", Path: "/bin/sh",
		Args: []string{"-c", "while :; do printf x >> \"$1\"; sleep 0.05; done", "lisan-pause", counter},
		Dir:  t.TempDir(),
	}, 20, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	waitForSize := func(minimum int64) int64 {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if info, statErr := os.Stat(counter); statErr == nil && info.Size() >= minimum {
				return info.Size()
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("counter did not reach %d bytes", minimum)
		return 0
	}

	before := waitForSize(3)
	if err := session.Pause(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	paused, err := os.Stat(counter)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Size() > before+1 {
		t.Fatalf("hidden process kept working while paused: before=%d after=%d", before, paused.Size())
	}
	if err := session.Resume(); err != nil {
		t.Fatal(err)
	}
	waitForSize(paused.Size() + 2)
}

func TestSessionCloseEscalatesForChildIgnoringHangup(t *testing.T) {
	session, err := Start(Spec{
		ID: "close-test", Name: "close test", Path: "/bin/sh",
		Args: []string{"-c", "trap '' HUP; printf ready; sleep 30"}, Dir: t.TempDir(),
	}, 20, 4)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(session.Render(), "ready") && time.Now().Before(deadline) {
		_ = session.NextEvent()
	}
	if !strings.Contains(session.Render(), "ready") {
		t.Fatal("test child did not install its signal handler")
	}
	started := time.Now()
	session.Close()
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("session cleanup took %s", elapsed)
	}
	if exited, _ := session.Exited(); !exited {
		t.Fatal("session child survived cleanup escalation")
	}
}
