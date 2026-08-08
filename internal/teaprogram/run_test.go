package teaprogram

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSequenceFilterRemovesOnlyUnsupportedSequences(t *testing.T) {
	var output bytes.Buffer
	filter := sequenceFilter{target: &output}
	input := "before" + ansi.SetTabEvery8Columns + ansi.SetModifyOtherKeys2 + "middle" + ansi.ResetModifyOtherKeys + "after\x1b[31m"
	written, err := filter.Write([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if written != len(input) {
		t.Fatalf("reported %d bytes written, want %d", written, len(input))
	}
	if got, want := output.String(), "beforemiddleafter\x1b[31m"; got != want {
		t.Fatalf("filtered output = %q, want %q", got, want)
	}
}

func TestSequenceFilterRetainsWriteErrors(t *testing.T) {
	want := errors.New("output failed")
	filter := sequenceFilter{target: errorWriter{err: want}}
	if _, err := filter.Write([]byte("visible")); !errors.Is(err, want) {
		t.Fatalf("Write error = %v, want %v", err, want)
	}
	if !errors.Is(filter.err, want) {
		t.Fatalf("retained error = %v, want %v", filter.err, want)
	}
}

func TestTerminalOutputPreservesTerminalFileInterface(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	output := newTerminalOutput(file)
	var terminalFile interface {
		io.ReadWriteCloser
		Fd() uintptr
	} = output
	if terminalFile.Fd() != file.Fd() {
		t.Fatalf("filtered output descriptor = %d, want %d", terminalFile.Fd(), file.Fd())
	}
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

var _ io.Writer = errorWriter{}
