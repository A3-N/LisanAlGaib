package cliout

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestOutputStatesShareTheDockerBarLanguage(t *testing.T) {
	var output bytes.Buffer
	Start(&output, "Installing dependencies", "git and ripgrep")
	Success(&output, "Installing dependencies")
	Skipped(&output, "Cleaning Docker state", "Docker is unavailable")
	Warning(&output, "Docker", "deprecated syntax")
	Failure(&output, "Lisan", errors.New("broken\ndetail"))
	Result(&output, "Cleaning Docker state", "done")
	got := output.String()
	for _, expected := range []string{
		"Installing dependencies ╾▶───────────────────╼ running",
		"Installing dependencies ╾━━━━━━━━━━━━━━━━━━━━╼ done",
		"Cleaning Docker state ╾────────────────────╼ skipped",
		"Docker ╾━━━━━━━━━━━━━━━━━━━━╼ warning",
		"Lisan ╾━━━━━━━━━━━━━━━━━━━━╼ failed",
		"Cleaning Docker state · done",
		"  error · broken\n  detail",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("formatted output omitted %q:\n%s", expected, got)
		}
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("redirected output contains ANSI color: %q", output.String())
	}
}
