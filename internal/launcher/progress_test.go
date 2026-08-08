package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDockerFailureSummaryKeepsFailedDockerfileInstruction(t *testing.T) {
	output := `#14 ERROR: process "/bin/sh -c apt-get install missing" did not complete successfully: exit code: 100
------
Dockerfile:149
--------------------
 148 |     RUN set -eu; \
 149 | >>>     apt-get install -y missing; \
 150 | >>>     rm -rf /var/lib/apt/lists/*
--------------------
ERROR: failed to solve: process "/bin/sh -c apt-get install missing" did not complete successfully
`
	summary := dockerFailureSummary(output)
	for _, expected := range []string{"#14 ERROR", "Dockerfile:149", "149 | >>>", "ERROR: failed to solve"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("failure summary omitted %q:\n%s", expected, summary)
		}
	}
}

func TestDockerFailureSummaryFallsBackToConciseTail(t *testing.T) {
	var output strings.Builder
	for index := range 20 {
		output.WriteString("routine line\n")
		if index == 19 {
			output.WriteString("daemon connection failed\n")
		}
	}
	summary := dockerFailureSummary(output.String())
	if !strings.Contains(summary, "daemon connection failed") || strings.Count(summary, "\n") > 9 {
		t.Fatalf("fallback was not concise:\n%s", summary)
	}
}

func TestNonTerminalProgressPrintsOnlyStableLifecycleLines(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", false, 120)
	display.start()
	display.consumeLine(`{"vertexes":[{"digest":"sha256:a","name":"[heighliner 1/2] RUN go build","started":"2026-08-08T00:00:00Z"}]}`)
	display.finish(true)
	got := output.String()
	want := "Building Sietch Tabr ╾▶───────────────────╼ running\n" +
		"  task · waiting for Docker\n" +
		"Building Sietch Tabr ╾━━━━━━━━━━━━━━━━━━━━╼ done\n"
	if got != want {
		t.Fatalf("non-terminal progress = %q", got)
	}
	if strings.Contains(got, "Compiling Lisan") || strings.Contains(got, "\x1b[") {
		t.Fatalf("non-terminal output leaked live repainting: %q", got)
	}
}

func TestStructuredCategoriesReplaceOneAnotherWithoutFlicker(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", true, 180)
	display.start()
	display.consumeLine(`{"vertexes":[{"digest":"sha256:compile","name":"[heighliner 2/4] RUN go build ./cmd/lisan","started":"2026-08-08T00:00:00Z"}]}`)
	display.consumeLine(`{"vertexes":[{"digest":"sha256:agents","name":"[guild_navigator 2/2] RUN set -eu","started":"2026-08-08T00:00:01Z"}]}`)
	if !strings.Contains(display.lastLine, "Compiling Lisan") || !strings.Contains(display.lastLine, "+1 parallel") {
		t.Fatalf("active category was not pinned while parallel work appeared: %q", display.lastLine)
	}
	display.consumeLine(`{"vertexes":[{"digest":"sha256:compile","name":"[heighliner 2/4] RUN go build ./cmd/lisan","completed":"2026-08-08T00:00:02Z"}]}`)
	if !strings.Contains(display.lastLine, "Fetching selected agents") || !strings.Contains(display.lastLine, "downloading enabled agents") {
		t.Fatalf("completed category was not replaced by the next task: %q", display.lastLine)
	}
	if !strings.Contains(output.String(), "Compiling Lisan ╾━━━━━━━━━━━━━━━━━━━━╼ done\n") {
		t.Fatalf("completed category was not kept above live progress: %q", output.String())
	}
}

func TestStructuredTransferUsesExactDockerByteProgress(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", true, 180)
	display.start()
	display.consumeLine(`{"vertexes":[{"digest":"sha256:context","name":"[internal] load build context","started":"2026-08-08T00:00:00Z"}]}`)
	display.consumeLine(`{"statuses":[{"id":"transfer","vertex":"sha256:context","name":"transferring context","current":1024,"total":2048,"started":"2026-08-08T00:00:00Z"}]}`)
	for _, expected := range []string{"Loading build inputs", "50%", "1.0 KiB/2.0 KiB"} {
		if !strings.Contains(display.lastLine, expected) {
			t.Fatalf("transfer line omitted %q: %q", expected, display.lastLine)
		}
	}
	if !strings.Contains(display.lastLine, "╾━━━━━━━━━━▶─────────╼") {
		t.Fatalf("transfer did not render an Ornithopter rail: %q", display.lastLine)
	}
}

