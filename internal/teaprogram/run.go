// Package teaprogram applies Lisan's terminal compatibility policy to every
// Bubble Tea program.
package teaprogram

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var unsupportedSequences = [][]byte{
	[]byte(ansi.SetTabEvery8Columns),
}

// Run starts a Bubble Tea program while filtering only output known to damage
// layout. Keyboard capability negotiation is left intact so the launch
// terminal can provide the richest input events it supports.
func Run(model tea.Model, options ...tea.ProgramOption) (tea.Model, error) {
	output := newTerminalOutput(os.Stdout)
	options = append(options, tea.WithOutput(output))
	result, err := tea.NewProgram(model, options...).Run()
	return result, errors.Join(err, output.filter.err)
}

// terminalOutput embeds the real terminal file so Bubble Tea can still detect
// its file descriptor, enter terminal mode, and read the initial dimensions.
// Write is the only operation intercepted.
type terminalOutput struct {
	*os.File
	filter sequenceFilter
}

func newTerminalOutput(file *os.File) *terminalOutput {
	return &terminalOutput{File: file, filter: sequenceFilter{target: file}}
}

func (output *terminalOutput) Write(input []byte) (int, error) {
	return output.filter.Write(input)
}

type sequenceFilter struct {
	target io.Writer
	mu     sync.Mutex
	err    error
}

func (writer *sequenceFilter) Write(input []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	filtered := append([]byte(nil), input...)
	for _, sequence := range unsupportedSequences {
		filtered = bytes.ReplaceAll(filtered, sequence, nil)
	}
	if len(filtered) == 0 {
		return len(input), nil
	}
	written, err := writer.target.Write(filtered)
	if err == nil && written != len(filtered) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = errors.Join(writer.err, err)
		return 0, err
	}
	return len(input), nil
}
