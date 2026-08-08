//go:build !windows

package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

func TestEnvironmentOverridesWithoutDuplicates(t *testing.T) {
	base := EnvironmentWithout([]string{"KITTY_WINDOW_ID=3", "TERM=old", "A=1", "TERM=older"}, "KITTY_WINDOW_ID")
	environment := Environment(base, "TERM=xterm-256color", "B=2")
	if strings.Join(environment, ",") != "A=1,TERM=xterm-256color,B=2" {
		t.Fatalf("unexpected environment: %#v", environment)
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