func TestComposeEventsDriveCreateThenStartCategories(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Starting Sietch Tabr", true, 160)
	display.start()
	display.consumeLine(`{"id":"sietch-tabr","status":"Working","text":"Creating"}`)
	if !strings.Contains(display.lastLine, "Creating Sietch Tabr") {
		t.Fatalf("Compose create event rendered as %q", display.lastLine)
	}
	display.consumeLine(`{"id":"sietch-tabr","status":"Done","text":"Created"}`)
	display.consumeLine(`{"id":"sietch-tabr","status":"Working","text":"Starting"}`)
	if !strings.Contains(display.lastLine, "Starting Sietch Tabr") {
		t.Fatalf("Compose start event rendered as %q", display.lastLine)
	}
}

func TestComposeBuildWrapperDoesNotHideBuildKitCategories(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", true, 160)
	display.start()
	display.consumeLine(`{"id":"workspace","status":"Working","text":"Building"}`)
	if _, exists := display.categories["Building Sietch Tabr"]; exists {
		t.Fatal("service-level Compose wrapper became a competing build category")
	}
	display.consumeLine(`{"vertexes":[{"digest":"sha256:compile","name":"[heighliner 2/4] RUN go build","started":"2026-08-08T00:00:00Z"}]}`)
	if !strings.Contains(display.lastLine, "Compiling Lisan") {
		t.Fatalf("BuildKit category was hidden by Compose: %q", display.lastLine)
	}
}

func TestWarningsArePermanentDeduplicatedOutput(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", true, 160)
	display.start()
	warning := `{"warnings":[{"short":"ZGVwcmVjYXRlZCBzeW50YXg=","detail":["dXNlIHRoZSBuZXcgZm9ybQ=="],"url":"https://docs.example/warning"}]}`
	display.consumeLine(warning)
	display.consumeLine(warning)
	got := output.String()
	if strings.Count(got, "Docker ╾━━━━━━━━━━━━━━━━━━━━╼ warning") != 1 || !strings.Contains(got, "deprecated syntax") || !strings.Contains(got, "https://docs.example/warning") {
		t.Fatalf("warning output was not useful and deduplicated: %q", got)
	}
}

func TestStructuredFailureKeepsFailedStepAndBoundedLogs(t *testing.T) {
	display := newDockerProgressDisplay(&bytes.Buffer{}, "Building Sietch Tabr", false, 120)
	display.start()
	display.consumeLine(`{"vertexes":[{"digest":"sha256:failed","name":"[sietch_tabr 8/9] RUN apt-get install missing","started":"2026-08-08T00:00:00Z"}]}`)
	for index := range 220 {
		display.vertices["sha256:failed"].logs.add("routine output " + strings.Repeat("x", index%20))
	}
	display.consumeLine(`{"logs":[{"vertex":"sha256:failed","stream":2,"data":"cGFja2FnZSBub3QgZm91bmQK"}]}`)
	display.consumeLine(`{"vertexes":[{"digest":"sha256:failed","name":"[sietch_tabr 8/9] RUN apt-get install missing","completed":"2026-08-08T00:00:02Z","error":"process exited with code 100"}]}`)
	err := display.commandError(errors.New("exit status 1"))
	for _, expected := range []string{"RUN apt-get install missing", "package not found", "process exited with code 100"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("structured failure omitted %q:\n%s", expected, err)
		}
	}
	if strings.Count(err.Error(), "routine output") > 12 {
		t.Fatalf("failure retained too many routine log lines:\n%s", err)
	}
}

