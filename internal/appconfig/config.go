package appconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"lisanalgaib/internal/safefile"
	"lisanalgaib/internal/textsafe"
)

const (
	SchemaVersion      = 1
	MaxConnectors      = 16
	EnvironmentProfile = "LISAN_PROFILE_B64"
	EnvironmentConfig  = "LISAN_CONFIG"
)

var configIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var extensionTmpfsSize = regexp.MustCompile(`^size=[1-9][0-9]*[kKmMgG]?$`)
var extensionEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var extensionImageArgument = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ExtensionControlNetworkName gives each managed extension its own private
// internal network. The workspace joins each network as needed, but extensions
// never share a broadcast/DNS domain with one another.
func ExtensionControlNetworkName(id string) string {
	return "lisan-extension-control-" + id
}

// ValidExtensionContainerUser requires an explicit non-root numeric identity.
// User names are image-defined and therefore cannot prove that an extension is
// non-root before its image starts.
func ValidExtensionContainerUser(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		parsed, err := strconv.ParseUint(part, 10, 31)
		if err != nil || parsed == 0 {
			return false
		}
	}
	return true
}

// ValidateExtensionImageArgument rejects values that Docker could parse as a
// run option, as well as whitespace and control characters. Docker remains the
// authority for the complete image-reference grammar.
func ValidateExtensionImageArgument(value string) error {
	if value == "" || len(value) > 255 || strings.TrimSpace(value) != value || !extensionImageArgument.MatchString(value) {
		return fmt.Errorf("invalid or unsafe extension image reference")
	}
	return nil
}

// ValidateExtensionEnvironment prevents malformed entries and reserves the
// LISAN_EXTENSION_ namespace for lifecycle paths and identity supplied by the
// core after all bundle-defined values.
func ValidateExtensionEnvironment(value string) error {
	name, _, ok := strings.Cut(value, "=")
	if !ok || !extensionEnvironmentName.MatchString(name) || strings.HasPrefix(name, "LISAN_EXTENSION_") || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid or reserved extension environment entry")
	}
	return nil
}

// ValidateExtensionTmpfs accepts only bounded, non-executable Linux tmpfs
// mounts. It deliberately rejects kernel/device paths and the default /tmp
// mount, which Lisan owns itself.
func ValidateExtensionTmpfs(value string) error {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "/") || path.Clean(parts[0]) != parts[0] {
		return fmt.Errorf("tmpfs %q must use a clean absolute target and explicit options", value)
	}
	target := parts[0]
	for _, forbidden := range []string{"/", "/tmp", "/proc", "/sys", "/dev"} {
		if target == forbidden || strings.HasPrefix(target, forbidden+"/") {
			return fmt.Errorf("tmpfs target %q is reserved", target)
		}
	}
	required := map[string]bool{"rw": false, "noexec": false, "nosuid": false, "nodev": false, "size": false}
	seen := map[string]bool{}
	for _, option := range strings.Split(parts[1], ",") {
		if seen[option] {
			return fmt.Errorf("tmpfs %q repeats option %q", value, option)
		}
		seen[option] = true
		switch {
		case option == "rw", option == "noexec", option == "nosuid", option == "nodev":
			required[option] = true
		case extensionTmpfsSize.MatchString(option):
			required["size"] = true
		default:
			return fmt.Errorf("tmpfs %q has unsafe or unsupported option %q", value, option)
		}
	}
	for option, present := range required {
		if !present {
			return fmt.Errorf("tmpfs %q requires %s", value, option)
		}
	}
	return nil
}

type Category string

const (
	Features Category = "features"
	Tools    Category = "tools"
	Agents   Category = "agents"
)

type Option struct {
	Category    Category
	ID          string
	Label       string
	Description string
}

