package terminal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

const (
	inputEventCapacity = 1024
	pasteWriteChunk    = 16 << 10
)

var (
	// ErrInputClosed reports input submitted after session shutdown began.
	ErrInputClosed = errors.New("terminal input is closed")
	// ErrInputQueueFull reports bounded backpressure without blocking the UI.
	ErrInputQueueFull = errors.New("terminal input queue is full")
)

type inputEventKind uint8

const (
	inputKey inputEventKind = iota
	inputText
	inputPaste
	inputMouse
	inputFocus
	inputBlur
)

type inputEvent struct {
	kind  inputEventKind
	key   uv.KeyEvent
	mouse uv.MouseEvent
	text  string
	cost  int
}

// inputController is the single writer for a session. It preserves ordering
// across keys, mouse events, focus changes, and whole paste operations while
// keeping PTY backpressure away from Bubble Tea's Update loop.
type inputController struct {
	emulator *vt.SafeEmulator
	policy   InputPolicy
	events   chan inputEvent
	stop     chan struct{}
	done     chan struct{}

	queueMu      sync.Mutex
	closed       bool
	pendingBytes int

	modeMu             sync.Mutex
	bracketedPaste     bool
	bracketedPasteEver bool
	pasteModeResolved  bool
	modeChanged        chan struct{}
	closeOnce          sync.Once
}

func newInputController(emulator *vt.SafeEmulator, policy InputPolicy) *inputController {
	controller := &inputController{
		emulator:    emulator,
		policy:      policy.withDefaults(),
		events:      make(chan inputEvent, inputEventCapacity),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		modeChanged: make(chan struct{}, 1),
	}
	go controller.run()
	return controller
}

func (c *inputController) enqueue(event inputEvent) error {
	if event.cost <= 0 {
		event.cost = 1
	}
	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	if c.closed {
		return ErrInputClosed
	}
	if event.cost > c.policy.MaxPendingBytes || c.pendingBytes > c.policy.MaxPendingBytes-event.cost {
		return fmt.Errorf("%w (%d-byte limit)", ErrInputQueueFull, c.policy.MaxPendingBytes)
	}
	select {
	case c.events <- event:
		c.pendingBytes += event.cost
		return nil
	default:
		return fmt.Errorf("%w (%d-event limit)", ErrInputQueueFull, cap(c.events))
	}
}

func (c *inputController) release(cost int) {
	c.queueMu.Lock()
	c.pendingBytes = max(c.pendingBytes-cost, 0)
	c.queueMu.Unlock()
}

func (c *inputController) run() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			return
		case event := <-c.events:
			select {
			case <-c.stop:
				c.release(event.cost)
				return
			default:
			}
			c.dispatch(event)
			c.release(event.cost)
		}
	}
}

func (c *inputController) dispatch(event inputEvent) {
	switch event.kind {
	case inputKey:
		sendKey(c.emulator, event.key)
	case inputText:
		c.emulator.SendText(event.text)
	case inputPaste:
		c.writePaste(event.text)
	case inputMouse:
		c.emulator.SendMouse(event.mouse)
	case inputFocus:
		c.emulator.Focus()
	case inputBlur:
		c.emulator.Blur()
	}
}

func (c *inputController) writePaste(text string) {
	if c.stopped() {
		return
	}
	bracketed := c.pasteModeAtDelivery()
	if c.stopped() {
		return
	}
	if bracketed {
		c.emulator.SendText(ansi.BracketedPasteStart)
	}
	for len(text) > 0 {
		if c.stopped() {
			return
		}
		size := min(len(text), pasteWriteChunk)
		c.emulator.SendText(text[:size])
		text = text[size:]
	}
	if bracketed {
		c.emulator.SendText(ansi.BracketedPasteEnd)
	}
}

func (c *inputController) pasteModeAtDelivery() bool {
	if !c.policy.WaitForBracketedPaste {
		return c.bracketedPasteMode()
	}
	timer := time.NewTimer(c.policy.PasteReadyTimeout)
	defer timer.Stop()
	for {
		c.modeMu.Lock()
		enabled, resolved := c.bracketedPaste, c.pasteModeResolved
		c.modeMu.Unlock()
		if enabled || resolved {
			return enabled
		}
		select {
		case <-c.modeChanged:
		case <-timer.C:
			c.modeMu.Lock()
			c.pasteModeResolved = true
			c.modeMu.Unlock()
			return false
		case <-c.stop:
			return false
		}
	}
}

func (c *inputController) bracketedPasteMode() bool {
	c.modeMu.Lock()
	defer c.modeMu.Unlock()
	return c.bracketedPaste
}

func (c *inputController) setMode(mode ansi.Mode, enabled bool) {
	if mode != ansi.ModeBracketedPaste {
		return
	}
	c.modeMu.Lock()
	c.bracketedPaste = enabled
	if enabled {
		c.bracketedPasteEver = true
		c.pasteModeResolved = true
	} else if c.bracketedPasteEver {
		c.pasteModeResolved = true
	}
	c.modeMu.Unlock()
	select {
	case c.modeChanged <- struct{}{}:
	default:
	}
}

func (c *inputController) stopped() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

func (c *inputController) close() {
	c.closeOnce.Do(func() {
		c.queueMu.Lock()
		c.closed = true
		c.queueMu.Unlock()
		close(c.stop)
	})
}

func (c *inputController) sendKey(event uv.KeyEvent) error {
	return c.enqueue(inputEvent{kind: inputKey, key: event})
}

func (c *inputController) sendText(text string) error {
	return c.enqueue(inputEvent{kind: inputText, text: text, cost: len(text)})
}

func (c *inputController) paste(text string) error {
	if text == "" {
		return nil
	}
	return c.enqueue(inputEvent{kind: inputPaste, text: text, cost: len(text)})
}

func (c *inputController) sendMouse(event uv.MouseEvent) error {
	return c.enqueue(inputEvent{kind: inputMouse, mouse: event})
}

func (c *inputController) focus() error {
	return c.enqueue(inputEvent{kind: inputFocus})
}

func (c *inputController) blur() error {
	return c.enqueue(inputEvent{kind: inputBlur})
}

// sendKey preserves the text decoded by the launch terminal, fills the
// emulator's legacy modified-key gaps, and leaves all remaining mode-aware
// encoding to the emulator.
func sendKey(emulator *vt.SafeEmulator, event uv.KeyEvent) {
	if _, pressed := event.(uv.KeyPressEvent); pressed {
		key := event.Key()
		if text := key.Text; text != "" {
			emulator.SendText(text)
			return
		}
		if sequence, ok := encodeCompatibleKey(key); ok {
			emulator.SendText(sequence)
			return
		}
	}
	emulator.SendKey(event)
}
