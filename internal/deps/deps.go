package deps

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/childproc"
)

type requirement struct {
	ID      string
	Command string
}

type installCommand struct {
	Name string
	Args []string
}

func required(profile appconfig.Profile) []requirement {
	byCommand := map[string]requirement{}
	add := func(id, command string) {
		if command != "" {
			byCommand[command] = requirement{ID: id, Command: command}
		}
	}
	needsNvChad := profile.Feature("files") || profile.Tool("nvchad")
	if needsNvChad {
		add("nvim", "nvim")
		add("git", "git")
		add("rg", "rg")
		if runtime.GOOS == "linux" {
			add("fd", "fdfind")
		} else {
			add("fd", "fd")
		}
		if runtime.GOOS != "windows" {
			add("unzip", "unzip")
		}
	}
	if profile.Feature("terminal") {
		if os.Getenv("LISAN_CONTAINER") == "1" {
			add(profile.Terminal.DockerShell, profile.Terminal.DockerShell)
		}
	}
	for _, pair := range []struct{ id, command string }{
		{"git", "git"}, {"rg", "rg"}, {"nvim", "nvim"},
		{"node", "node"}, {"go", "go"}, {"python", "python3"},
	} {
		if profile.Tool(pair.id) {
			add(pair.id, pair.command)
		}
	}
	if profile.Tool("node") {
		add("npm", "npm")
	}
	if profile.Feature("agents") {
		for _, id := range []string{"codex", "opencode", "claude", "kimi"} {
			if profile.Agent(id) {
				add(id, id)
			}
		}
		if profile.Agent("codex") || profile.Agent("opencode") {
			add("npm", "npm")
		}
		if runtime.GOOS != "windows" && (profile.Agent("claude") || profile.Agent("kimi")) {
			add("curl", "curl")
		}
	}
	result := make([]requirement, 0, len(byCommand))
	for _, requirement := range byCommand {
		result = append(result, requirement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func missing(requirements []requirement) []requirement {
	var missing []requirement
	for _, requirement := range requirements {
		if requirement.Command == "@docker-compose" {
			if exec.Command("docker", "compose", "version").Run() != nil {
				missing = append(missing, requirement)
			}
			continue
		}
		if _, err := exec.LookPath(requirement.Command); err != nil {
			missing = append(missing, requirement)
		}
	}
	return missing
}

func installPlan(goos string, missing []requirement) ([]installCommand, error) {
	packages := map[string]bool{}
	agents := map[string]bool{}
	for _, requirement := range missing {
		if isAgent(requirement.ID) {
			agents[requirement.ID] = true
			continue
		}
		for _, pkg := range packageNames(goos, requirement.ID) {
			packages[pkg] = true
		}
	}
	var names []string
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)
	var commands []installCommand
	switch goos {
	case "linux":
		if len(names) > 0 {
			commands = append(commands,
				installCommand{Name: "sudo", Args: []string{"apt-get", "update"}},
				installCommand{Name: "sudo", Args: append([]string{"apt-get", "install", "-y", "--no-install-recommends"}, names...)},
			)
		}
	case "darwin":
		var formulae, casks []string
		for _, name := range names {
			if strings.HasPrefix(name, "cask:") {
				casks = append(casks, strings.TrimPrefix(name, "cask:"))
			} else {
				formulae = append(formulae, name)
			}
		}
		if len(formulae) > 0 {
			commands = append(commands, installCommand{Name: "brew", Args: append([]string{"install"}, formulae...)})
		}
		if len(casks) > 0 {
			commands = append(commands, installCommand{Name: "brew", Args: append([]string{"install", "--cask"}, casks...)})
		}
	case "windows":
		for _, name := range names {
			commands = append(commands, installCommand{Name: "winget", Args: []string{"install", "--exact", "--id", name, "--accept-package-agreements", "--accept-source-agreements"}})
		}
	default:
		return nil, fmt.Errorf("unsupported dependency platform %s", goos)
	}
	for _, id := range []string{"codex", "opencode", "claude", "kimi"} {
		if agents[id] {
			commands = append(commands, agentInstall(goos, id))
		}
	}
	return commands, nil
}

func Ensure(ctx context.Context, profile appconfig.Profile, output io.Writer) error {
	return ensure(ctx, required(profile), output)
}

// EnsureDocker installs only the host-side Docker runtime required to enter
// Sietch Tabr. Docker is a launch prerequisite, not a workspace tool option.
func EnsureDocker(ctx context.Context, output io.Writer) error {
	return ensure(ctx, dockerRequirements(), output)
}

func dockerRequirements() []requirement {
	return []requirement{
		{ID: "docker", Command: "docker"},
		{ID: "docker-compose", Command: "@docker-compose"},
	}
}

func ensure(ctx context.Context, requirements []requirement, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	if err := prepareEnvironment(); err != nil {
		return err
	}
	missingRequirements := missing(requirements)
	if len(missingRequirements) == 0 {
		return nil
	}
	plan, err := installPlan(runtime.GOOS, missingRequirements)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return fmt.Errorf("configured dependencies are missing but no installer is available: %s", missingNames(missingRequirements))
	}
	fmt.Fprintln(output, "Installing only missing dependencies:", missingNames(missingRequirements))
	for _, step := range plan {
		if step.Name == "" {
			return errorsForUnsupported(missingRequirements)
		}
		fmt.Fprintln(output, "  +", step.Name, strings.Join(step.Args, " "))
		if _, err := exec.LookPath(step.Name); err != nil {
			return fmt.Errorf("%s is required to install configured dependencies: %w", step.Name, err)
		}
		command := exec.CommandContext(ctx, step.Name, step.Args...)
		childproc.Configure(command)
		command.Stdin = os.Stdin
		command.Stdout = output
		command.Stderr = output
		if err := command.Run(); err != nil {
			return fmt.Errorf("install configured dependency with %s: %w", step.Name, err)
		}
	}
	stillMissing := missing(requirements)
	if len(stillMissing) > 0 {
		return fmt.Errorf("dependencies still missing after install: %s (open a new terminal if PATH changed)", missingNames(stillMissing))
	}
	return nil
}

func packageNames(goos, id string) []string {
	linux := map[string][]string{
		"curl": {"curl", "ca-certificates"}, "git": {"git"}, "rg": {"ripgrep"}, "fd": {"fd-find"}, "unzip": {"unzip"}, "nvim": {"neovim"},
		"node": {"nodejs", "npm"}, "npm": {"nodejs", "npm"},
		"go": {"golang-go"}, "python": {"python3", "python3-venv"},
		"docker": {"docker.io", "docker-compose-v2"}, "docker-compose": {"docker-compose-v2"},
	}
	darwin := map[string][]string{
		"curl": {"curl"}, "git": {"git"}, "rg": {"ripgrep"}, "fd": {"fd"}, "unzip": {"unzip"}, "nvim": {"neovim"},
		"node": {"node"}, "npm": {"node"}, "go": {"go"}, "python": {"python"},
		"docker": {"cask:docker"}, "docker-compose": {"cask:docker"},
	}
	windows := map[string][]string{
		"git": {"Git.Git"}, "rg": {"BurntSushi.ripgrep.MSVC"},
		"fd": {"sharkdp.fd"}, "nvim": {"Neovim.Neovim"}, "node": {"OpenJS.NodeJS.LTS"}, "npm": {"OpenJS.NodeJS.LTS"},
		"go": {"GoLang.Go"}, "python": {"Python.Python.3.13"},
		"docker":         {"Docker.DockerDesktop"},
		"docker-compose": {"Docker.DockerDesktop"},
	}
	switch goos {
	case "linux":
		return linux[id]
	case "darwin":
		return darwin[id]
	case "windows":
		return windows[id]
	default:
		return nil
	}
}

func agentInstall(goos, id string) installCommand {
	if id == "codex" {
		return installCommand{Name: "npm", Args: npmGlobalArgs(goos, "@openai/codex")}
	}
	if id == "opencode" {
		return installCommand{Name: "npm", Args: npmGlobalArgs(goos, "opencode-ai")}
	}
	if goos == "windows" {
		url := "https://claude.ai/install.ps1"
		if id == "kimi" {
			url = "https://code.kimi.com/kimi-code/install.ps1"
		}
		return installCommand{Name: "powershell", Args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", windowsAgentInstaller + " '" + url + "'"}}
	}
	url := "https://claude.ai/install.sh"
	if id == "kimi" {
		url = "https://code.kimi.com/kimi-code/install.sh"
	}
	return installCommand{Name: "sh", Args: []string{"-c", unixAgentInstaller, "lisan-agent-install", url}}
}

const unixAgentInstaller = `set -eu
script="$(mktemp)"
trap 'unlink "$script"' EXIT
curl -fsSL -o "$script" "$1"
sh "$script"`

const windowsAgentInstaller = `& { param([string]$Uri)
$Script = Join-Path ([IO.Path]::GetTempPath()) (([IO.Path]::GetRandomFileName()) + '.ps1')
try {
  Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $Script
  & $Script
} finally {
  Remove-Item -LiteralPath $Script -Force -ErrorAction SilentlyContinue
}
}`

func npmGlobalArgs(goos, packageName string) []string {
	arguments := []string{"install", "--global"}
	if goos != "windows" {
		if home, err := os.UserHomeDir(); err == nil {
			arguments = append(arguments, "--prefix", filepath.Join(home, ".local"))
		}
	}
	return append(arguments, packageName)
}

// prepareEnvironment makes per-user agent installs visible to this process and
// every embedded child without requiring the user to restart their shell.
func prepareEnvironment() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate user home for dependency PATH: %w", err)
	}
	userBin := filepath.Clean(filepath.Join(home, ".local", "bin"))
	path := os.Getenv("PATH")
	for _, entry := range filepath.SplitList(path) {
		entry = filepath.Clean(entry)
		if entry == userBin || (runtime.GOOS == "windows" && strings.EqualFold(entry, userBin)) {
			return nil
		}
	}
	if path == "" {
		return os.Setenv("PATH", userBin)
	}
	return os.Setenv("PATH", userBin+string(os.PathListSeparator)+path)
}

func isAgent(id string) bool {
	return id == "codex" || id == "opencode" || id == "claude" || id == "kimi"
}

func missingNames(missing []requirement) string {
	names := make([]string, len(missing))
	for index, requirement := range missing {
		names[index] = requirement.ID
	}
	return strings.Join(names, ", ")
}

func errorsForUnsupported(missing []requirement) error {
	return fmt.Errorf("no native installer mapping for: %s", missingNames(missing))
}
