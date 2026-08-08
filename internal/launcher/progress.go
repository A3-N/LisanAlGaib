package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"

	"lisanalgaib/internal/cliout"
)

const (
	dockerVerboseEnvironment = "LISAN_DOCKER_VERBOSE"
	progressBarWidth         = 20
	progressRawLines         = 256
	progressRawBytes         = 64 << 10
	progressLogLines         = 200
	progressLogBytes         = 64 << 10
	progressReset            = "\x1b[0m"
	progressBold             = "\x1b[1m"
	progressSpice            = "\x1b[38;2;229;168;83m"
	progressSand             = "\x1b[38;2;201;154;102m"
	progressAgent            = "\x1b[38;2;184;161;217m"
	progressOrange           = "\x1b[38;2;224;123;57m"
	progressTeal             = "\x1b[38;2;111;183;183m"
	progressCyan             = "\x1b[38;2;124;183;201m"
	progressGreen            = "\x1b[38;2;126;190;132m"
	progressRed              = "\x1b[38;2;212;106;94m"
)

var (
	dockerANSI       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	plainBuildLine   = regexp.MustCompile(`^#([0-9]+)\s+(.*)$`)
	plainComposeLine = regexp.MustCompile(`(?i)^.*?container\s+([^\s]+)\s+(creating|created|starting|started|running|waiting|healthy|pulling|pulled|removing|removed|stopping|stopped|error)(?:\s|$)`)
)

// runDockerProgress renders Docker activity from structured BuildKit or
// Compose events. A fallback command may be supplied for Docker releases that
// reject the structured progress mode; its plain output is parsed as events
// instead of being animated by a timer.
func runDockerProgress(output io.Writer, label string, command *exec.Cmd, fallback ...*exec.Cmd) error {
	if output == nil {
		output = io.Discard
	}
	if dockerVerbose() {
		return runVerboseDocker(output, command, fallback...)
	}

	display := newDockerProgressDisplay(output, label, terminalWriter(output), dockerOutputWidth(output))
	display.start()
	err := runDockerProgressAttempt(command, display)
	if err != nil && len(fallback) > 0 && dockerProgressUnsupported(display.raw.text()) {
		display.reset()
		err = runDockerProgressAttempt(fallback[0], display)
	}
	display.finish(err == nil)
	if err != nil {
		return display.commandError(err)
	}
	return nil
}

func runVerboseDocker(output io.Writer, command *exec.Cmd, fallback ...*exec.Cmd) error {
	captured := newLineRing(progressRawLines, progressRawBytes)
	capture := &lineCaptureWriter{lines: captured}
	writer := io.MultiWriter(output, capture)
	command.Stdout, command.Stderr = writer, writer
	err := command.Run()
	capture.close()
	if err != nil && len(fallback) > 0 && dockerProgressUnsupported(captured.text()) {
		fallback[0].Stdout, fallback[0].Stderr = output, output
		return fallback[0].Run()
	}
	return err
}

func runDockerProgressAttempt(command *exec.Cmd, display *dockerProgressDisplay) error {
	sink := &dockerLineWriter{consume: display.consumeLine}
	command.Stdout, command.Stderr = sink, sink
	err := command.Run()
	sink.close()
	return err
}

type dockerLineWriter struct {
	mu      sync.Mutex
	pending []byte
	consume func(string)
}

func (writer *dockerLineWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	written := len(data)
	writer.pending = append(writer.pending, data...)
	for {
		newline := bytes.IndexByte(writer.pending, '\n')
		if newline < 0 {
			if len(writer.pending) > progressRawBytes {
				writer.consume(string(writer.pending[:progressRawBytes]))
				writer.pending = writer.pending[progressRawBytes:]
			}
			return written, nil
		}
		line := strings.TrimSuffix(string(writer.pending[:newline]), "\r")
		writer.pending = writer.pending[newline+1:]
		writer.consume(line)
	}
}

func (writer *dockerLineWriter) close() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.pending) > 0 {
		writer.consume(strings.TrimSuffix(string(writer.pending), "\r"))
		writer.pending = nil
	}
}

type lineCaptureWriter struct {
	mu      sync.Mutex
	pending string
	lines   *lineRing
}

