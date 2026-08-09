//go:build windows

// Package terminal hosts child programs inside Lisan's screen. Windows uses
// standard pipes until the native ConPTY backend is available; the outer TUI,
// config UI, inventory, and launcher remain native Windows applications.
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
	"github.com/charmbracelet/x/vt"
)

type Session struct {
	id, name    string
	command     *exec.Cmd
	emulator    *vt.SafeEmulator
	background  color.Color
	screenMu    sync.Mutex
	stdin       io.WriteCloser
	frames      chan struct{}
	exit        chan error
	processDone chan struct{}
	input       io.Closer
	inputDone   chan struct{}
	mu          sync.RWMutex
	exited      bool
	exitErr     error
	title       string
	closeOnce   sync.Once
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
	command := exec.Command(spec.Path, spec.Args...)
	command.Dir, command.Env = spec.Dir, spec.Env
	if command.Env == nil {
		command.Env = os.Environ()
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = command.Stdout
	session := &Session{id: spec.ID, name: spec.Name, command: command, emulator: emulator, background: spec.Background, stdin: stdin, frames: make(chan struct{}, 1), exit: make(chan error, 1), processDone: make(chan struct{}), inputDone: make(chan struct{})}
	if input, ok := emulator.InputPipe().(io.Closer); ok {
		session.input = input
	}
	emulator.SetCallbacks(vt.Callbacks{Title: func(title string) {
		session.mu.Lock()
		session.title = cleanTitle(title)
		session.mu.Unlock()
		session.signalFrame()
	}})
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = emulator.Close()
		return nil, err
	}
	go session.readOutput(stdout)
	go func() {
		defer close(session.inputDone)
		_, _ = io.Copy(stdin, emulator)
	}()
	go func() {
		err := command.Wait()
		time.Sleep(20 * time.Millisecond)
		session.mu.Lock()
		session.exited, session.exitErr = true, err
		session.mu.Unlock()
		close(session.processDone)
		session.signalFrame()
		session.exit <- err
	}()
	return session, nil
}

func (s *Session) ID() string                   { return s.id }
func (s *Session) Name() string                 { return s.name }
func (s *Session) Title() string                { s.mu.RLock(); defer s.mu.RUnlock(); return s.title }
func (s *Session) Render() string               { return s.emulator.Render() }
func (s *Session) BackgroundColor() color.Color { return s.background }
func (s *Session) Cursor() Cursor {
	p := s.emulator.CursorPosition()
	return Cursor{X: p.X, Y: p.Y, Visible: true, Style: vt.CursorBlock, Blink: true}
}
func (s *Session) Resize(width, height int) error {
	s.screenMu.Lock()
	defer s.screenMu.Unlock()
	width, height = int(terminalDimension(width)), int(terminalDimension(height))
	s.emulator.Resize(width, height)
	s.signalFrame()
	return nil
}
func (s *Session) SendKey(key uv.KeyEvent)       { sendKey(s.emulator, key) }
func (s *Session) SendText(value string)         { s.emulator.SendText(value) }
func (s *Session) Paste(value string)            { s.emulator.Paste(value) }
func (s *Session) SendMouse(event uv.MouseEvent) { s.emulator.SendMouse(event) }
func (s *Session) Focus()                        { s.emulator.Focus() }
func (s *Session) Blur()                         { s.emulator.Blur() }

// Windows keeps hidden pane state alive. Safe process suspension will be added
// with the planned ConPTY/job-object backend.
func (s *Session) Pause() error  { return nil }
func (s *Session) Resume() error { return nil }
func (s *Session) NextEvent() Event {
	select {
	case <-s.frames:
		return Event{Kind: FrameEvent}
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
		s.mu.RLock()
		exited := s.exited
		s.mu.RUnlock()
		if !exited && s.command.Process != nil {
			// taskkill /T includes descendants started by shells and agent CLIs.
			if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
				taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
				_ = exec.Command(taskkill, "/PID", fmt.Sprint(s.command.Process.Pid), "/T", "/F").Run()
			}
			_ = s.command.Process.Kill()
		}
		if s.input != nil {
			_ = s.input.Close()
		}
		_ = s.stdin.Close()
		if !exited {
			select {
			case <-s.processDone:
			case <-time.After(time.Second):
			}
		}
		select {
		case <-s.inputDone:
			_ = s.emulator.Close()
		case <-time.After(500 * time.Millisecond):
		}
	})
}

func (s *Session) readOutput(output io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := output.Read(buffer)
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
func (s *Session) signalFrame() {
	select {
	case s.frames <- struct{}{}:
	default:
	}
}