var Options = []Option{
	{Features, "files", "Files + NvChad", "Embedded editor and workspace picker"},
	{Features, "tools", "Tool inventory", "Dynamic configured-tool and package inventory"},
	{Features, "agents", "Mentats", "Coding-agent pages; individual mentats are selected below"},
	{Features, "terminal", "Embedded terminal", "Interactive shell page inside Lisan"},
	{Tools, "git", "Git", "Version control"},
	{Tools, "rg", "ripgrep", "Fast file and text search"},
	{Tools, "nvim", "Neovim", "Editor runtime"},
	{Tools, "nvchad", "NvChad", "Neovim distribution and dashboard"},
	{Tools, "node", "Node.js + npm", "JavaScript runtime and package manager"},
	{Tools, "go", "Go", "Go compiler and toolchain"},
	{Tools, "python", "Python", "Python 3 runtime and virtual environments"},
	{Tools, "pip", "pip", "Python package manager"},
	{Tools, "rust", "Rust + Cargo", "Rust compiler and package manager"},
	{Tools, "java", "Java JDK", "Java compiler and runtime"},
	{Tools, "clang", "C/C++ (Clang)", "C and C++ compiler toolchain"},
	{Tools, "ruby", "Ruby", "Ruby runtime and development tools"},
	{Tools, "php", "PHP", "PHP command-line runtime"},
	{Tools, "lua", "Lua", "Lua runtime"},
	{Tools, "curl", "curl", "HTTP and data-transfer client"},
	{Tools, "jq", "jq", "JSON query and transformation tool"},
	{Tools, "wget", "wget", "Non-interactive network downloader"},
	{Tools, "zip", "zip", "ZIP archive creator"},
	{Tools, "fd", "fd", "Fast filesystem search"},
	{Tools, "fzf", "fzf", "Fuzzy finder"},
	{Tools, "bat", "bat", "Syntax-highlighted file viewer"},
	{Tools, "tree", "tree", "Directory tree viewer"},
	{Tools, "shellcheck", "ShellCheck", "Shell script static analysis"},
	{Tools, "ip", "IProute2", "Modern IP and socket inspection (ip and ss)"},
	{Tools, "ping", "Ping", "ICMP reachability checks"},
	{Tools, "dns", "DNS utilities", "DNS lookup and diagnostics"},
	{Tools, "net-tools", "Net-tools", "Legacy interface and connection inspection"},
	{Tools, "traceroute", "Traceroute", "Network path tracing"},
	{Tools, "netcat", "Netcat", "TCP and UDP connection diagnostics"},
	{Tools, "nmap", "Nmap", "Network discovery and port scanning"},
	{Tools, "mtr", "MTR", "Continuous route and latency diagnostics"},
	{Tools, "tcpdump", "tcpdump", "Packet capture and inspection"},
	{Tools, "whois", "Whois", "Domain and network registration lookup"},
	{Agents, "codex", "Codex", "OpenAI coding agent CLI"},
	{Agents, "opencode", "OpenCode", "Provider-flexible coding agent CLI"},
	{Agents, "claude", "Claude Code", "Anthropic coding agent CLI"},
	{Agents, "kimi", "Kimi Code", "Moonshot coding agent CLI"},
	{Agents, "omp", "Oh My Pi", "Coding-first, provider-flexible agent CLI"},
}