func (writer *lineCaptureWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.pending += string(data)
	for {
		line, remaining, found := strings.Cut(writer.pending, "\n")
		if !found {
			break
		}
		writer.lines.add(strings.TrimSuffix(line, "\r"))
		writer.pending = remaining
	}
	if len(writer.pending) > progressRawBytes {
		writer.lines.add(writer.pending[:progressRawBytes])
		writer.pending = writer.pending[progressRawBytes:]
	}
	return len(data), nil
}

func (writer *lineCaptureWriter) close() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.pending != "" {
		writer.lines.add(writer.pending)
		writer.pending = ""
	}
}

type lineRing struct {
	lines    []string
	bytes    int
	maxLines int
	maxBytes int
}

func newLineRing(maxLines, maxBytes int) *lineRing {
	return &lineRing{maxLines: maxLines, maxBytes: maxBytes}
}

func (ring *lineRing) add(line string) {
	line = cleanDockerLine(line)
	if line == "" {
		return
	}
	if len(line) > ring.maxBytes {
		line = line[len(line)-ring.maxBytes:]
		for !utf8.ValidString(line) && len(line) > 0 {
			line = line[1:]
		}
	}
	ring.lines = append(ring.lines, line)
	ring.bytes += len(line)
	for len(ring.lines) > ring.maxLines || ring.bytes > ring.maxBytes {
		ring.bytes -= len(ring.lines[0])
		ring.lines = ring.lines[1:]
	}
}

func (ring *lineRing) reset() {
	ring.lines = nil
	ring.bytes = 0
}

func (ring *lineRing) text() string { return strings.Join(ring.lines, "\n") }

type buildkitStatus struct {
	Vertexes []*buildkitVertex   `json:"vertexes,omitempty"`
	Statuses []*buildkitTransfer `json:"statuses,omitempty"`
	Logs     []*buildkitLog      `json:"logs,omitempty"`
	Warnings []*buildkitWarning  `json:"warnings,omitempty"`
}

