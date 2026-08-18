//go:build windows

// Package terminal hosts interactive child programs inside Lisan's screen.
// Native Windows sessions use ConPTY so children observe a real terminal and
// receive every pane-size change.
package terminal

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

type Session struct {
	id, name   string
	pty        *windowsPTY
	emulator   *vt.SafeEmulator
	inputQueue *inputController
	input      io.Closer
	background color.Color

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
	cursorVisible bool
	cursorStyle   vt.CursorStyle
	cursorBlink   bool
	cursorColor   color.Color
	title         string
	closeOnce     sync.Once
	resourcesOnce sync.Once
}

func Start(spec Spec, width, height int) (*Session, error) {
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Path) == "" {
		return nil, errors.New("terminal session requires an id and executable")
	}
	width, height = int(terminalDimension(width)), int(terminalDimension(height))
	emulator := vt.NewSafeEmulator(width, height)
	if spec.Foreground != nil {
		emulator.SetDefaultForegroundColor(spec.Foreground)
		emulator.SetForegroundColor(spec.Foreground)
	}
	if spec.Background != nil {
		emulator.SetDefaultBackgroundColor(spec.Background)
		emulator.SetBackgroundColor(spec.Background)
	}
	session := &Session{
		id: spec.ID, name: spec.Name, emulator: emulator, background: spec.Background,
		frames: make(chan struct{}, 1), clipboard: make(chan string, 4), exit: make(chan error, 1),
		outputDone: make(chan struct{}), processDone: make(chan struct{}), inputDone: make(chan struct{}),
		width: width, height: height, cursorVisible: true, cursorStyle: vt.CursorBlock, cursorBlink: true,
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
		CursorPosition: func(_, _ uv.Position) { session.signalFrame() },
		CursorVisibility: func(visible bool) {
			session.mu.Lock()
			session.cursorVisible = visible
			session.mu.Unlock()
			session.signalFrame()
		},
		CursorStyle: func(style vt.CursorStyle, blink bool) {
			session.mu.Lock()
			session.cursorStyle, session.cursorBlink = style, blink
			session.mu.Unlock()
			session.signalFrame()
		},
		CursorColor: func(value color.Color) {
			session.mu.Lock()
			session.cursorColor = value
			session.mu.Unlock()
			session.signalFrame()
		},
		EnableMode:  func(mode ansi.Mode) { session.inputQueue.setMode(mode, true) },
		DisableMode: func(mode ansi.Mode) { session.inputQueue.setMode(mode, false) },
	})
	registerClipboardHandler(emulator, func(value string) {
		select {
		case session.clipboard <- value:
		default:
		}
	})

	ptySession, err := startWindowsPTY(spec, width, height)
	if err != nil {
		session.inputQueue.close()
		_ = emulator.Close()
		<-session.inputQueue.done
		return nil, err
	}
	session.pty = ptySession
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
func (s *Session) Cursor() Cursor {
	position := s.emulator.CursorPosition()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Cursor{X: position.X, Y: position.Y, Visible: s.cursorVisible, Style: s.cursorStyle, Blink: s.cursorBlink, Color: s.cursorColor}
}

func (s *Session) Resize(width, height int) error {
	width, height = int(terminalDimension(width)), int(terminalDimension(height))
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	if width == s.width && height == s.height {
		return nil
	}
	if err := s.pty.Resize(width, height); err != nil {
		return err
	}
	resizePrimaryScreen(s.emulator, width, height)
	s.width, s.height = width, height
	s.signalFrame()
	return nil
}

func (s *Session) SendKey(key uv.KeyEvent) error       { return s.inputQueue.sendKey(key) }
func (s *Session) SendText(value string) error         { return s.inputQueue.sendText(value) }
func (s *Session) Paste(value string) error            { return s.inputQueue.paste(value) }
func (s *Session) SendMouse(event uv.MouseEvent) error { return s.inputQueue.sendMouse(event) }
func (s *Session) Focus() error                        { return s.inputQueue.focus() }
func (s *Session) Blur() error                         { return s.inputQueue.blur() }

// ConPTY keeps hidden pane state alive. Suspending a Windows process tree safely
// requires job-object coordination, so hidden sessions remain resumable no-ops.
func (s *Session) Pause() error  { return nil }
func (s *Session) Resume() error { return nil }

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
		s.mu.RUnlock()
		if !exited && s.pty != nil {
			if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
				taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
				_ = exec.Command(taskkill, "/PID", fmt.Sprint(s.pty.pid), "/T", "/F").Run()
			}
			s.pty.terminate()
		}
		s.closeIO()
		if !exited {
			select {
			case <-s.processDone:
			case <-time.After(time.Second):
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
		if s.pty != nil {
			s.pty.closeProcess()
		}
	})
}

func (s *Session) closeIO() {
	s.resourcesOnce.Do(func() {
		if s.input != nil {
			_ = s.input.Close()
		}
		if s.pty != nil {
			s.pty.closeIO()
		}
	})
}

func (s *Session) readOutput() {
	defer close(s.outputDone)
	buffer := make([]byte, 32*1024)
	for {
		count, err := s.pty.Read(buffer)
		if count > 0 {
			s.screenMu.Lock()
			_, _ = s.emulator.Write(buffer[:count])
			s.screenMu.Unlock()
			s.signalFrame()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) writeInput() {
	defer close(s.inputDone)
	_, _ = io.Copy(s.pty, s.emulator)
}

func (s *Session) wait() {
	code, err := s.pty.Wait()
	if err == nil && code != 0 {
		err = fmt.Errorf("process exited with code %d", code)
	}
	select {
	case <-s.outputDone:
	case <-time.After(250 * time.Millisecond):
	}
	s.mu.Lock()
	s.exited, s.exitErr = true, err
	s.mu.Unlock()
	s.inputQueue.close()
	s.closeIO()
	s.pty.closeProcess()
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
