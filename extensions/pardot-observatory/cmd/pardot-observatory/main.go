package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lisanalgaib/internal/connectors"
	"lisanalgaib/internal/extensionhost"
)

func main() {
	listen := flag.String("listen", envOr("LISAN_EXTENSION_LISTEN", "127.0.0.1:7777"), "HTTP listen address")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := newObservatory()
	if err := extensionhost.Serve(ctx, *listen, backend, log.New(os.Stderr, "pardot: ", log.LstdFlags)); err != nil {
		log.Fatal(err)
	}
}

type observatory struct {
	mu        sync.RWMutex
	jobs      map[string]*surveyJob
	sessions  map[string]*fieldSession
	notes     []string
	sequence  atomic.Uint64
	startedAt time.Time
	region    string
	stateDir  string
	sharedDir string
}

type surveyJob struct {
	job      connectors.Job
	artifact []byte
	cancel   context.CancelFunc
}

type fieldSession struct {
	session connectors.Session
}

func newObservatory() *observatory {
	service := &observatory{
		jobs: map[string]*surveyJob{}, sessions: map[string]*fieldSession{},
		startedAt: time.Now(), region: envOr("PARDOT_REGION", "Unknown erg"),
		stateDir: os.Getenv("LISAN_EXTENSION_STATE"), sharedDir: os.Getenv("LISAN_EXTENSION_SHARED"),
	}
	service.loadNotes()
	return service
}

func (o *observatory) Manifest(context.Context) (connectors.Manifest, error) {
	return connectors.Manifest{
		ProtocolVersion: connectors.ProtocolVersion,
		ID:              "pardot-observatory", Name: "Pardot Observatory", Version: "3.0.0", Icon: "󰆤",
		Description: "Independent field-survey service demonstrating every protocol v3 extension surface",
		Views: []connectors.ViewDescriptor{
			{ID: "overview", Title: "Station Overview", Default: true},
			{ID: "observations", Title: "Observations"},
		},
		Actions: []connectors.ActionDescriptor{
			{ID: "survey", Name: "Run field survey", Description: "Collect samples asynchronously and emit a Markdown report", Inputs: []connectors.InputSpec{
				{ID: "subject", Label: "Subject", Kind: connectors.InputText, Required: true, Default: "maker traces", Pattern: `^[A-Za-z0-9 ._-]{1,80}$`},
				{ID: "samples", Label: "Samples", Kind: connectors.InputNumber, Required: true, Default: "5", Min: 1, Max: 20},
				{ID: "detail", Label: "Detail", Kind: connectors.InputSelect, Default: "field", Options: []connectors.InputOption{{Value: "brief", Label: "Brief"}, {Value: "field", Label: "Field"}, {Value: "deep", Label: "Deep"}}},
				{ID: "environment", Label: "Include environment", Kind: connectors.InputBoolean, Default: "false"},
			}},
			{ID: "record-note", Name: "Record observation", Description: "Store a note in extension-owned state", Inputs: []connectors.InputSpec{{ID: "note", Label: "Note", Kind: connectors.InputText, Required: true, Default: "Spice bloom remains stable"}}},
		},
		Sessions: []connectors.SessionDescriptor{{ID: "field-console", Name: "Field Console", Description: "Restricted console supporting only observatory commands"}},
	}, nil
}

func (o *observatory) View(_ context.Context, id string) (connectors.View, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	switch id {
	case "overview":
		state := "ephemeral"
		if o.stateDir != "" {
			state = "persistent grant active"
		}
		shared := "not mounted"
		if o.sharedDir != "" {
			shared = o.sharedDir
		}
		return connectors.View{ID: id, Title: "Station Overview", Updated: time.Now().UTC().Format(time.RFC3339), Blocks: []connectors.Block{
			{ID: "station", Kind: connectors.BlockStatus, Title: "Station", Tone: connectors.ToneSuccess, Text: "Pardot Observatory is online", Detail: "The service owns this data and UI structure; Lisan only renders it."},
			{ID: "runtime", Kind: connectors.BlockKeyValue, Title: "Runtime", Fields: []connectors.FieldValue{{Label: "Region", Value: o.region}, {Label: "Uptime", Value: time.Since(o.startedAt).Round(time.Second).String()}, {Label: "State", Value: state}, {Label: "Shared", Value: shared}}},
			{ID: "surfaces", Kind: connectors.BlockList, Title: "Protocol surfaces", Items: []connectors.ListItem{{Label: "Structured views", Detail: "status, key/value, list, table, and progress blocks"}, {Label: "Typed actions", Detail: "text, number, select, and boolean inputs"}, {Label: "Jobs and artifacts", Detail: "polling, cancellation, logs, checksum verification"}, {Label: "Owned session", Detail: "restricted field console; no arbitrary host shell"}}},
			{ID: "activity", Kind: connectors.BlockProgress, Title: "Station activity", Progress: min(len(o.jobs)*10, 100), Detail: fmt.Sprintf("%d jobs observed", len(o.jobs))},
		}}, nil
	case "observations":
		rows := make([][]string, 0, len(o.jobs))
		ids := make([]string, 0, len(o.jobs))
		for jobID := range o.jobs {
			ids = append(ids, jobID)
		}
		sort.Strings(ids)
		for _, jobID := range ids {
			job := o.jobs[jobID].job
			rows = append(rows, []string{job.ID, job.ActionID, job.Status, strconv.Itoa(job.Progress) + "%"})
		}
		items := make([]connectors.ListItem, 0, len(o.notes))
		for _, note := range o.notes {
			items = append(items, connectors.ListItem{Label: note})
		}
		return connectors.View{ID: id, Title: "Observations", Updated: time.Now().UTC().Format(time.RFC3339), Blocks: []connectors.Block{
			{ID: "jobs", Kind: connectors.BlockTable, Title: "Survey jobs", Columns: []connectors.Column{{ID: "id", Title: "ID"}, {ID: "action", Title: "Action"}, {ID: "status", Title: "Status"}, {ID: "progress", Title: "Progress"}}, Rows: rows},
			{ID: "notes", Kind: connectors.BlockList, Title: "Field notes", Items: items},
		}}, nil
	default:
		return connectors.View{}, extensionhost.ErrNotFound
	}
}