type buildkitVertex struct {
	Digest    string          `json:"digest,omitempty"`
	Name      string          `json:"name,omitempty"`
	Started   json.RawMessage `json:"started,omitempty"`
	Completed json.RawMessage `json:"completed,omitempty"`
	Cached    bool            `json:"cached,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type buildkitTransfer struct {
	ID        string          `json:"id"`
	Vertex    string          `json:"vertex,omitempty"`
	Name      string          `json:"name,omitempty"`
	Total     int64           `json:"total,omitempty"`
	Current   int64           `json:"current"`
	Started   json.RawMessage `json:"started,omitempty"`
	Completed json.RawMessage `json:"completed,omitempty"`
}

type buildkitLog struct {
	Vertex string `json:"vertex,omitempty"`
	Stream int    `json:"stream,omitempty"`
	Data   []byte `json:"data"`
}

type buildkitWarning struct {
	Short  []byte   `json:"short,omitempty"`
	Detail [][]byte `json:"detail,omitempty"`
	URL    string   `json:"url,omitempty"`
}

type composeProgress struct {
	ID       string `json:"id,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Text     string `json:"text,omitempty"`
	Details  string `json:"details,omitempty"`
	Current  int64  `json:"current,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Percent  int    `json:"percent,omitempty"`
}

type progressVertex struct {
	id        string
	name      string
	category  string
	started   bool
	completed bool
	cached    bool
	err       string
	order     int
	logs      *lineRing
}

type progressTransfer struct {
	id        string
	vertex    string
	name      string
	current   int64
	total     int64
	started   bool
	completed bool
	order     int
}

type progressResource struct {
	id        string
	category  string
	text      string
	details   string
	current   int64
	total     int64
	completed bool
	failed    bool
	order     int
}

type progressCategory struct {
	name     string
	activity int
	order    int
}

type dockerProgressDisplay struct {
	output       io.Writer
	label        string
	terminal     bool
	width        int
	event        int
	current      string
	lastLine     string
	vertices     map[string]*progressVertex
	transfers    map[string]*progressTransfer
	resources    map[string]*progressResource
	categories   map[string]*progressCategory
	committed    map[string]bool
	warnings     map[string]bool
	daemonErrors []string
	raw          *lineRing
}

func newDockerProgressDisplay(output io.Writer, label string, terminal bool, width int) *dockerProgressDisplay {
	if width < 50 {
		width = 50
	}
	return &dockerProgressDisplay{
		output: output, label: label, terminal: terminal, width: width,
		vertices: make(map[string]*progressVertex), transfers: make(map[string]*progressTransfer),
		resources: make(map[string]*progressResource), categories: make(map[string]*progressCategory),
		committed: make(map[string]bool), warnings: make(map[string]bool), raw: newLineRing(progressRawLines, progressRawBytes),
	}
}

func (display *dockerProgressDisplay) start() {
	if display.terminal {
		display.writeLive(display.label+" "+renderProgressRail(0, 0, false)+" waiting for Docker", progressSpice)
		return
	}
	cliout.Start(display.output, display.label, "waiting for Docker")
}

func (display *dockerProgressDisplay) reset() {
	display.event = 0
	display.current = ""
	display.vertices = make(map[string]*progressVertex)
	display.transfers = make(map[string]*progressTransfer)
	display.resources = make(map[string]*progressResource)
	display.categories = make(map[string]*progressCategory)
	display.committed = make(map[string]bool)
	display.warnings = make(map[string]bool)
	display.daemonErrors = nil
	display.raw.reset()
}

func (display *dockerProgressDisplay) consumeLine(line string) {
	display.raw.add(line)
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	if display.consumeJSON(trimmed) || display.consumePlain(trimmed) {
		display.render()
	}
}

func (display *dockerProgressDisplay) consumeJSON(line string) bool {
	if !strings.HasPrefix(line, "{") || !json.Valid([]byte(line)) {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &fields) != nil {
		return false
	}
	if fields["vertexes"] != nil || fields["statuses"] != nil || fields["logs"] != nil || fields["warnings"] != nil {
		var status buildkitStatus
		if json.Unmarshal([]byte(line), &status) != nil {
			return false
		}
		display.consumeBuildkit(status)
		return true
	}
	if fields["id"] != nil || fields["status"] != nil || fields["text"] != nil {
		var event composeProgress
		if json.Unmarshal([]byte(line), &event) != nil {
			return false
		}
		display.consumeCompose(event)
		return true
	}
	var daemon struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(line), &daemon) == nil {
		message := nonempty(strings.TrimSpace(daemon.Error), strings.TrimSpace(daemon.Message))
		if message != "" {
			display.daemonErrors = append(display.daemonErrors, cleanDockerLine(message))
			return true
		}
	}
	return false
}

func (display *dockerProgressDisplay) consumeBuildkit(status buildkitStatus) {
	display.event++
	for _, incoming := range status.Vertexes {
		if incoming == nil || incoming.Digest == "" {
			continue
		}
		vertex := display.vertices[incoming.Digest]
		if vertex == nil {
			vertex = &progressVertex{id: incoming.Digest, logs: newLineRing(progressLogLines, progressLogBytes)}
			display.vertices[incoming.Digest] = vertex
		}
		if incoming.Name != "" {
			vertex.name = incoming.Name
			vertex.category = dockerVertexCategory(display.label, incoming.Name)
		}
		vertex.started = vertex.started || rawTimeSet(incoming.Started)
		vertex.completed = vertex.completed || rawTimeSet(incoming.Completed)
		vertex.cached = vertex.cached || incoming.Cached
		if incoming.Error != "" {
			vertex.err = incoming.Error
			vertex.completed = true
		}
		vertex.order = display.event
		display.touchCategory(vertex.category)
	}
	for _, incoming := range status.Statuses {
		if incoming == nil || incoming.ID == "" {
			continue
		}
		transfer := display.transfers[incoming.ID]
		if transfer == nil {
			transfer = &progressTransfer{id: incoming.ID}
			display.transfers[incoming.ID] = transfer
		}
		transfer.vertex = incoming.Vertex
		transfer.name = incoming.Name
		transfer.current = incoming.Current
		transfer.total = incoming.Total
		transfer.started = transfer.started || rawTimeSet(incoming.Started) || incoming.Current > 0
		transfer.completed = rawTimeSet(incoming.Completed) || (incoming.Total > 0 && incoming.Current >= incoming.Total)
		transfer.order = display.event
		if vertex := display.vertices[incoming.Vertex]; vertex != nil {
			display.touchCategory(vertex.category)
		}
	}
	for _, incoming := range status.Logs {
		if incoming == nil {
			continue
		}
		vertex := display.vertices[incoming.Vertex]
		if vertex == nil {
			vertex = &progressVertex{id: incoming.Vertex, category: display.label, logs: newLineRing(progressLogLines, progressLogBytes)}
			display.vertices[incoming.Vertex] = vertex
		}
		for _, line := range strings.Split(string(incoming.Data), "\n") {
			vertex.logs.add(line)
		}
		vertex.started = true
		vertex.order = display.event
		display.touchCategory(vertex.category)
	}
	for _, warning := range status.Warnings {
		if warning == nil {
			continue
		}
		parts := []string{string(warning.Short)}
		for _, detail := range warning.Detail {
			parts = append(parts, string(detail))
		}
		if warning.URL != "" {
			parts = append(parts, warning.URL)
		}
		display.emitWarning(strings.Join(parts, " · "))
	}
}

func (display *dockerProgressDisplay) consumeCompose(event composeProgress) {
	display.event++
	if strings.EqualFold(event.Status, "warning") {
		display.emitWarning(nonempty(event.Details, event.Text))
		return
	}
	if strings.HasPrefix(display.label, "Building ") && !strings.EqualFold(event.Status, "error") &&
		(strings.EqualFold(event.Text, "building") || strings.EqualFold(event.Text, "built")) {
		// Compose wraps the detailed BuildKit stream in a service-level event.
		// The wrapper spans the whole solve and would otherwise hide the real
		// categories that arrive alongside it.
		return
	}
	if event.ID == "" {
		if strings.EqualFold(event.Status, "error") {
			display.daemonErrors = append(display.daemonErrors, cleanDockerLine(nonempty(event.Details, event.Text)))
		}
		return
	}
	resource := display.resources[event.ID]
	if resource == nil {
		resource = &progressResource{id: event.ID}
		display.resources[event.ID] = resource
	}
	resource.category = dockerComposeCategory(display.label, event.Text)
	resource.text = event.Text
	resource.details = event.Details
	resource.current = event.Current
	resource.total = event.Total
	resource.completed = strings.EqualFold(event.Status, "done")
	resource.failed = strings.EqualFold(event.Status, "error")
	resource.order = display.event
	display.touchCategory(resource.category)
	if resource.failed {
		display.daemonErrors = append(display.daemonErrors, cleanDockerLine(nonempty(event.Details, event.Text)))
	}
}

func (display *dockerProgressDisplay) consumePlain(line string) bool {
	line = strings.TrimSpace(dockerANSI.ReplaceAllString(line, ""))
	if matches := plainBuildLine.FindStringSubmatch(line); len(matches) == 3 {
		display.event++
		id, content := "#"+matches[1], strings.TrimSpace(matches[2])
		vertex := display.vertices[id]
		if vertex == nil {
			vertex = &progressVertex{id: id, logs: newLineRing(progressLogLines, progressLogBytes)}
			display.vertices[id] = vertex
		}
		upper := strings.ToUpper(content)
		switch {
		case strings.HasPrefix(content, "["):
			vertex.name = content
			vertex.category = dockerVertexCategory(display.label, content)
			vertex.started = true
		case strings.HasPrefix(upper, "CACHED"):
			vertex.cached, vertex.completed = true, true
		case strings.HasPrefix(upper, "DONE"):
			vertex.completed = true
		case strings.HasPrefix(upper, "ERROR"):
			vertex.err, vertex.completed = content, true
		default:
			vertex.logs.add(content)
		}
		if vertex.category == "" {
			vertex.category = display.label
		}
		vertex.order = display.event
		display.touchCategory(vertex.category)
		if strings.Contains(upper, " WARN") || strings.HasPrefix(upper, "WARN") {
			display.emitWarning(content)
		}
		return true
	}
	if matches := plainComposeLine.FindStringSubmatch(line); len(matches) == 3 {
		display.event++
		text := strings.ToLower(matches[2])
		resource := &progressResource{
			id: matches[1], text: matches[2], category: dockerComposeCategory(display.label, text),
			completed: text == "created" || text == "started" || text == "running" || text == "healthy" || text == "pulled" || text == "removed" || text == "stopped",
			failed:    text == "error", order: display.event,
		}
		display.resources[resource.id] = resource
		display.touchCategory(resource.category)
		return true
	}
	upper := strings.ToUpper(line)
	if strings.HasPrefix(upper, "WARNING:") || strings.HasPrefix(upper, "WARN ") {
		display.emitWarning(line)
		return true
	}
	return false
}

func (display *dockerProgressDisplay) touchCategory(name string) {
	if name == "" {
		return
	}
	category := display.categories[name]
	if category == nil {
		category = &progressCategory{name: name}
		display.categories[name] = category
	}
	category.activity++
	category.order = display.event
}

type categoryStats struct {
	name       string
	total      int
	completed  int
	cached     int
	active     int
	failed     bool
	current    int64
	bytesTotal int64
	transfer   bool
	activeStep string
	order      int
	activity   int
}

func (display *dockerProgressDisplay) stats(name string) categoryStats {
	stats := categoryStats{name: name}
	if category := display.categories[name]; category != nil {
		stats.order, stats.activity = category.order, category.activity
	}
	for _, vertex := range display.vertices {
		if vertex.category != name {
			continue
		}
		stats.total++
		if vertex.completed {
			stats.completed++
		} else if vertex.started {
			stats.active++
		}
		if vertex.cached {
			stats.cached++
		}
		if vertex.err != "" {
			stats.failed = true
		}
		if vertex.order >= stats.order || stats.activeStep == "" {
			stats.activeStep = shortDockerStep(vertex.name, name)
		}
	}
	for _, transfer := range display.transfers {
		vertex := display.vertices[transfer.vertex]
		if vertex == nil || vertex.category != name || transfer.completed {
			continue
		}
		if transfer.started {
			stats.active++
		}
		if transfer.total <= 0 {
			continue
		}
		if !stats.transfer || transfer.order >= stats.order {
			stats.transfer = true
			stats.current = transfer.current
			stats.bytesTotal = transfer.total
			stats.activeStep = nonempty(shortDockerStep(transfer.name, name), stats.activeStep)
		}
	}
	for _, resource := range display.resources {
		if resource.category != name {
			continue
		}
		stats.total++
		if resource.completed {
			stats.completed++
		} else {
			stats.active++
		}
		stats.failed = stats.failed || resource.failed
		if resource.total > 0 && !resource.completed && (!stats.transfer || resource.order >= stats.order) {
			stats.transfer = true
			stats.current, stats.bytesTotal = resource.current, resource.total
		}
		if resource.order >= stats.order || stats.activeStep == "" {
			stats.activeStep = nonempty(resource.details, resource.text)
		}
	}
	return stats
}

func (display *dockerProgressDisplay) selectCategory() categoryStats {
	if display.current != "" {
		current := display.stats(display.current)
		if current.failed || current.active > 0 {
			return current
		}
	}
	var selected categoryStats
	for name := range display.categories {
		candidate := display.stats(name)
		if candidate.failed && !selected.failed {
			selected = candidate
			continue
		}
		if candidate.failed == selected.failed && candidate.active > 0 && (selected.active == 0 || candidate.order < selected.order) {
			selected = candidate
		}
	}
	display.current = selected.name
	return selected
}

func (display *dockerProgressDisplay) render() {
	if !display.terminal {
		return
	}
	display.commitCompletedCategories(false)
	stats := display.selectCategory()
	if stats.name == "" {
		return
	}
	rail, info := renderCategoryRail(stats)
	parts := []string{stats.name + " " + rail, info}
	if stats.cached > 0 {
		parts = append(parts, fmt.Sprintf("%d cached", stats.cached))
	}
	if step := cleanDockerLine(stats.activeStep); step != "" {
		parts = append(parts, truncateRunes(step, 42))
	}
	if parallel := display.parallelCategories(stats.name); parallel > 0 {
		parts = append(parts, fmt.Sprintf("+%d parallel", parallel))
	}
	display.writeLive(strings.Join(nonemptyParts(parts), " · "), dockerProgressColor(stats.name, stats.failed))
}

func (display *dockerProgressDisplay) commitCompletedCategories(force bool) {
	var completed []categoryStats
	for name := range display.categories {
		if display.committed[name] || name == display.label {
			continue
		}
		stats := display.stats(name)
		done := stats.total > 0 && stats.active == 0 && stats.completed == stats.total && !stats.failed
		if done || (force && stats.total > 0 && !stats.failed) {
			completed = append(completed, stats)
		}
	}
	if len(completed) == 0 {
		return
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].order < completed[j].order })
	fmt.Fprint(display.output, "\r\x1b[2K")
	for _, stats := range completed {
		line := stats.name + " " + renderProgressRail(progressBarWidth, 0, true) + " done"
		fmt.Fprintln(display.output, styleDockerProgress(line, progressGreen, cliout.ColorEnabled(display.output), true))
		display.committed[stats.name] = true
	}
	display.lastLine = ""
}

func renderCategoryRail(stats categoryStats) (string, string) {
	if stats.failed {
		return renderProgressRail(progressBarWidth, 0, true), "failed"
	}
	if stats.transfer && stats.bytesTotal > 0 {
		current := min(max(stats.current, 0), stats.bytesTotal)
		filled := int(current * progressBarWidth / stats.bytesTotal)
		return renderProgressRail(filled, filled, filled == progressBarWidth), fmt.Sprintf("%d%% · %s/%s", current*100/stats.bytesTotal, humanBytes(current), humanBytes(stats.bytesTotal))
	}
	if stats.total > 0 {
		filled := stats.completed * progressBarWidth / stats.total
		nose := filled
		if stats.completed < stats.total && filled < progressBarWidth {
			remaining := progressBarWidth - filled
			nose = filled + movingBlockPosition(stats.activity, remaining, 1)
		}
		return renderProgressRail(filled, nose, stats.completed == stats.total), fmt.Sprintf("%d/%d steps", stats.completed, stats.total)
	}
	return renderProgressRail(0, movingBlockPosition(stats.activity, progressBarWidth, 1), false), "active"
}

func renderProgressRail(filled, nose int, complete bool) string {
	track := []rune(strings.Repeat("─", progressBarWidth))
	filled = min(max(filled, 0), progressBarWidth)
	for index := 0; index < filled; index++ {
		track[index] = '━'
	}
	if !complete && progressBarWidth > 0 {
		nose = min(max(nose, filled), progressBarWidth-1)
		track[nose] = '▶'
	}
	return "╾" + string(track) + "╼"
}

func (display *dockerProgressDisplay) parallelCategories(current string) int {
	parallel := 0
	for name := range display.categories {
		if name != current && display.stats(name).active > 0 {
			parallel++
		}
	}
	return parallel
}

func (display *dockerProgressDisplay) emitWarning(message string) {
	message = cleanDockerLine(strings.TrimSpace(message))
	if message == "" || display.warnings[message] {
		return
	}
	display.warnings[message] = true
	if display.terminal {
		fmt.Fprint(display.output, "\r\x1b[2K")
	}
	cliout.Warning(display.output, "Docker", message)
	display.lastLine = ""
}

func (display *dockerProgressDisplay) finish(success bool) {
	if success && display.terminal {
		display.commitCompletedCategories(true)
	}
	status, rail := "done", renderProgressRail(progressBarWidth, 0, true)
	if !success {
		status, rail = "failed", renderProgressRail(progressBarWidth, 0, true)
	}
	line := fmt.Sprintf("%s %s %s", display.label, rail, status)
	if display.terminal {
		fmt.Fprint(display.output, "\r\x1b[2K")
	}
	color := progressGreen
	if !success {
		color = progressRed
	}
	fmt.Fprintln(display.output, styleDockerProgress(line, color, cliout.ColorEnabled(display.output), true))
	display.lastLine = ""
}

func (display *dockerProgressDisplay) writeLive(line, color string) {
	line = truncateRunes(line, display.width)
	if line == display.lastLine {
		return
	}
	fmt.Fprintf(display.output, "\r\x1b[2K%s", styleDockerProgress(line, color, cliout.ColorEnabled(display.output), false))
	display.lastLine = line
}

func dockerProgressColor(category string, failed bool) string {
	if failed {
		return progressRed
	}
	lower := strings.ToLower(category)
	switch {
	case strings.Contains(lower, "loading"):
		return progressSand
	case strings.Contains(lower, "compiling"):
		return progressSpice
	case strings.Contains(lower, "agent"):
		return progressAgent
	case strings.Contains(lower, "installing"):
		return progressOrange
	case strings.Contains(lower, "exporting"):
		return progressTeal
	case strings.Contains(lower, "creating"), strings.Contains(lower, "starting"), strings.Contains(lower, "pulling"):
		return progressCyan
	default:
		return progressSpice
	}
}

func styleDockerProgress(line, color string, enabled, bold bool) string {
	if !enabled {
		return line
	}
	prefix := color
	if bold {
		prefix = progressBold + prefix
	}
	return prefix + line + progressReset
}

func (display *dockerProgressDisplay) commandError(commandErr error) error {
	var lines []string
	var failed *progressVertex
	for _, vertex := range display.vertices {
		if vertex.err != "" && (failed == nil || vertex.order > failed.order) {
			failed = vertex
		}
	}
	if failed != nil {
		lines = append(lines, "step: "+cleanDockerLine(failed.name))
		start := max(0, len(failed.logs.lines)-12)
		lines = append(lines, failed.logs.lines[start:]...)
		lines = append(lines, cleanDockerLine(failed.err))
	}
	lines = append(lines, display.daemonErrors...)
	if raw := dockerFailureSummary(display.raw.text()); raw != "" {
		raw = strings.TrimPrefix(raw, "Docker failure:\n  ")
		lines = append(lines, strings.Split(raw, "\n  ")...)
	}
	lines = uniqueDockerLines(nonemptyParts(lines), 18)
	if len(lines) == 0 {
		return commandErr
	}
	return fmt.Errorf("%w\nDocker failure:\n  %s", commandErr, strings.Join(lines, "\n  "))
}

func dockerVertexCategory(label, name string) string {
	lower := strings.ToLower(name)
	extension := strings.TrimSpace(strings.TrimPrefix(label, "Building extension "))
	if strings.Contains(lower, "exporting") || strings.Contains(lower, "unpacking to") {
		if extension != label && extension != "" {
			return "Exporting " + extension
		}
		return "Exporting image"
	}
	if strings.Contains(lower, "load build definition") || strings.Contains(lower, "load .dockerignore") ||
		strings.Contains(lower, "load build context") || strings.Contains(lower, "load metadata") ||
		strings.Contains(lower, "importing cache") || strings.Contains(lower, "resolve image config") {
		if extension != label && extension != "" {
			return "Loading " + extension
		}
		return "Loading build inputs"
	}
	if strings.Contains(lower, "heighliner") {
		if extension != label && extension != "" {
			return "Compiling " + extension
		}
		return "Compiling Lisan"
	}
	if strings.Contains(lower, "guild_navigator") {
		return "Fetching selected agents"
	}
	if strings.Contains(lower, "apt-get install -y --no-install-recommends $packages") || strings.Contains(lower, "installing selected packages") {
		return "Installing selected tools"
	}
	if extension != label && extension != "" {
		return "Preparing " + extension
	}
	if strings.Contains(lower, "sietch_tabr") || strings.HasPrefix(label, "Building Sietch Tabr") {
		return "Preparing Sietch Tabr"
	}
	return label
}

func dockerComposeCategory(label, text string) string {
	target := strings.TrimSpace(strings.TrimPrefix(label, "Starting "))
	if target == label || target == "" {
		target = "Docker services"
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "pull"), strings.Contains(lower, "download"):
		return "Pulling images"
	case strings.Contains(lower, "creat"), strings.Contains(lower, "configur"):
		return "Creating " + target
	case strings.Contains(lower, "start"), strings.Contains(lower, "wait"), strings.Contains(lower, "health"), strings.Contains(lower, "running"):
		return "Starting " + target
	default:
		return label
	}
}

func shortDockerStep(step, category string) string {
	step = strings.TrimSpace(step)
	if close := strings.Index(step, "] "); strings.HasPrefix(step, "[") && close >= 0 {
		step = step[close+2:]
	}
	lower := strings.ToLower(step)
	switch category {
	case "Fetching selected agents":
		if strings.HasPrefix(lower, "run ") {
			return "downloading enabled agents"
		}
	case "Installing selected tools":
		return "installing selected packages"
	}
	return step
}

func rawTimeSet(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}

func dockerProgressUnsupported(output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "progress") {
		return false
	}
	return strings.Contains(lower, "unsupported") || strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "unknown") || strings.Contains(lower, "invalid") || strings.Contains(lower, "not a valid")
}

func terminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func dockerOutputWidth(writer io.Writer) int {
	if file, ok := writer.(*os.File); ok {
		if width, _, err := term.GetSize(file.Fd()); err == nil && width > 0 {
			return width
		}
	}
	if width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && width > 0 {
		return width
	}
	return 120
}

func dockerVerbose() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(dockerVerboseEnvironment)))
	return value == "1" || value == "true" || value == "yes"
}

func dockerCommandError(commandErr error, output string) error {
	summary := dockerFailureSummary(output)
	if summary == "" {
		return commandErr
	}
	return fmt.Errorf("%w\n%s", commandErr, summary)
}

func dockerFailureSummary(output string) string {
	output = dockerANSI.ReplaceAllString(strings.ReplaceAll(output, "\r\n", "\n"), "")
	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = cleanDockerLine(line)
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !(strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed))) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}

	dockerfile, lastError := -1, -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Dockerfile:") || strings.Contains(trimmed, "/Dockerfile:") {
			dockerfile = index
		}
		if strings.Contains(trimmed, " ERROR") || strings.HasPrefix(trimmed, "ERROR:") || strings.Contains(strings.ToLower(trimmed), "failed to solve") {
			lastError = index
		}
	}

	var selected []string
	if dockerfile >= 0 {
		for index := dockerfile - 1; index >= 0; index-- {
			if strings.Contains(lines[index], "ERROR") {
				selected = append(selected, lines[index])
				break
			}
		}
		end := min(dockerfile+14, len(lines))
		selected = append(selected, lines[dockerfile:end]...)
		if lastError >= end {
			selected = append(selected, lines[lastError])
		}
	} else {
		end := len(lines)
		if lastError >= 0 {
			end = lastError + 1
		}
		start := max(0, end-8)
		selected = append(selected, lines[start:end]...)
	}

	selected = uniqueDockerLines(selected, 18)
	if len(selected) == 0 {
		return ""
	}
	return "Docker failure:\n  " + strings.Join(selected, "\n  ")
}

func cleanDockerLine(line string) string {
	var cleaned strings.Builder
	for _, character := range line {
		if character == '\t' || character >= ' ' {
			cleaned.WriteRune(character)
		}
	}
	line = strings.TrimRight(cleaned.String(), " \t")
	const maximum = 800
	if utf8.RuneCountInString(line) > maximum {
		line = truncateRunes(line, maximum)
	}
	return line
}

func uniqueDockerLines(lines []string, limit int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, min(len(lines), limit))
	for _, line := range lines {
		if seen[line] {
			continue
		}
		seen[line] = true
		result = append(result, line)
		if len(result) == limit {
			break
		}
	}
	return result
}

func movingBlockPosition(step, width, blockWidth int) int {
	maximum := width - blockWidth
	if maximum <= 0 {
		return 0
	}
	period := 2 * maximum
	position := step % period
	if position > maximum {
		position = period - position
	}
	return position
}

func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := "B"
	for _, candidate := range units {
		size /= 1024
		unit = candidate
		if size < 1024 {
			break
		}
	}
	if size >= 10 {
		return fmt.Sprintf("%.0f %s", size, unit)
	}
	return fmt.Sprintf("%.1f %s", size, unit)
}

func truncateRunes(value string, maximum int) string {
	if maximum < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	return string(runes[:maximum-1]) + "…"
}

func nonemptyParts(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}
