package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/textsafe"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var protocolHTTPClient = &http.Client{
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("extension protocol redirects are not allowed")
	},
}

type State struct {
	Config   appconfig.ConnectorConfig
	Manifest Manifest
	Views    map[string]View
	Online   bool
	Error    string
}

func Scan(ctx context.Context, configured []appconfig.ConnectorConfig) []State {
	states := make([]State, 0, len(configured))
	for _, config := range configured {
		if config.Enabled {
			states = append(states, State{Config: config, Views: map[string]View{}})
		}
	}
	var wait sync.WaitGroup
	for index := range states {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			manifest, err := FetchManifest(ctx, states[index].Config.Endpoint)
			if err != nil {
				states[index].Error = err.Error()
				return
			}
			if manifest.ID != states[index].Config.ID {
				states[index].Error = fmt.Sprintf("extension manifest id %q does not match configured id %q", manifest.ID, states[index].Config.ID)
				return
			}
			states[index].Manifest = manifest
			for _, descriptor := range manifest.Views {
				view, fetchErr := FetchView(ctx, states[index].Config.Endpoint, descriptor.ID)
				if fetchErr != nil {
					states[index].Error = fetchErr.Error()
					return
				}
				states[index].Views[descriptor.ID] = view
			}
			states[index].Online = true
		}()
	}
	wait.Wait()
	return states
}