// AgentIDs returns the selectable Mentat IDs in their configured display
// order. Runtime, installer, and UI code use this catalog so adding an agent
// does not require maintaining parallel identity lists.
func AgentIDs() []string {
	var ids []string
	for _, option := range Options {
		if option.Category == Agents {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

type Profile struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Preset     string            `json:"preset,omitempty"`
	Revision   int               `json:"revision"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Features   map[string]bool   `json:"features"`
	Tools      map[string]bool   `json:"tools"`
	Agents     map[string]bool   `json:"agents"`
	Terminal   TerminalConfig    `json:"terminal"`
	Connectors []ConnectorConfig `json:"connectors,omitempty"`
}

type TerminalConfig struct {
	DockerShell   string `json:"docker_shell"`
	DockerUser    string `json:"docker_user"`
	DockerWorkdir string `json:"docker_workdir"`
	legacyRuntime bool
}

// UnmarshalJSON accepts the removed outer and host-shell settings so profiles
// written by older releases continue to load. They are intentionally ignored:
// every mode stays in the invoking terminal, host mode uses the host's default
// shell, and only Docker's embedded shell is configurable.
func (config *TerminalConfig) UnmarshalJSON(data []byte) error {
	type terminalWire struct {
		DockerShell   string `json:"docker_shell"`
		DockerUser    string `json:"docker_user"`
		DockerWorkdir string `json:"docker_workdir"`
		Outer         string `json:"outer"`
		HostShell     string `json:"host_shell"`
	}
	var wire terminalWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	*config = TerminalConfig{
		DockerShell:   wire.DockerShell,
		DockerUser:    wire.DockerUser,
		DockerWorkdir: wire.DockerWorkdir,
		legacyRuntime: wire.Outer != "" || wire.HostShell != "",
	}
	return nil
}

// ConnectorConfig is resolved from a discovered lifecycle bundle. Docker mode
// uses its image and grants; vm mode launches its platform executable. The TUI
// knows only the shared protocol advertised by either runtime.
type ConnectorConfig struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Icon             string          `json:"icon,omitempty"`
	Description      string          `json:"description,omitempty"`
	Enabled          bool            `json:"enabled"`
	Managed          bool            `json:"managed"`
	External         bool            `json:"external,omitempty"`
	Bundle           string          `json:"bundle,omitempty"`
	Version          string          `json:"version,omitempty"`
	Image            string          `json:"image,omitempty"`
	BuildContext     string          `json:"build_context,omitempty"`
	Dockerfile       string          `json:"dockerfile,omitempty"`
	NativeExecutable string          `json:"native_executable,omitempty"`
	NativePackage    string          `json:"native_package,omitempty"`
	NativeArguments  []string        `json:"native_arguments,omitempty"`
	Container        string          `json:"container"`
	User             string          `json:"user,omitempty"`
	Network          string          `json:"network"`
	Endpoint         string          `json:"endpoint"`
	Tmpfs            []string        `json:"tmpfs,omitempty"`
	Environment      []string        `json:"environment,omitempty"`
	Requests         ExtensionGrants `json:"requests,omitempty"`
	Grants           ExtensionGrants `json:"grants,omitempty"`
}

// UnmarshalJSON accepts the removed declarative native_config key only as a
// one-way config migration. No protocol-v2 host or runtime is retained.
func (connector *ConnectorConfig) UnmarshalJSON(data []byte) error {
	type ConnectorWire ConnectorConfig
	var wire struct {
		ConnectorWire
		NativeConfig string `json:"native_config,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	*connector = ConnectorConfig(wire.ConnectorWire)
	return nil
}

type ExtensionGrants struct {
	Internet        bool `json:"internet,omitempty"`
	PersistentState bool `json:"persistent_state,omitempty"`
	SharedRead      bool `json:"shared_read,omitempty"`
	SharedWrite     bool `json:"shared_write,omitempty"`
}

type Document struct {
	SchemaVersion   int       `json:"schema_version"`
	NextRevision    int       `json:"next_revision"`
	ActiveProfileID string    `json:"active_profile_id"`
	RuntimeRoot     string    `json:"runtime_root,omitempty"`
	Profiles        []Profile `json:"profiles"`
}

type Preset struct {
	ID          string
	Name        string
	Description string
	Enabled     map[string]bool
}

var Presets = []Preset{
	preset("full", "Golden Path", "Every page and agent with a lean Python and network toolset",
		"files", "tools", "agents", "terminal",
		"git", "rg", "nvim", "nvchad", "python", "pip", "curl", "zip",
		"ip", "ping", "dns", "net-tools", "traceroute", "netcat",
		"codex", "opencode", "claude", "kimi", "omp"),
	preset("core", "Mentat", "Core editor with NvChad, Git, search, and inventory",
		"files", "tools", "git", "rg", "nvim", "nvchad"),
	preset("agent-lab", "Landsraad", "Editor, terminal, and every coding-agent wrapper",
		"files", "agents", "terminal", "git", "rg", "nvim", "nvchad", "node",
		"codex", "opencode", "claude", "kimi", "omp"),
	preset("minimal", "Muad'Dib", "Minimal overview only; no child process is launched"),
}

func preset(id, name, description string, enabled ...string) Preset {
	return Preset{ID: id, Name: name, Description: description, Enabled: enabledIDs(enabled...)}
}

func enabledIDs(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, id := range values {
		result[id] = true
	}
	return result
}

func ConfigPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvironmentConfig)); override != "" {
		return filepath.Abs(override)
	}
	if runtime.GOOS == "windows" {
		if root := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); root != "" {
			return filepath.Join(root, "lisanalgaib", "config.json"), nil
		}
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(root, "lisanalgaib", "config.json"), nil
}