func (o *observatory) StartJob(_ context.Context, request connectors.StartJobRequest) (connectors.Job, error) {
	jobID := fmt.Sprintf("job-%06d", o.sequence.Add(1))
	switch request.ActionID {
	case "record-note":
		note := strings.TrimSpace(request.Inputs["note"])
		if note == "" || len([]rune(note)) > 200 {
			return connectors.Job{}, errors.New("note must contain 1-200 characters")
		}
		o.mu.Lock()
		o.notes = append(o.notes, note)
		o.saveNotesLocked()
		job := connectors.Job{ID: jobID, ActionID: request.ActionID, Status: connectors.JobSucceeded, Progress: 100, StatusText: "recorded", Result: "Observation recorded"}
		o.jobs[jobID] = &surveyJob{job: job}
		o.mu.Unlock()
		return job, nil
	case "survey":
		samples, err := strconv.Atoi(request.Inputs["samples"])
		if err != nil || samples < 1 || samples > 20 {
			return connectors.Job{}, errors.New("samples must be from 1 to 20")
		}
		subject := strings.TrimSpace(request.Inputs["subject"])
		if subject == "" || len([]rune(subject)) > 80 {
			return connectors.Job{}, errors.New("subject must contain 1-80 characters")
		}
		detail := request.Inputs["detail"]
		if detail != "brief" && detail != "field" && detail != "deep" {
			return connectors.Job{}, errors.New("detail must be brief, field, or deep")
		}
		jobContext, cancel := context.WithCancel(context.Background())
		job := connectors.Job{ID: jobID, ActionID: request.ActionID, Status: connectors.JobQueued, Progress: 0, StatusText: "survey queued"}
		o.mu.Lock()
		o.jobs[jobID] = &surveyJob{job: job, cancel: cancel}
		o.mu.Unlock()
		go o.runSurvey(jobContext, jobID, subject, samples, detail, request.Inputs["environment"] == "true")
		return job, nil
	default:
		return connectors.Job{}, extensionhost.ErrNotFound
	}
}

func (o *observatory) runSurvey(ctx context.Context, jobID, subject string, samples int, detail string, includeEnvironment bool) {
	for sample := 1; sample <= samples; sample++ {
		select {
		case <-ctx.Done():
			o.mu.Lock()
			entry := o.jobs[jobID]
			entry.job.Status, entry.job.StatusText = connectors.JobCancelled, "survey cancelled"
			o.mu.Unlock()
			return
		case <-time.After(120 * time.Millisecond):
		}
		o.mu.Lock()
		entry := o.jobs[jobID]
		entry.job.Status = connectors.JobRunning
		entry.job.Progress = sample * 100 / samples
		entry.job.StatusText = fmt.Sprintf("sample %d of %d", sample, samples)
		entry.job.Logs = append(entry.job.Logs, fmt.Sprintf("sample %02d · %s · stable", sample, subject))
		o.mu.Unlock()
	}
	report := o.surveyReport(jobID, subject, samples, detail, includeEnvironment)
	sum := sha256.Sum256(report)
	artifact := connectors.Artifact{ID: "survey-report", Name: jobID + "-survey.md", MediaType: "text/markdown", Size: int64(len(report)), SHA256: hex.EncodeToString(sum[:])}
	o.mu.Lock()
	entry := o.jobs[jobID]
	entry.artifact = report
	entry.job.Status, entry.job.Progress, entry.job.StatusText = connectors.JobSucceeded, 100, "survey complete"
	entry.job.Result = fmt.Sprintf("Collected %d samples for %s", samples, subject)
	entry.job.Artifacts = []connectors.Artifact{artifact}
	o.mu.Unlock()
}