func FetchManifest(parent context.Context, endpoint string) (Manifest, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var manifest Manifest
	if err := getJSON(ctx, endpoint, "/v3/manifest", &manifest); err != nil {
		return Manifest{}, fmt.Errorf("extension manifest: %w", err)
	}
	sanitizeManifest(&manifest)
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func FetchView(parent context.Context, endpoint, viewID string) (View, error) {
	if !identifier.MatchString(viewID) {
		return View{}, errors.New("extension view id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var view View
	if err := getJSON(ctx, endpoint, "/v3/views/"+url.PathEscape(viewID), &view); err != nil {
		return View{}, fmt.Errorf("extension view %s: %w", viewID, err)
	}
	sanitizeView(&view)
	if err := ValidateView(view); err != nil {
		return View{}, err
	}
	if view.ID != viewID {
		return View{}, fmt.Errorf("extension view id %q does not match requested id %q", view.ID, viewID)
	}
	return view, nil
}

func StartJob(parent context.Context, endpoint string, request StartJobRequest) (Job, error) {
	if !identifier.MatchString(request.ActionID) {
		return Job{}, errors.New("extension action id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var job Job
	if err := sendJSON(ctx, http.MethodPost, endpoint, "/v3/jobs", request, &job); err != nil {
		return Job{}, fmt.Errorf("start extension job: %w", err)
	}
	sanitizeJob(&job)
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	if job.ActionID != request.ActionID {
		return Job{}, fmt.Errorf("extension job action %q does not match requested action %q", job.ActionID, request.ActionID)
	}
	return job, nil
}

func FetchJob(parent context.Context, endpoint, jobID string) (Job, error) {
	if !identifier.MatchString(jobID) {
		return Job{}, errors.New("extension job id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var job Job
	if err := getJSON(ctx, endpoint, "/v3/jobs/"+url.PathEscape(jobID), &job); err != nil {
		return Job{}, fmt.Errorf("fetch extension job: %w", err)
	}
	sanitizeJob(&job)
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	if job.ID != jobID {
		return Job{}, fmt.Errorf("extension job id %q does not match requested id %q", job.ID, jobID)
	}
	return job, nil
}

func CancelJob(parent context.Context, endpoint, jobID string) (Job, error) {
	if !identifier.MatchString(jobID) {
		return Job{}, errors.New("extension job id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var job Job
	if err := sendJSON(ctx, http.MethodDelete, endpoint, "/v3/jobs/"+url.PathEscape(jobID), nil, &job); err != nil {
		return Job{}, fmt.Errorf("cancel extension job: %w", err)
	}
	sanitizeJob(&job)
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	if job.ID != jobID {
		return Job{}, fmt.Errorf("extension job id %q does not match requested id %q", job.ID, jobID)
	}
	return job, nil
}

func FetchArtifact(parent context.Context, endpoint, jobID string, artifact Artifact) ([]byte, error) {
	if !identifier.MatchString(jobID) || !identifier.MatchString(artifact.ID) || artifact.Size < 0 || artifact.Size > MaxArtifactBytes || !sha256Pattern.MatchString(artifact.SHA256) {
		return nil, errors.New("extension artifact metadata is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	requestURL, err := connectorURL(endpoint, "/v3/jobs/"+url.PathEscape(jobID)+"/artifacts/"+url.PathEscape(artifact.ID))
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := protocolHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("extension artifact returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxArtifactBytes || int64(len(data)) != artifact.Size {
		return nil, errors.New("extension artifact size does not match metadata")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != artifact.SHA256 {
		return nil, errors.New("extension artifact checksum does not match metadata")
	}
	return data, nil
}

func OpenSession(parent context.Context, endpoint string, request OpenSessionRequest) (Session, error) {
	if !identifier.MatchString(request.SessionID) || request.Rows < 1 || request.Rows > 1000 || request.Columns < 1 || request.Columns > 1000 {
		return Session{}, errors.New("extension session id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var session Session
	if err := sendJSON(ctx, http.MethodPost, endpoint, "/v3/sessions", request, &session); err != nil {
		return Session{}, err
	}
	sanitizeSession(&session)
	if err := ValidateSession(session); err != nil {
		return Session{}, err
	}
	if session.SessionID != request.SessionID {
		return Session{}, fmt.Errorf("extension session type %q does not match requested type %q", session.SessionID, request.SessionID)
	}
	return session, nil
}

func SendSessionInput(parent context.Context, endpoint, id, input string) (Session, error) {
	if len(input) > 64<<10 {
		return Session{}, errors.New("extension session input exceeds 64 KiB")
	}
	return updateSession(parent, http.MethodPost, endpoint, id, "/input", SessionInputRequest{Input: input})
}

func ResizeSession(parent context.Context, endpoint, id string, rows, columns int) (Session, error) {
	if rows < 1 || rows > 1000 || columns < 1 || columns > 1000 {
		return Session{}, errors.New("extension session dimensions are invalid")
	}
	return updateSession(parent, http.MethodPost, endpoint, id, "/resize", ResizeSessionRequest{Rows: rows, Columns: columns})
}

func CloseSession(parent context.Context, endpoint, id string) (Session, error) {
	return updateSession(parent, http.MethodDelete, endpoint, id, "", nil)
}

func updateSession(parent context.Context, method, endpoint, id, suffix string, body any) (Session, error) {
	if !identifier.MatchString(id) {
		return Session{}, errors.New("extension session instance id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var session Session
	if err := sendJSON(ctx, method, endpoint, "/v3/sessions/"+url.PathEscape(id)+suffix, body, &session); err != nil {
		return Session{}, err
	}
	sanitizeSession(&session)
	if err := ValidateSession(session); err != nil {
		return Session{}, err
	}
	if session.ID != id {
		return Session{}, fmt.Errorf("extension session id %q does not match requested id %q", session.ID, id)
	}
	return session, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("extension protocol %d is unsupported (want %d)", manifest.ProtocolVersion, ProtocolVersion)
	}
	if !identifier.MatchString(manifest.ID) || strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
		return errors.New("extension manifest requires id, name, and version")
	}
	if len(manifest.Views) == 0 || len(manifest.Views) > MaxViews || len(manifest.Actions) > MaxActions || len(manifest.Sessions) > MaxSessions {
		return errors.New("extension manifest exceeds view, action, or session limits")
	}
	views, actions, sessions := map[string]bool{}, map[string]bool{}, map[string]bool{}
	defaults := 0
	for _, view := range manifest.Views {
		if !identifier.MatchString(view.ID) || view.Title == "" || views[view.ID] {
			return errors.New("extension views require unique valid ids and titles")
		}
		views[view.ID] = true
		if view.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return errors.New("extension manifest has more than one default view")
	}
	for _, action := range manifest.Actions {
		if !identifier.MatchString(action.ID) || action.Name == "" || actions[action.ID] || len(action.Inputs) > MaxInputs {
			return errors.New("extension actions require unique valid ids, names, and bounded inputs")
		}
		actions[action.ID] = true
		if err := validateInputs(action.Inputs); err != nil {
			return fmt.Errorf("action %s: %w", action.ID, err)
		}
	}
	for _, session := range manifest.Sessions {
		if !identifier.MatchString(session.ID) || session.Name == "" || sessions[session.ID] {
			return errors.New("extension sessions require unique valid ids and names")
		}
		sessions[session.ID] = true
	}
	return nil
}

func validateInputs(inputs []InputSpec) error {
	seen := map[string]bool{}
	for _, input := range inputs {
		if !identifier.MatchString(input.ID) || input.Label == "" || seen[input.ID] {
			return errors.New("inputs require unique valid ids and labels")
		}
		seen[input.ID] = true
		if input.Kind != InputText && input.Kind != InputNumber && input.Kind != InputBoolean && input.Kind != InputSelect {
			return fmt.Errorf("input %s has unsupported kind %q", input.ID, input.Kind)
		}
		if input.Kind == InputSelect && len(input.Options) == 0 {
			return fmt.Errorf("select input %s has no options", input.ID)
		}
		if len(input.Pattern) > 500 {
			return fmt.Errorf("input %s pattern is too long", input.ID)
		}
		if input.Pattern != "" {
			if _, err := regexp.Compile(input.Pattern); err != nil {
				return fmt.Errorf("input %s pattern is invalid", input.ID)
			}
		}
		if input.Kind == InputBoolean && input.Default != "" && input.Default != "true" && input.Default != "false" {
			return fmt.Errorf("boolean input %s has an invalid default", input.ID)
		}
		if input.Kind == InputSelect {
			options := map[string]bool{}
			defaultFound := input.Default == ""
			for _, option := range input.Options {
				if option.Value == "" || option.Label == "" || options[option.Value] {
					return fmt.Errorf("select input %s has invalid or duplicate options", input.ID)
				}
				options[option.Value] = true
				defaultFound = defaultFound || option.Value == input.Default
			}
			if !defaultFound {
				return fmt.Errorf("select input %s default is not an option", input.ID)
			}
		}
	}
	return nil
}

func ValidateView(view View) error {
	if !identifier.MatchString(view.ID) || view.Title == "" || len(view.Blocks) > MaxBlocksPerView {
		return errors.New("extension view requires an id, title, and bounded blocks")
	}
	seen := map[string]bool{}
	for _, block := range view.Blocks {
		if !identifier.MatchString(block.ID) || seen[block.ID] {
			return errors.New("extension view blocks require unique valid ids")
		}
		seen[block.ID] = true
		if block.Kind != BlockText && block.Kind != BlockKeyValue && block.Kind != BlockTable && block.Kind != BlockList && block.Kind != BlockStatus && block.Kind != BlockProgress {
			return fmt.Errorf("extension block %s has unsupported kind %q", block.ID, block.Kind)
		}
		if block.Tone != "" && block.Tone != ToneNeutral && block.Tone != ToneInfo && block.Tone != ToneSuccess && block.Tone != ToneWarning && block.Tone != ToneDanger {
			return fmt.Errorf("extension block %s has unsupported tone %q", block.ID, block.Tone)
		}
		if block.Progress < 0 || block.Progress > 100 || len(block.Columns) > MaxTableColumns || len(block.Rows) > MaxTableRows {
			return fmt.Errorf("extension block %s exceeds progress or table limits", block.ID)
		}
		for _, row := range block.Rows {
			if len(row) != len(block.Columns) {
				return fmt.Errorf("extension block %s has a table row with the wrong column count", block.ID)
			}
		}
	}
	return nil
}

func ValidateJob(job Job) error {
	if !identifier.MatchString(job.ID) || !identifier.MatchString(job.ActionID) || job.Progress < 0 || job.Progress > 100 || len(job.Logs) > MaxJobLogLines || len(job.Artifacts) > MaxArtifacts {
		return errors.New("extension job has invalid identity, progress, logs, or artifacts")
	}
	if job.Status != JobQueued && job.Status != JobRunning && job.Status != JobSucceeded && job.Status != JobFailed && job.Status != JobCancelled {
		return fmt.Errorf("extension job has unsupported status %q", job.Status)
	}
	seen := map[string]bool{}
	for _, artifact := range job.Artifacts {
		if !identifier.MatchString(artifact.ID) || artifact.Name == "" || artifact.Size < 0 || artifact.Size > MaxArtifactBytes || !sha256Pattern.MatchString(artifact.SHA256) || seen[artifact.ID] {
			return errors.New("extension job has invalid artifact metadata")
		}
		seen[artifact.ID] = true
	}
	return nil
}

func ValidateSession(session Session) error {
	if !identifier.MatchString(session.ID) || !identifier.MatchString(session.SessionID) {
		return errors.New("extension session has invalid identity")
	}
	if session.Status != "open" && session.Status != "closed" && session.Status != "failed" {
		return fmt.Errorf("extension session has unsupported status %q", session.Status)
	}
	return nil
}

func connectorURL(endpoint, requestPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("extension endpoint must be an http(s) URL without credentials")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = path.Clean(requestPath), "", "", ""
	return parsed.String(), nil
}

func getJSON(ctx context.Context, endpoint, requestPath string, destination any) error {
	return sendJSON(ctx, http.MethodGet, endpoint, requestPath, nil, destination)
}

func sendJSON(ctx context.Context, method, endpoint, requestPath string, body, destination any) error {
	requestURL, err := connectorURL(endpoint, requestPath)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := protocolHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("returned %s: %s", response.Status, textsafe.Label(string(message), 500))
	}
	if destination == nil {
		return nil
	}
	return decodeLimited(response.Body, destination)
}

func decodeLimited(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, MaxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > MaxResponseBytes {
		return errors.New("extension response exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("extension response must contain one JSON value")
	}
	return nil
}

func sanitizeManifest(manifest *Manifest) {
	manifest.ID = textsafe.Label(manifest.ID, 80)
	manifest.Name = textsafe.Label(manifest.Name, 100)
	manifest.Version = textsafe.Label(manifest.Version, 40)
	manifest.Icon = textsafe.Icon(manifest.Icon, 8)
	manifest.Description = textsafe.Label(manifest.Description, 500)
	for i := range manifest.Views {
		manifest.Views[i].ID = textsafe.Label(manifest.Views[i].ID, 80)
		manifest.Views[i].Title = textsafe.Label(manifest.Views[i].Title, 100)
		manifest.Views[i].Description = textsafe.Label(manifest.Views[i].Description, 300)
	}
	for i := range manifest.Actions {
		action := &manifest.Actions[i]
		action.ID = textsafe.Label(action.ID, 80)
		action.Name = textsafe.Label(action.Name, 100)
		action.Description = textsafe.Label(action.Description, 300)
		for j := range action.Inputs {
			input := &action.Inputs[j]
			input.ID = textsafe.Label(input.ID, 80)
			input.Label = textsafe.Label(input.Label, 100)
			input.Description = textsafe.Label(input.Description, 300)
			input.Default = textsafe.Label(input.Default, 500)
			for k := range input.Options {
				input.Options[k].Value = textsafe.Label(input.Options[k].Value, 100)
				input.Options[k].Label = textsafe.Label(input.Options[k].Label, 100)
			}
		}
	}
	for i := range manifest.Sessions {
		manifest.Sessions[i].ID = textsafe.Label(manifest.Sessions[i].ID, 80)
		manifest.Sessions[i].Name = textsafe.Label(manifest.Sessions[i].Name, 100)
		manifest.Sessions[i].Description = textsafe.Label(manifest.Sessions[i].Description, 300)
	}
}

func sanitizeView(view *View) {
	view.ID = textsafe.Label(view.ID, 80)
	view.Title = textsafe.Label(view.Title, 100)
	view.Updated = textsafe.Label(view.Updated, 100)
	for i := range view.Blocks {
		block := &view.Blocks[i]
		block.ID = textsafe.Label(block.ID, 80)
		block.Title = textsafe.Label(block.Title, 100)
		block.Text = textsafe.Output(block.Text)
		block.Detail = textsafe.Output(block.Detail)
		for j := range block.Fields {
			block.Fields[j].Label = textsafe.Label(block.Fields[j].Label, 100)
			block.Fields[j].Value = textsafe.Label(block.Fields[j].Value, 500)
		}
		for j := range block.Columns {
			block.Columns[j].ID = textsafe.Label(block.Columns[j].ID, 80)
			block.Columns[j].Title = textsafe.Label(block.Columns[j].Title, 100)
		}
		for j := range block.Rows {
			for k := range block.Rows[j] {
				block.Rows[j][k] = textsafe.Label(block.Rows[j][k], 500)
			}
		}
		for j := range block.Items {
			block.Items[j].Label = textsafe.Label(block.Items[j].Label, 200)
			block.Items[j].Detail = textsafe.Label(block.Items[j].Detail, 500)
		}
	}
}

func sanitizeJob(job *Job) {
	job.ID = textsafe.Label(job.ID, 80)
	job.ActionID = textsafe.Label(job.ActionID, 80)
	job.StatusText = textsafe.Label(job.StatusText, 300)
	job.Result = textsafe.Output(job.Result)
	job.Error = textsafe.Label(job.Error, 500)
	for i := range job.Logs {
		job.Logs[i] = textsafe.Output(job.Logs[i])
	}
	for i := range job.Artifacts {
		job.Artifacts[i].ID = textsafe.Label(job.Artifacts[i].ID, 80)
		job.Artifacts[i].Name = textsafe.Label(job.Artifacts[i].Name, 200)
		job.Artifacts[i].MediaType = textsafe.Label(job.Artifacts[i].MediaType, 100)
		job.Artifacts[i].SHA256 = strings.ToLower(strings.TrimSpace(job.Artifacts[i].SHA256))
	}
}

func sanitizeSession(session *Session) {
	session.ID = textsafe.Label(session.ID, 80)
	session.SessionID = textsafe.Label(session.SessionID, 80)
	session.Output = textsafe.Output(session.Output)
	session.Prompt = textsafe.Label(session.Prompt, 100)
}
