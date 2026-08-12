//go:build !windows

// Package terminal hosts interactive child programs inside Lisan's screen.
// A PTY gives the child normal terminal semantics while the VT emulator turns
// its ANSI output into a renderable grid for the parent Bubble Tea program.
package terminal

import (
	"errors"
	"image/color"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Session is one persistent child terminal. SafeEmulator protects rendering
// from the PTY reader goroutine while Bubble Tea is drawing the same frame.
type Session struct {
	id         string
	name       string
	command    *exec.Cmd
	pty        *os.File
	emulator   *vt.SafeEmulator
	inputQueue *inputController
	input      io.Closer
	background color.Color

	// screenMu keeps PTY resize notifications and emulator output in the same
	// order. Without it, a full-screen child can draw for the new PTY size
	// while the emulator still has its previous dimensions.
	screenMu sync.Mutex
	width    int
	height   int

	frames      chan struct{}
	clipboard   chan string
	exit        chan error
	outputDone  chan struct{}
	processDone chan struct{}
	inputDone   chan struct{}

	mu            sync.RWMutex
	exited        bool
	exitErr       error
	paused        bool
	cursorVisible bool
	cursorStyle   vt.CursorStyle
	cursorBlink   bool
	cursorColor   color.Color
	title         string
	closeOnce     sync.Once
	resourcesOnce sync.Once
}

func Start(spec Spec, width, height int) (*Session, error) {
	if strings.TrimSpace(spec.ID) == "" {
		return nil, errors.New("terminal session requires an id")
	}
	if strings.TrimSpace(spec.Path) == "" {
		return nil, errors.New("terminal session requires an executable")
	}
	ptyWidth, ptyHeight := terminalDimension(width), terminalDimension(height)
	width, height = int(ptyWidth), int(ptyHeight)

	emulator := vt.NewSafeEmulator(width, height)
	if spec.Foreground != nil {
		emulator.SetDefaultForegroundColor(spec.Foreground)
		emulator.SetForegroundColor(spec.Foreground)
	}
	if spec.Background != nil {
		emulator.SetDefaultBackgroundColor(spec.Background)
		emulator.SetBackgroundColor(spec.Background)
	}

	command := exec.Command(spec.Path, spec.Args...)
	command.Dir = spec.Dir
	if spec.Env == nil {
		command.Env = os.Environ()
	} else {
		command.Env = spec.Env
	}

	session := &Session{
		id:            spec.ID,
		name:          spec.Name,
		command:       command,
		emulator:      emulator,
		background:    spec.Background,
		frames:        make(chan struct{}, 1),
		clipboard:     make(chan string, 4),
		exit:          make(chan error, 1),
		outputDone:    make(chan struct{}),
		processDone:   make(chan struct{}),
		inputDone:     make(chan struct{}),
		width:         width,
		height:        height,
		cursorVisible: true,
		cursorStyle:   vt.CursorBlock,
		cursorBlink:   true,
	}
	session.inputQueue = newInputController(emulator, spec.Input)
	if input, ok := emulator.InputPipe().(io.Closer); ok {
		session.input = input
	}
	emulator.SetCallbacks(vt.Callbacks{
		Title: func(title string) {
			session.mu.Lock()
			session.title = cleanTitle(title)
			session.mu.Unlock()
			session.signalFrame()
		},
		CursorPosition: func(_, _ uv.Position) {
			session.signalFrame()
		},
		CursorVisibility: func(visible bool) {
			session.mu.Lock()
			session.cursorVisible = visible
			session.mu.Unlock()
			session.signalFrame()
		},
		CursorStyle: func(style vt.CursorStyle, blink bool) {
			session.mu.Lock()
			session.cursorStyle = style
			session.cursorBlink = blink
			session.mu.Unlock()
			session.signalFrame()
		},
		CursorColor: func(value color.Color) {
			session.mu.Lock()
			session.cursorColor = value
			session.mu.Unlock()
			session.signalFrame()
		},
		EnableMode: func(mode ansi.Mode) {
			session.inputQueue.setMode(mode, true)
		},
		DisableMode: func(mode ansi.Mode) {
			session.inputQueue.setMode(mode, false)
		},
	})
	registerClipboardHandler(emulator, func(value string) {
		select {
		case session.clipboard <- value:
		default:
		}
	})

	ptmx, err := pty.StartWithSize(command, &pty.Winsize{Rows: ptyHeight, Cols: ptyWidth})
	if err != nil {
		session.inputQueue.close()
		_ = emulator.Close()
		<-session.inputQueue.done
		return nil, err
	}
	session.pty = ptmx

	go session.readOutput()
	go session.writeInput()
	go session.wait()
	return session, nil
}

func (s *Session) ID() string   { return s.id }
func (s *Session) Name() string { return s.name }

func (s *Session) Title() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.title
}

func (s *Session) BackgroundColor() color.Color {
	return s.background
}

func (s *Session) Cursor() Cursor {
	position := s.emulator.CursorPosition()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Cursor{
		X:       position.X,
		Y:       position.Y,
		Visible: s.cursorVisible,
		Style:   s.cursorStyle,
		Blink:   s.cursorBlink,
		Color:   s.cursorColor,
	}
}

func (s *Session) Resize(width, height int) error {
	ptyWidth, ptyHeight := terminalDimension(width), terminalDimension(height)
	width, height = int(ptyWidth), int(ptyHeight)

	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	if width == s.width && height == s.height {
		return nil
	}

	// Hold screenMu while changing the PTY and emulator. The reader may collect
	// an immediate SIGWINCH repaint, but cannot apply it until the virtual screen
	// has the matching dimensions. Failed OS resizes leave the emulator intact.
	if err := pty.Setsize(s.pty, &pty.Winsize{Rows: ptyHeight, Cols: ptyWidth}); err != nil {
		return err
	}
	resizePrimaryScreen(s.emulator, width, height)
	s.width, s.height = width, height
	s.signalFrame()
	return nil
}