func TestLineWriterAcceptsChunkedJSONAndWindowsNewlines(t *testing.T) {
	var lines []string
	writer := &dockerLineWriter{consume: func(line string) { lines = append(lines, line) }}
	for _, chunk := range []string{"{\"id\":\"one", "\"}\r\n{\"id\":", "\"two\"}"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	writer.close()
	if len(lines) != 2 || lines[0] != `{"id":"one"}` || lines[1] != `{"id":"two"}` {
		t.Fatalf("chunked lines = %#v", lines)
	}
}

func TestPlainFallbackIsEventDriven(t *testing.T) {
	var output bytes.Buffer
	display := newDockerProgressDisplay(&output, "Building Sietch Tabr", true, 160)
	display.start()
	display.consumeLine(`#7 [heighliner 4/4] RUN go build ./cmd/lisan`)
	if !strings.Contains(display.lastLine, "Compiling Lisan") {
		t.Fatalf("plain BuildKit event was not categorized: %q", display.lastLine)
	}
	display.consumeLine(`#7 DONE 3.2s`)
	if !strings.Contains(output.String(), "Compiling Lisan ╾━━━━━━━━━━━━━━━━━━━━╼ done") {
		t.Fatalf("plain completion did not persist real progress: %q", output.String())
	}
}

func TestUnsupportedStructuredModeDetectionIsNarrow(t *testing.T) {
	if !dockerProgressUnsupported(`unsupported --progress value "json"`) {
		t.Fatal("unsupported JSON mode did not trigger fallback")
	}
	if dockerProgressUnsupported("build failed while reporting progress") {
		t.Fatal("ordinary build failure incorrectly triggered a retry")
	}
}

func TestDockerProgressCategoriesUseDistinctDuneColors(t *testing.T) {
	for category, expected := range map[string]string{
		"Loading build inputs":      progressSand,
		"Compiling Lisan":           progressSpice,
		"Fetching selected agents":  progressAgent,
		"Installing selected tools": progressOrange,
		"Exporting image":           progressTeal,
		"Starting Sietch Tabr":      progressCyan,
	} {
		if got := dockerProgressColor(category, false); got != expected {
			t.Fatalf("category %q color = %q, want %q", category, got, expected)
		}
	}
	if got := dockerProgressColor("Compiling Lisan", true); got != progressRed {
		t.Fatalf("failed category color = %q, want red", got)
	}
	styled := styleDockerProgress("Building ╾━━▶──╼", progressSpice, true, true)
	if !strings.HasPrefix(styled, progressBold+progressSpice) || !strings.HasSuffix(styled, progressReset) {
		t.Fatalf("styled progress line is missing ANSI boundaries: %q", styled)
	}
	if plain := styleDockerProgress("Building ╾━━▶──╼", progressSpice, false, true); plain != "Building ╾━━▶──╼" {
		t.Fatalf("disabled color changed output: %q", plain)
	}
}

func TestProgressCommandRetriesUnsupportedStructuredModeOnce(t *testing.T) {
	if os.Getenv("LISAN_PROGRESS_HELPER") == "1" {
		mode := os.Args[len(os.Args)-1]
		if mode == "structured" {
			fmt.Fprintln(os.Stderr, `unsupported --progress value "json"`)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, `#1 [heighliner 1/1] RUN go build`)
		fmt.Fprintln(os.Stderr, `#1 DONE 0.1s`)
		os.Exit(0)
	}
	t.Setenv(dockerVerboseEnvironment, "")
	primary := exec.Command(os.Args[0], "-test.run=TestProgressCommandRetriesUnsupportedStructuredModeOnce", "--", "structured")
	primary.Env = append(os.Environ(), "LISAN_PROGRESS_HELPER=1")
	fallback := exec.Command(os.Args[0], "-test.run=TestProgressCommandRetriesUnsupportedStructuredModeOnce", "--", "plain")
	fallback.Env = append(os.Environ(), "LISAN_PROGRESS_HELPER=1")
	var output bytes.Buffer
	if err := runDockerProgress(&output, "Building Sietch Tabr", primary, fallback); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Count(got, "Building Sietch Tabr ╾▶───────────────────╼ running") != 1 || !strings.Contains(got, "╾━━━━━━━━━━━━━━━━━━━━╼ done") {
		t.Fatalf("fallback did not preserve one stable operation lifecycle: %q", got)
	}
}

func TestMovingProgressBlockNeverEscapesBar(t *testing.T) {
	for _, dimensions := range []struct {
		width int
		block int
	}{{20, 1}, {5, 5}} {
		for step := range 10_000 {
			position := movingBlockPosition(step, dimensions.width, dimensions.block)
			if position < 0 || position+dimensions.block > dimensions.width {
				t.Fatalf("step %d produced [%d,%d) in width %d", step, position, position+dimensions.block, dimensions.width)
			}
		}
	}
}
