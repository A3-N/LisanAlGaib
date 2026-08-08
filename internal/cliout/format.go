// Package cliout defines the stable, non-interactive Lisan output language.
// Long-running Docker work uses the same twenty-cell bar while it is active;
// ordinary commands emit only completed states and compact keyed details.
package cliout

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

const barWidth = 20

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	spiceGold   = "\x1b[38;2;229;168;83m"
	waterGreen  = "\x1b[38;2;126;190;132m"
	warningGold = "\x1b[38;2;224;123;57m"
	dangerRed   = "\x1b[38;2;212;106;94m"
	desertMuted = "\x1b[38;2;159;139;112m"
)

func Success(output io.Writer, label string) {
	line(output, label, "done")
}

func Failure(output io.Writer, label string, err error) {
	line(output, label, "failed")
	if err != nil {
		Detail(output, "error", err.Error())
	}
}

func Skipped(output io.Writer, label, reason string) {
	line(output, label, "skipped")
	Detail(output, "reason", reason)
}

func Warning(output io.Writer, label, message string) {
	line(output, label, "warning")
	Detail(output, "detail", message)
}

func Start(output io.Writer, label, detail string) {
	line(output, label, "running")
	if detail != "" {
		Detail(output, "task", detail)
	}
}

func Detail(output io.Writer, key, value string) {
	if output == nil || strings.TrimSpace(value) == "" {
		return
	}
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", "\n  ")
	fmt.Fprintf(output, "  %s · %s\n", key, value)
}

// Result emits a final state without implying measurable progress. Cleanup
// uses this because its individual removals are already the useful output.
func Result(output io.Writer, label, state string) {
	if output == nil {
		return
	}
	if ColorEnabled(output) {
		fmt.Fprintf(output, "%s%s%s · %s%s%s\n", ansiBold, label, ansiReset, stateColor(state), state, ansiReset)
		return
	}
	fmt.Fprintf(output, "%s · %s\n", label, state)
}

func line(output io.Writer, label, state string) {
	if output == nil {
		return
	}
	rail := stateRail(state)
	if ColorEnabled(output) {
		color := stateColor(state)
		fmt.Fprintf(output, "%s%s%s %s%s%s %s%s%s\n", ansiBold, label, ansiReset, color, rail, ansiReset, color, state, ansiReset)
		return
	}
	fmt.Fprintf(output, "%s %s %s\n", label, rail, state)
}

func stateRail(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return "╾▶" + strings.Repeat("─", barWidth-1) + "╼"
	case "skipped":
		return "╾" + strings.Repeat("─", barWidth) + "╼"
	default:
		return "╾" + strings.Repeat("━", barWidth) + "╼"
	}
}

// ColorEnabled follows the conventional NO_COLOR opt-out and never emits ANSI
// escapes into redirected output.
func ColorEnabled(output io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	file, ok := output.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func stateColor(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "done":
		return waterGreen
	case "failed":
		return dangerRed
	case "warning":
		return warningGold
	case "skipped":
		return desertMuted
	default:
		return spiceGold
	}
}