func DefaultDocument(now time.Time) Document {
	profile := ProfileFromPreset(Presets[0], now)
	// New installations expose bundled extensions in config but never start
	// them implicitly. Extensions remain a separate opt-in from every preset.
	for index := range profile.Connectors {
		profile.Connectors[index].Enabled = false
	}
	profile.ID = "golden-path"
	profile.Revision = 1
	return Document{
		SchemaVersion:   SchemaVersion,
		NextRevision:    2,
		ActiveProfileID: profile.ID,
		Profiles:        []Profile{profile},
	}
}

func ProfileFromPreset(preset Preset, now time.Time) Profile {
	profile := Profile{
		Name:       preset.Name,
		Preset:     preset.ID,
		CreatedAt:  now,
		UpdatedAt:  now,
		Features:   map[string]bool{},
		Tools:      map[string]bool{},
		Agents:     map[string]bool{},
		Terminal:   defaultTerminal(),
		Connectors: nil,
	}
	for _, option := range Options {
		profile.Set(option.Category, option.ID, preset.Enabled[option.ID])
	}
	return profile
}

func (p Profile) Clone() Profile {
	clone := p
	clone.Features = cloneMap(p.Features)
	clone.Tools = cloneMap(p.Tools)
	clone.Agents = cloneMap(p.Agents)
	clone.Connectors = append([]ConnectorConfig(nil), p.Connectors...)
	for index := range clone.Connectors {
		clone.Connectors[index].Tmpfs = append([]string(nil), p.Connectors[index].Tmpfs...)
		clone.Connectors[index].Environment = append([]string(nil), p.Connectors[index].Environment...)
		clone.Connectors[index].NativeArguments = append([]string(nil), p.Connectors[index].NativeArguments...)
	}
	return clone
}

func cloneMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (p Profile) Enabled(category Category, id string) bool {
	switch category {
	case Features:
		return p.Features[id]
	case Tools:
		return p.Tools[id]
	case Agents:
		return p.Agents[id]
	default:
		return false
	}
}

func (p *Profile) Set(category Category, id string, enabled bool) {
	var values *map[string]bool
	switch category {
	case Features:
		values = &p.Features
	case Tools:
		values = &p.Tools
	case Agents:
		values = &p.Agents
	default:
		return
	}
	if *values == nil {
		*values = map[string]bool{}
	}
	(*values)[id] = enabled
}

func (p Profile) Feature(id string) bool { return p.Enabled(Features, id) }
func (p Profile) Tool(id string) bool    { return p.Enabled(Tools, id) }
func (p Profile) Agent(id string) bool   { return p.Enabled(Agents, id) }

func (p Profile) Signature() string {
	var selected []string
	for _, option := range Options {
		if p.Enabled(option.Category, option.ID) {
			selected = append(selected, string(option.Category)+":"+option.ID)
		}
	}
	sort.Strings(selected)
	selected = append(selected,
		"terminal:docker-shell="+p.Terminal.DockerShell,
		"terminal:docker-user="+p.Terminal.DockerUser,
		"terminal:docker-workdir="+p.Terminal.DockerWorkdir,
	)
	connectors := append([]ConnectorConfig(nil), p.Connectors...)
	sort.Slice(connectors, func(i, j int) bool { return connectors[i].ID < connectors[j].ID })
	connectorJSON, _ := json.Marshal(connectors)
	selected = append(selected, "connectors:"+string(connectorJSON))
	sum := sha256.Sum256([]byte(strings.Join(selected, "\n")))
	return fmt.Sprintf("%x", sum[:8])
}

func (d Document) Active() (Profile, bool) {
	for _, profile := range d.Profiles {
		if profile.ID == d.ActiveProfileID {
			return profile.Clone(), true
		}
	}
	return Profile{}, false
}

