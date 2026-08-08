// Package connectors defines Lisan's language-neutral extension protocol.
// Extensions are out-of-process services; the core owns lifecycle, grants,
// transport validation, and rendering.
package connectors

const ProtocolVersion = 3

const (
	MaxViews         = 32
	MaxBlocksPerView = 64
	MaxActions       = 100
	MaxInputs        = 32
	MaxSessions      = 16
	MaxArtifacts     = 32
	MaxTableColumns  = 24
	MaxTableRows     = 1000
	MaxJobLogLines   = 4000
	MaxResponseBytes = 1 << 20
	MaxArtifactBytes = 32 << 20
)

const (
	BlockText     = "text"
	BlockKeyValue = "key-value"
	BlockTable    = "table"
	BlockList     = "list"
	BlockStatus   = "status"
	BlockProgress = "progress"
)

const (
	ToneNeutral = "neutral"
	ToneInfo    = "info"
	ToneSuccess = "success"
	ToneWarning = "warning"
	ToneDanger  = "danger"
)

const (
	InputText    = "text"
	InputNumber  = "number"
	InputBoolean = "boolean"
	InputSelect  = "select"
)

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

type Manifest struct {
	ProtocolVersion int                 `json:"protocol_version"`
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Version         string              `json:"version"`
	Icon            string              `json:"icon,omitempty"`
	Description     string              `json:"description,omitempty"`
	Views           []ViewDescriptor    `json:"views"`
	Actions         []ActionDescriptor  `json:"actions,omitempty"`
	Sessions        []SessionDescriptor `json:"sessions,omitempty"`
}

type ViewDescriptor struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type View struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Blocks  []Block `json:"blocks"`
	Updated string  `json:"updated,omitempty"`
}

// Block is deliberately semantic. Extensions never provide terminal escape
// sequences, coordinates, styles, or executable UI code.
type Block struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Title    string       `json:"title,omitempty"`
	Tone     string       `json:"tone,omitempty"`
	Text     string       `json:"text,omitempty"`
	Detail   string       `json:"detail,omitempty"`
	Progress int          `json:"progress,omitempty"`
	Fields   []FieldValue `json:"fields,omitempty"`
	Columns  []Column     `json:"columns,omitempty"`
	Rows     [][]string   `json:"rows,omitempty"`
	Items    []ListItem   `json:"items,omitempty"`
}

type FieldValue struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Column struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ListItem struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Tone   string `json:"tone,omitempty"`
}

type ActionDescriptor struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Inputs      []InputSpec `json:"inputs,omitempty"`
}

type InputSpec struct {
	ID          string        `json:"id"`
	Label       string        `json:"label"`
	Description string        `json:"description,omitempty"`
	Kind        string        `json:"kind"`
	Required    bool          `json:"required,omitempty"`
	Default     string        `json:"default,omitempty"`
	Pattern     string        `json:"pattern,omitempty"`
	Min         int64         `json:"min,omitempty"`
	Max         int64         `json:"max,omitempty"`
	Options     []InputOption `json:"options,omitempty"`
}

type InputOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type StartJobRequest struct {
	ActionID string            `json:"action_id"`
	Inputs   map[string]string `json:"inputs,omitempty"`
}

type Job struct {
	ID         string     `json:"id"`
	ActionID   string     `json:"action_id"`
	Status     string     `json:"status"`
	Progress   int        `json:"progress"`
	StatusText string     `json:"status_text,omitempty"`
	Logs       []string   `json:"logs,omitempty"`
	Result     string     `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
	ExitCode   int        `json:"exit_code,omitempty"`
	Artifacts  []Artifact `json:"artifacts,omitempty"`
}

func (job Job) Terminal() bool {
	return job.Status == JobSucceeded || job.Status == JobFailed || job.Status == JobCancelled
}

type Artifact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type SessionDescriptor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type OpenSessionRequest struct {
	SessionID string `json:"session_id"`
	Rows      int    `json:"rows"`
	Columns   int    `json:"columns"`
}

type SessionInputRequest struct {
	Input string `json:"input"`
}

type ResizeSessionRequest struct {
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
}

type Session struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
}
