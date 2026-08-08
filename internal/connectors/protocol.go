package connectors

const ProtocolVersion = 2

const (
	MaxPanels  = 32
	MaxTools   = 100
	MaxActions = 100
)

const (
	PanelTools        = "tools"
	PanelActions      = "actions"
	PanelSummary      = "summary"
	PanelActionOutput = "action-output"
)

type Manifest struct {
	ProtocolVersion int      `json:"protocol_version"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Icon            string   `json:"icon,omitempty"`
	Description     string   `json:"description,omitempty"`
	UI              UIConfig `json:"ui"`
	Tools           []Tool   `json:"tools,omitempty"`
	Actions         []Action `json:"actions,omitempty"`
}

// UIConfig is mandatory extension metadata that tells the cockpit which
// generic modules to place in its sidebar and main pane. Extension containers
// provide data and actions; the wrapper remains connector-agnostic.
type UIConfig struct {
	Sidebar []Panel `json:"sidebar"`
	Main    []Panel `json:"main"`
}

type Panel struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	Expanded bool   `json:"expanded,omitempty"`
}

type Tool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Ready       bool   `json:"ready"`
}

type Action struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type RunRequest struct {
	ActionID string `json:"action_id"`
}

type RunResponse struct {
	ActionID   string `json:"action_id"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