func (d *Document) Activate(id string) bool {
	for index := range d.Profiles {
		if d.Profiles[index].ID == id {
			d.ActiveProfileID = id
			return true
		}
	}
	return false
}

// SaveSelection activates an existing identical profile or appends a new
// revision. This preserves every distinct checkbox combination for config.
func (d *Document) SaveSelection(selection Profile, now time.Time) Profile {
	normalizeProfile(&selection)
	signature := selection.Signature()
	for index := range d.Profiles {
		if d.Profiles[index].Signature() == signature {
			d.ActiveProfileID = d.Profiles[index].ID
			return d.Profiles[index].Clone()
		}
	}
	if d.NextRevision < 1 {
		d.NextRevision = 1
		for _, profile := range d.Profiles {
			d.NextRevision = max(d.NextRevision, profile.Revision+1)
		}
	}
	selection.Revision = d.NextRevision
	d.NextRevision++
	selection.ID = fmt.Sprintf("profile-%03d-%s", selection.Revision, signature[:6])
	selection.Name = fmt.Sprintf("Custom %03d", selection.Revision)
	selection.Preset = ""
	selection.CreatedAt = now
	selection.UpdatedAt = now
	d.Profiles = append(d.Profiles, selection.Clone())
	d.ActiveProfileID = selection.ID
	return selection
}

