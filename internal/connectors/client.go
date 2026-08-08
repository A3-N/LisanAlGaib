package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/textsafe"
)

const maxResponseBytes = 1 << 20

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type State struct {
	Config   appconfig.ConnectorConfig
	Manifest Manifest
	Online   bool
	Error    string
}

func Scan(ctx context.Context, configured []appconfig.ConnectorConfig) []State {
	states := make([]State, 0, len(configured))
	for _, config := range configured {
		if config.Enabled {
			states = append(states, State{Config: config})
		}
	}
	var wait sync.WaitGroup
	for index := range states {
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
			states[index].Online = true
		}()
	}
	wait.Wait()
	return states
}

func FetchManifest(parent context.Context, endpoint string) (Manifest, error) {
	ctx, cancel := context.WithTimeout(parent, 2500*time.Millisecond)
	defer cancel()
	requestURL, err := connectorURL(endpoint, "/v1/manifest")
	if err != nil {
		return Manifest{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("extension manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("extension manifest returned %s", response.Status)
	}
	var manifest Manifest
	if err := decodeLimited(response.Body, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode extension manifest: %w", err)
	}
	sanitizeManifest(&manifest)
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("extension protocol %d is unsupported (want %d)", manifest.ProtocolVersion, ProtocolVersion)
	}
	if !identifier.MatchString(manifest.ID) || strings.TrimSpace(manifest.Name) == "" {
		return errors.New("extension manifest requires id and name")
	}
	if len(manifest.UI.Sidebar) == 0 || len(manifest.UI.Main) == 0 {
		return errors.New("extension manifest requires ui.sidebar and ui.main panels")
	}
	if len(manifest.UI.Sidebar) > MaxPanels || len(manifest.UI.Main) > MaxPanels || len(manifest.Tools) > MaxTools || len(manifest.Actions) > MaxActions {
		return errors.New("extension manifest exceeds panel, tool, or action limits")
	}
	if err := validatePanels(manifest.UI.Sidebar, map[string]bool{PanelTools: true, PanelActions: true}, "sidebar"); err != nil {
		return err
	}
	if err := validatePanels(manifest.UI.Main, map[string]bool{PanelSummary: true, PanelActionOutput: true}, "main"); err != nil {
		return err
	}
	if err := validateTools(manifest.Tools); err != nil {
		return err
	}
	return validateActions(manifest.Actions)
}

func validatePanels(panels []Panel, allowed map[string]bool, area string) error {
	seen := map[string]bool{}
	for _, panel := range panels {
		if !identifier.MatchString(panel.ID) || strings.TrimSpace(panel.Title) == "" || !allowed[panel.Kind] {
			return fmt.Errorf("extension ui %s panel requires a unique id, title, and supported kind", area)
		}
		if seen[panel.ID] {
			return fmt.Errorf("extension ui %s panel id %q is duplicated", area, panel.ID)
		}
		seen[panel.ID] = true
	}
	return nil
}

func validateTools(tools []Tool) error {
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if !identifier.MatchString(tool.ID) || strings.TrimSpace(tool.Name) == "" || seen[tool.ID] {
			return errors.New("extension tools require unique valid ids and names")
		}
		seen[tool.ID] = true
	}
	return nil
}

func validateActions(actions []Action) error {
	seen := make(map[string]bool, len(actions))
	for _, action := range actions {
		if !identifier.MatchString(action.ID) || strings.TrimSpace(action.Name) == "" || seen[action.ID] {
			return errors.New("extension actions require unique valid ids and names")
		}
		seen[action.ID] = true
	}
	return nil
}

func RunAction(parent context.Context, endpoint, actionID string) (RunResponse, error) {
	if !identifier.MatchString(actionID) {
		return RunResponse{}, errors.New("extension action id is invalid")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	requestURL, err := connectorURL(endpoint, "/v1/run")
	if err != nil {
		return RunResponse{}, err
	}
	payload, _ := json.Marshal(RunRequest{ActionID: actionID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return RunResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return RunResponse{}, fmt.Errorf("run extension action: %w", err)
	}
	defer response.Body.Close()
	var result RunResponse
	if err := decodeLimited(response.Body, &result); err != nil {
		return RunResponse{}, fmt.Errorf("decode extension action: %w", err)
	}
	result.ActionID = actionID
	result.Output = textsafe.Output(result.Output)
	result.Error = textsafe.Label(result.Error, 500)
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("extension action returned %s: %s", response.Status, result.Error)
	}
	return result, nil
}

func connectorURL(endpoint, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("extension endpoint must be an http(s) URL without credentials")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = path, "", "", ""
	return parsed.String(), nil
}

func decodeLimited(reader io.Reader, destination any) error {
	limited := io.LimitReader(reader, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return errors.New("extension response exceeds 1 MiB")
	}
	return json.Unmarshal(data, destination)
}

func sanitizeManifest(manifest *Manifest) {
	manifest.ID = textsafe.Label(manifest.ID, 80)
	manifest.Name = textsafe.Label(manifest.Name, 80)
	manifest.Icon = textsafe.Icon(manifest.Icon, 8)
	manifest.Description = textsafe.Label(manifest.Description, 300)
	for index := range manifest.UI.Sidebar {
		sanitizePanel(&manifest.UI.Sidebar[index])
	}
	for index := range manifest.UI.Main {
		sanitizePanel(&manifest.UI.Main[index])
	}
	for index := range manifest.Tools {
		manifest.Tools[index].ID = textsafe.Label(manifest.Tools[index].ID, 80)
		manifest.Tools[index].Name = textsafe.Label(manifest.Tools[index].Name, 100)
		manifest.Tools[index].Description = textsafe.Label(manifest.Tools[index].Description, 300)
		manifest.Tools[index].Version = textsafe.Label(manifest.Tools[index].Version, 160)
	}
	for index := range manifest.Actions {
		manifest.Actions[index].ID = textsafe.Label(manifest.Actions[index].ID, 80)
		manifest.Actions[index].Name = textsafe.Label(manifest.Actions[index].Name, 100)
		manifest.Actions[index].Description = textsafe.Label(manifest.Actions[index].Description, 300)
	}
}

func sanitizePanel(panel *Panel) {
	panel.ID = textsafe.Label(panel.ID, 80)
	panel.Title = textsafe.Label(panel.Title, 100)
	panel.Kind = textsafe.Label(panel.Kind, 80)
}