func (o *observatory) surveyReport(jobID, subject string, samples int, detail string, environment bool) []byte {
	lines := []string{"# Pardot Observatory Survey", "", "- Job: `" + jobID + "`", "- Region: " + o.region, "- Subject: " + subject, "- Samples: " + strconv.Itoa(samples), "- Detail: " + detail}
	if environment {
		lines = append(lines, "- State grant: "+yesNo(o.stateDir != ""), "- Shared grant: "+yesNo(o.sharedDir != ""))
	}
	lines = append(lines, "", "All sample readings were stable.", "")
	return []byte(strings.Join(lines, "\n"))
}

func (o *observatory) Job(_ context.Context, id string) (connectors.Job, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	entry, ok := o.jobs[id]
	if !ok {
		return connectors.Job{}, extensionhost.ErrNotFound
	}
	return cloneJob(entry.job), nil
}

func (o *observatory) CancelJob(_ context.Context, id string) (connectors.Job, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry, ok := o.jobs[id]
	if !ok {
		return connectors.Job{}, extensionhost.ErrNotFound
	}
	if entry.cancel != nil && !entry.job.Terminal() {
		entry.cancel()
		entry.job.Status, entry.job.StatusText = connectors.JobCancelled, "cancellation requested"
	}
	return cloneJob(entry.job), nil
}

func (o *observatory) Artifact(_ context.Context, jobID, artifactID string) (extensionhost.ArtifactPayload, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	entry, ok := o.jobs[jobID]
	if !ok || artifactID != "survey-report" || len(entry.job.Artifacts) != 1 {
		return extensionhost.ArtifactPayload{}, extensionhost.ErrNotFound
	}
	return extensionhost.ArtifactPayload{Metadata: entry.job.Artifacts[0], Data: append([]byte(nil), entry.artifact...)}, nil
}

func (o *observatory) OpenSession(_ context.Context, request connectors.OpenSessionRequest) (connectors.Session, error) {
	if request.SessionID != "field-console" {
		return connectors.Session{}, extensionhost.ErrNotFound
	}
	id := fmt.Sprintf("session-%06d", o.sequence.Add(1))
	session := connectors.Session{ID: id, SessionID: request.SessionID, Status: "open", Output: "PARDOT FIELD CONSOLE\nType help for permitted commands.\n", Prompt: "pardot> "}
	o.mu.Lock()
	o.sessions[id] = &fieldSession{session: session}
	o.mu.Unlock()
	return session, nil
}

func (o *observatory) SessionInput(_ context.Context, id string, request connectors.SessionInputRequest) (connectors.Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry, ok := o.sessions[id]
	if !ok {
		return connectors.Session{}, extensionhost.ErrNotFound
	}
	command := strings.TrimSpace(request.Input)
	response := "unknown command; type help"
	switch command {
	case "help":
		response = "commands: help, status, jobs, notes, clear"
	case "status":
		response = fmt.Sprintf("region=%s uptime=%s state=%s shared=%s", o.region, time.Since(o.startedAt).Round(time.Second), yesNo(o.stateDir != ""), yesNo(o.sharedDir != ""))
	case "jobs":
		response = fmt.Sprintf("jobs=%d", len(o.jobs))
	case "notes":
		response = strings.Join(o.notes, " | ")
		if response == "" {
			response = "no observations recorded"
		}
	case "clear":
		entry.session.Output = ""
		response = ""
	case "":
		response = ""
	}
	if response != "" {
		entry.session.Output += entry.session.Prompt + command + "\n" + response + "\n"
	}
	if len(entry.session.Output) > 32<<10 {
		entry.session.Output = entry.session.Output[len(entry.session.Output)-(32<<10):]
	}
	return entry.session, nil
}

func (o *observatory) ResizeSession(_ context.Context, id string, _ connectors.ResizeSessionRequest) (connectors.Session, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	entry, ok := o.sessions[id]
	if !ok {
		return connectors.Session{}, extensionhost.ErrNotFound
	}
	return entry.session, nil
}

func (o *observatory) CloseSession(_ context.Context, id string) (connectors.Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry, ok := o.sessions[id]
	if !ok {
		return connectors.Session{}, extensionhost.ErrNotFound
	}
	entry.session.Status = "closed"
	delete(o.sessions, id)
	return entry.session, nil
}

func (o *observatory) notesPath() string {
	if o.stateDir == "" {
		return ""
	}
	return filepath.Join(o.stateDir, "observations.txt")
}

func (o *observatory) loadNotes() {
	data, err := os.ReadFile(o.notesPath())
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				o.notes = append(o.notes, line)
			}
		}
	}
}

func (o *observatory) saveNotesLocked() {
	path := o.notesPath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(strings.Join(o.notes, "\n")+"\n"), 0o600)
}

func cloneJob(job connectors.Job) connectors.Job {
	job.Logs = append([]string(nil), job.Logs...)
	job.Artifacts = append([]connectors.Artifact(nil), job.Artifacts...)
	return job
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func min(first, second int) int {
	if first < second {
		return first
	}
	return second
}