func Load(path string) (Document, error) {
	data, err := safefile.Read(path, 4<<20)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultDocument(time.Now()), nil
	}
	if err != nil {
		return Document{}, fmt.Errorf("read config: %w", err)
	}
	var document Document
	if err := decodeJSON(data, &document); err != nil {
		return Document{}, fmt.Errorf("decode config: %w", err)
	}
	if err := normalizeDocument(&document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func Save(path string, document Document) error {
	if err := normalizeDocument(&document); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := safefile.Write(path, data, 0o700, 0o600); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func LoadActive() (Document, Profile, string, error) {
	path, err := ConfigPath()
	if err != nil {
		return Document{}, Profile{}, "", err
	}
	document, err := Load(path)
	if err != nil {
		return Document{}, Profile{}, path, err
	}
	if encoded := strings.TrimSpace(os.Getenv(EnvironmentProfile)); encoded != "" {
		profile, decodeErr := DecodeProfile(encoded)
		if decodeErr != nil {
			return Document{}, Profile{}, path, decodeErr
		}
		return document, profile, path, nil
	}
	profile, ok := document.Active()
	if !ok {
		return Document{}, Profile{}, path, errors.New("config has no active profile")
	}
	return document, profile, path, nil
}

func EncodeProfile(profile Profile) (string, error) {
	profile, err := NormalizeProfile(profile)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("encode active profile: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// NormalizeProfile returns a detached, validated profile suitable for a
// runtime launch. Callers can validate before creating processes or containers
// without mutating the saved configuration.
func NormalizeProfile(profile Profile) (Profile, error) {
	profile = profile.Clone()
	normalizeProfile(&profile)
	if err := validateProfile(profile, true); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func DecodeProfile(encoded string) (Profile, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Profile{}, fmt.Errorf("decode active profile: %w", err)
	}
	var profile Profile
	if err := decodeJSON(data, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode active profile: %w", err)
	}
	return NormalizeProfile(profile)
}

func normalizeDocument(document *Document) error {
	if document.SchemaVersion == 0 {
		document.SchemaVersion = SchemaVersion
	}
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported config schema %d (this build supports %d)", document.SchemaVersion, SchemaVersion)
	}
	if len(document.Profiles) == 0 {
		*document = DefaultDocument(time.Now())
		return nil
	}
	document.ActiveProfileID = strings.TrimSpace(document.ActiveProfileID)
	maximumRevision := 0
	for index := range document.Profiles {
		normalizeProfile(&document.Profiles[index])
		maximumRevision = max(maximumRevision, document.Profiles[index].Revision)
	}
	if document.NextRevision <= maximumRevision {
		document.NextRevision = maximumRevision + 1
	}
	if _, ok := document.Active(); !ok {
		document.ActiveProfileID = document.Profiles[len(document.Profiles)-1].ID
	}
	return validateDocument(*document)
}

func normalizeProfile(profile *Profile) {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = textsafe.Label(profile.Name, 100)
	profile.Preset = strings.TrimSpace(profile.Preset)
	legacyPresetNames := map[string]string{
		"Dune Full": "Golden Path", "Sietch Tabr // Full": "Golden Path",
		"Core Editor": "Mentat", "Mentat // Core Editor": "Mentat",
		"Agent Lab": "Landsraad", "Mentat Council // Agent Lab": "Landsraad",
		"Minimal": "Muad'Dib", "Desert Mouse // Minimal": "Muad'Dib",
	}
	if migrated, ok := legacyPresetNames[profile.Name]; ok {
		profile.Name = migrated
	}
	if profile.Features == nil {
		profile.Features = map[string]bool{}
	}
	// Skills are discovered and managed by each agent. Drop the removed wrapper
	// index toggle so profiles from earlier releases continue to load.
	delete(profile.Features, "skills")
	if profile.Tools == nil {
		profile.Tools = map[string]bool{}
	}
	// Shell and outer-terminal choices are runtime context now, not tool
	// toggles. Profiles carrying the retired terminal settings may also carry
	// retired runtime tool keys; discard only their unknown tool entries before
	// strict validation. Current profiles still reject unknown options.
	if profile.Terminal.legacyRuntime {
		for id := range profile.Tools {
			if !knownOption(Tools, id) {
				delete(profile.Tools, id)
			}
		}
	}
	if profile.Agents == nil {
		profile.Agents = map[string]bool{}
	}
	// Fill options introduced after a named preset was saved. The presence of
	// pip marks the first catalog that separated Python's package manager; older
	// Golden Path profiles also carried Go and Node as defaults, so migrate those
	// two choices once without rewriting later user selections during Save.
	if preset, ok := presetByID(profile.Preset); ok {
		_, currentToolCatalog := profile.Tools["pip"]
		for _, option := range Options {
			if !profile.optionConfigured(option.Category, option.ID) {
				profile.Set(option.Category, option.ID, preset.Enabled[option.ID])
			}
		}
		if preset.ID == "full" && !currentToolCatalog {
			profile.Set(Tools, "node", false)
			profile.Set(Tools, "go", false)
		}
	}
	if profile.Connectors == nil {
		profile.Connectors = []ConnectorConfig{}
	}
	// Retire bundled protocol-v2 examples during config normalization. This is
	// data cleanup only; none of their host, endpoints, or behavior survives.
	retained := profile.Connectors[:0]
	for _, connector := range profile.Connectors {
		switch connector.ID {
		case "ixian-proving-ground", "mobile-lab", "runtime-scout", "host-check", "guild-navigator":
			continue
		default:
			retained = append(retained, connector)
		}
	}
	profile.Connectors = retained
	for index := range profile.Connectors {
		normalizeConnector(&profile.Connectors[index])
	}
	defaults := defaultTerminal()
	profile.Terminal.DockerShell = strings.TrimSpace(profile.Terminal.DockerShell)
	profile.Terminal.DockerUser = strings.TrimSpace(profile.Terminal.DockerUser)
	profile.Terminal.DockerWorkdir = strings.TrimSpace(profile.Terminal.DockerWorkdir)
	if profile.Terminal.DockerUser == "dune" {
		profile.Terminal.DockerUser = "fremen"
	}
	if profile.Terminal.DockerWorkdir == "/home/dune" || strings.HasPrefix(profile.Terminal.DockerWorkdir, "/home/dune/") {
		profile.Terminal.DockerWorkdir = "/home/fremen" + strings.TrimPrefix(profile.Terminal.DockerWorkdir, "/home/dune")
	}
	if profile.Terminal.DockerShell == "" {
		profile.Terminal.DockerShell = defaults.DockerShell
	}
	if profile.Terminal.DockerUser == "" {
		profile.Terminal.DockerUser = defaults.DockerUser
	}
	if profile.Terminal.DockerWorkdir == "" {
		profile.Terminal.DockerWorkdir = defaults.DockerWorkdir
	}
}

func (p Profile) optionConfigured(category Category, id string) bool {
	var values map[string]bool
	switch category {
	case Features:
		values = p.Features
	case Tools:
		values = p.Tools
	case Agents:
		values = p.Agents
	}
	_, ok := values[id]
	return ok
}

func presetByID(id string) (Preset, bool) {
	for _, candidate := range Presets {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Preset{}, false
}

func defaultTerminal() TerminalConfig {
	return TerminalConfig{
		DockerShell:   "fish",
		DockerUser:    "fremen",
		DockerWorkdir: "/home/fremen",
	}
}

func normalizeConnector(connector *ConnectorConfig) {
	connector.ID = strings.TrimSpace(connector.ID)
	connector.Name = textsafe.Label(connector.Name, 100)
	connector.Icon = textsafe.Icon(connector.Icon, 8)
	connector.Description = textsafe.Label(connector.Description, 300)
	connector.Image = strings.TrimSpace(connector.Image)
	connector.BuildContext = strings.TrimSpace(connector.BuildContext)
	connector.Dockerfile = strings.TrimSpace(connector.Dockerfile)
	connector.Bundle = filepath.ToSlash(strings.TrimSpace(connector.Bundle))
	connector.Version = strings.TrimSpace(connector.Version)
	connector.NativeExecutable = filepath.ToSlash(strings.TrimSpace(connector.NativeExecutable))
	connector.NativePackage = filepath.ToSlash(strings.TrimSpace(connector.NativePackage))
	connector.Container = strings.TrimSpace(connector.Container)
	connector.User = strings.TrimSpace(connector.User)
	connector.Network = strings.TrimSpace(connector.Network)
	connector.Endpoint = strings.TrimSpace(connector.Endpoint)
	for index, value := range connector.Tmpfs {
		connector.Tmpfs[index] = migrateExtensionTmpfs(value)
	}
	if connector.Network == "lisan-al-gaib" || connector.Network == "lisan-sietch-net" {
		connector.Network = "arrakis-shield-wall"
	}
	if connector.Name == "" {
		connector.Name = connector.ID
	}
	if connector.Icon == "" {
		connector.Icon = "󰒍"
	}
	if connector.Container == "" {
		connector.Container = "lisan-" + connector.ID
	}
	if connector.Managed && connector.User == "" {
		connector.User = "10001:10001"
	}
	if connector.Managed {
		connector.Network = ExtensionControlNetworkName(connector.ID)
	}
	if connector.Network == "" {
		connector.Network = "arrakis-shield-wall"
	}
	if connector.Grants.SharedWrite {
		connector.Grants.SharedRead = true
	}
}

// migrateExtensionTmpfs upgrades values accepted before nodev became a
// mandatory sidecar mount option. Only an otherwise-valid legacy value is
// changed; malformed or custom unsafe values still fail normal validation.
func migrateExtensionTmpfs(value string) string {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return value
	}
	for _, option := range strings.Split(parts[1], ",") {
		if option == "nodev" {
			return value
		}
	}
	candidate := value + ",nodev"
	if ValidateExtensionTmpfs(candidate) == nil {
		return candidate
	}
	return value
}

func validateDocument(document Document) error {
	seen := make(map[string]bool, len(document.Profiles))
	for _, profile := range document.Profiles {
		if err := validateProfile(profile, true); err != nil {
			return err
		}
		if seen[profile.ID] {
			return fmt.Errorf("config profile id %q is duplicated", profile.ID)
		}
		seen[profile.ID] = true
	}
	return nil
}

func validateProfile(profile Profile, requireID bool) error {
	if requireID && !configIdentifier.MatchString(profile.ID) {
		return fmt.Errorf("config profile id %q is invalid", profile.ID)
	}
	if profile.Name == "" {
		return fmt.Errorf("config profile %q requires a name", profile.ID)
	}
	if !map[string]bool{"fish": true, "bash": true, "zsh": true, "sh": true}[profile.Terminal.DockerShell] {
		return fmt.Errorf("config profile %q has unsupported Docker shell %q", profile.ID, profile.Terminal.DockerShell)
	}
	if profile.Terminal.DockerUser != "fremen" && profile.Terminal.DockerUser != "root" {
		return fmt.Errorf("config profile %q Docker user must be fremen or root", profile.ID)
	}
	if !strings.HasPrefix(profile.Terminal.DockerWorkdir, "/") || strings.ContainsRune(profile.Terminal.DockerWorkdir, '\x00') {
		return fmt.Errorf("config profile %q Docker workdir must be an absolute container path", profile.ID)
	}
	for _, selected := range []struct {
		category Category
		values   map[string]bool
	}{
		{Features, profile.Features}, {Tools, profile.Tools}, {Agents, profile.Agents},
	} {
		for id := range selected.values {
			if !knownOption(selected.category, id) {
				return fmt.Errorf("config profile %q has unknown %s option %q", profile.ID, selected.category, id)
			}
		}
	}
	if len(profile.Connectors) > MaxConnectors {
		return fmt.Errorf("config profile %q exceeds the %d extension limit", profile.ID, MaxConnectors)
	}
	seen := make(map[string]bool, len(profile.Connectors))
	for _, connector := range profile.Connectors {
		if !configIdentifier.MatchString(connector.ID) {
			return fmt.Errorf("config profile %q has invalid extension id %q", profile.ID, connector.ID)
		}
		if seen[connector.ID] {
			return fmt.Errorf("config profile %q has duplicate extension id %q", profile.ID, connector.ID)
		}
		seen[connector.ID] = true
		if !connector.External && (!configIdentifier.MatchString(connector.Container) || !configIdentifier.MatchString(connector.Network)) {
			return fmt.Errorf("extension %q has an invalid container or network name", connector.ID)
		}
		if connector.Managed && connector.External {
			return fmt.Errorf("managed extension %q cannot use an external runtime", connector.ID)
		}
		if !connector.Managed && !connector.External {
			return fmt.Errorf("extension %q must use a managed bundle or external HTTP endpoint", connector.ID)
		}
		if connector.Managed && (connector.Image == "" || connector.NativeExecutable == "") {
			return fmt.Errorf("managed extension %q requires image and native_executable", connector.ID)
		}
		if connector.Managed {
			if err := ValidateExtensionImageArgument(connector.Image); err != nil {
				return fmt.Errorf("managed extension %q has an invalid image reference", connector.ID)
			}
		}
		if connector.Managed && !ValidExtensionContainerUser(connector.User) {
			return fmt.Errorf("managed extension %q has invalid container user %q", connector.ID, connector.User)
		}
		if connector.Managed && connector.Network != ExtensionControlNetworkName(connector.ID) {
			return fmt.Errorf("managed extension %q must use its dedicated control network", connector.ID)
		}
		for _, tmpfs := range connector.Tmpfs {
			if err := ValidateExtensionTmpfs(tmpfs); err != nil {
				return fmt.Errorf("managed extension %q: %w", connector.ID, err)
			}
		}
		for _, value := range connector.Environment {
			if err := ValidateExtensionEnvironment(value); err != nil {
				return fmt.Errorf("managed extension %q has invalid or reserved environment entry", connector.ID)
			}
		}
		if connector.Grants.SharedWrite && !connector.Grants.SharedRead {
			return fmt.Errorf("managed extension %q cannot grant shared_write without shared_read", connector.ID)
		}
		if connector.Managed && !grantsWithinRequests(connector.Grants, connector.Requests) {
			return fmt.Errorf("managed extension %q grants a capability it did not request", connector.ID)
		}
		if connector.Enabled {
			parsed, err := url.Parse(connector.Endpoint)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return fmt.Errorf("extension %q endpoint must be an http(s) URL without credentials", connector.ID)
			}
		}
	}
	return nil
}

func grantsWithinRequests(granted, requested ExtensionGrants) bool {
	return (!granted.Internet || requested.Internet) &&
		(!granted.PersistentState || requested.PersistentState) &&
		(!granted.SharedRead || requested.SharedRead) &&
		(!granted.SharedWrite || requested.SharedWrite)
}

func knownOption(category Category, id string) bool {
	for _, option := range Options {
		if option.Category == category && option.ID == id {
			return true
		}
	}
	return false
}

func decodeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