func (s *Session) SendKey(key uv.KeyEvent) error {
	return s.inputQueue.sendKey(key)
}

func (s *Session) SendText(value string) error {
	return s.inputQueue.sendText(value)
}

// Paste queues one host paste operation. The session waits for a Mentat's
// bracketed-paste negotiation when configured, then transports the operation
// in bounded chunks without allowing another input event between its markers.
func (s *Session) Paste(value string) error {
	return s.inputQueue.paste(value)
}

func (s *Session) SendMouse(event uv.MouseEvent) error {
	return s.inputQueue.sendMouse(event)
}

func (s *Session) Focus() error { return s.inputQueue.focus() }
func (s *Session) Blur() error  { return s.inputQueue.blur() }

// Pause freezes the complete PTY process group while preserving its process,
// emulator, and screen state. Hidden panes therefore consume no CPU on Unix.
func (s *Session) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited || s.paused || s.command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-s.command.Process.Pid, syscall.SIGSTOP); err != nil {
		return err
	}
	s.paused = true
	return nil
}

func (s *Session) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited || !s.paused || s.command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-s.command.Process.Pid, syscall.SIGCONT); err != nil {
		return err
	}
	s.paused = false
	return nil
}

func (s *Session) NextEvent() Event {
	select {
	case <-s.frames:
		return Event{Kind: FrameEvent}
	case value := <-s.clipboard:
		return Event{Kind: ClipboardEvent, Text: value}
	case err := <-s.exit:
		return Event{Kind: ExitEvent, Err: err}
	}
}

func (s *Session) Exited() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exited, s.exitErr
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.inputQueue.close()
		s.mu.RLock()
		exited := s.exited
		paused := s.paused
		s.mu.RUnlock()
		if !exited && s.command.Process != nil {
			if paused {
				_ = syscall.Kill(-s.command.Process.Pid, syscall.SIGCONT)
			}
			// StartWithSize creates a new session. Signalling its process group
			// prevents child jobs from surviving after the cockpit closes.
			if err := syscall.Kill(-s.command.Process.Pid, syscall.SIGHUP); err != nil {
				_ = s.command.Process.Signal(syscall.SIGHUP)
			}
		}
		s.closeIO()
		if !exited {
			select {
			case <-s.processDone:
			case <-time.After(500 * time.Millisecond):
				if s.command.Process != nil {
					if err := syscall.Kill(-s.command.Process.Pid, syscall.SIGKILL); err != nil {
						_ = s.command.Process.Kill()
					}
				}
				select {
				case <-s.processDone:
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
		inputStopped := false
		select {
		case <-s.inputDone:
			inputStopped = true
		case <-time.After(500 * time.Millisecond):
		}
		controllerStopped := false
		select {
		case <-s.inputQueue.done:
			controllerStopped = true
		case <-time.After(500 * time.Millisecond):
		}
		if inputStopped && controllerStopped {
			_ = s.emulator.Close()
		}
		// Both input users must be gone before closing the emulator. If either
		// fails to unblock, retaining it is safer than racing its input pipe.
	})
}

func (s *Session) closeIO() {
	s.resourcesOnce.Do(func() {
		if s.input != nil {
			_ = s.input.Close()
		}
		_ = s.pty.Close()
	})
}

func (s *Session) readOutput() {
	defer close(s.outputDone)
	buffer := make([]byte, 32*1024)
	for {
		count, err := s.pty.Read(buffer)
		if count > 0 {
			s.applyOutput(buffer[:count])
			s.signalFrame()
		}
		if err != nil {
			return
		}
	}
}

// applyOutput contains terminal-parser panics to the embedded session. The VT
// dependency currently accepts scroll margins larger than its buffer; stale
// full-screen output around a resize can therefore panic on a later line
// operation. Resetting at the current size repairs the margins while retaining
// the child process and all subsequent output.
func (s *Session) applyOutput(data []byte) {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	defer func() {
		if recover() != nil {
			// RIS also returns the ANSI parser to its ground state; Resize alone
			// repairs the screen but would leave the failed CSI half-consumed.
			_, _ = s.emulator.Write([]byte("\x1bc"))
			s.emulator.Resize(s.width, s.height)
			if s.pty != nil {
				// Ask the full-screen child for a clean repaint after the damaged
				// output chunk was dropped.
				_ = pty.Setsize(s.pty, &pty.Winsize{Rows: terminalDimension(s.height), Cols: terminalDimension(s.width)})
			}
		}
	}()
	_, _ = s.emulator.Write(data)
}

func (s *Session) writeInput() {
	defer close(s.inputDone)
	_, _ = io.Copy(s.pty, s.emulator)
}

func (s *Session) wait() {
	err := s.command.Wait()
	// Wait normally wins its race with the PTY reader. Give the reader time to
	// paint the child's last bytes before the UI receives the exit event.
	select {
	case <-s.outputDone:
	case <-time.After(250 * time.Millisecond):
	}
	s.mu.Lock()
	s.exited = true
	s.exitErr = err
	s.mu.Unlock()
	s.inputQueue.close()
	s.closeIO()
	close(s.processDone)
	s.signalFrame()
	s.exit <- err
}

func (s *Session) signalFrame() {
	select {
	case s.frames <- struct{}{}:
	default:
	}
}
