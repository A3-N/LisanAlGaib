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
	"lisanalgaib/internal/cliout"
)

type requirement struct {
	ID      string
	Command string
}

type installCommand struct {
	Name string
	Args []string
	Env  []string
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
	for _, option := range appconfig.Options {
		if option.Category == appconfig.Tools && profile.Tool(option.ID) {
			add(option.ID, toolCommand(runtime.GOOS, option.ID))
		}
	}
	if profile.Tool("node") {
		add("npm", "npm")
	}
	if profile.Feature("agents") {
		for _, id := range appconfig.AgentIDs() {
			if profile.Agent(id) {
				add(id, id)
			}
		}
	}
	result := make([]requirement, 0, len(byCommand))
	for _, requirement := range byCommand {
		result = append(result, requirement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func toolCommand(goos, id string) string {
	common := map[string]string{
		"git": "git", "rg": "rg", "nvim": "nvim", "node": "node", "go": "go",
		"rust": "rustc", "java": "javac", "clang": "clang", "ruby": "ruby", "php": "php", "lua": "lua",
		"curl": "curl", "jq": "jq", "wget": "wget", "zip": "zip", "fzf": "fzf", "tree": "tree",
		"shellcheck": "shellcheck", "ping": "ping", "traceroute": "traceroute", "nmap": "nmap",
		"mtr": "mtr", "tcpdump": "tcpdump", "whois": "whois",
	}
	commands := map[string]map[string]string{
		"linux": {
			"python": "python3", "pip": "pip3", "fd": "fdfind", "bat": "batcat",
			"ip": "ip", "dns": "dig", "net-tools": "ifconfig", "netcat": "nc",
		},
		"darwin": {
			"python": "python3", "pip": "pip3", "fd": "fd", "bat": "bat",
			"ip": "ip", "dns": "dig", "net-tools": "ifconfig", "netcat": "nc",
		},
		"windows": {
			"python": "python", "pip": "pip", "fd": "fd", "bat": "bat",
			"ip": "ipconfig", "dns": "nslookup", "net-tools": "netstat", "traceroute": "tracert",
			"netcat": "ncat", "mtr": "pathping", "tcpdump": "pktmon",
		},
	}
	if command := commands[goos][id]; command != "" {
		return command
	}
	return common[id]
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
	for _, id := range appconfig.AgentIDs() {
		if agents[id] {
			command, err := agentInstall(goos, id)
			if err != nil {
				return nil, err
			}
			commands = append(commands, command)
		}
	}
	return commands, nil
}

// Ensure may install selected host dependencies and is therefore reserved for
// the explicitly unsandboxed vm command.
func Ensure(ctx context.Context, profile appconfig.Profile, output io.Writer) error {
	return ensure(ctx, required(profile), output)
}

// CheckDocker is deliberately read-only. Docker mode never runs a host package
// manager or installer; users remain in control of their host configuration.
func CheckDocker() error {
	missingRequirements := missing(dockerRequirements())
	if len(missingRequirements) > 0 {
		return fmt.Errorf("docker mode requires host prerequisites: %s; install Docker Desktop/Engine with the Compose plugin and retry", missingNames(missingRequirements))
	}
	return nil
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
	installRequirements := withInstallerPrerequisites(runtime.GOOS, missingRequirements, exec.LookPath)
	plan, err := installPlan(runtime.GOOS, installRequirements)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return fmt.Errorf("configured dependencies are missing but no installer is available: %s", missingNames(missingRequirements))
	}
	cliout.Start(output, "Installing dependencies", missingNames(missingRequirements))
	for _, step := range plan {
		if step.Name == "" {
			return errorsForUnsupported(missingRequirements)
		}
		cliout.Detail(output, "command", step.Name+" "+strings.Join(step.Args, " "))
		if _, err := exec.LookPath(step.Name); err != nil {
			return fmt.Errorf("%s is required to install configured dependencies: %w", step.Name, err)
		}
		command := exec.CommandContext(ctx, step.Name, step.Args...)
		if len(step.Env) > 0 {
			command.Env = append(os.Environ(), step.Env...)
		}
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
	cliout.Success(output, "Installing dependencies")
	return nil
}

// withInstallerPrerequisites adds only the commands required by an agent's
// standalone installer. Agent installation never makes npm a prerequisite.
func withInstallerPrerequisites(goos string, missing []requirement, lookPath func(string) (string, error)) []requirement {
	result := append([]requirement(nil), missing...)
	seen := map[string]bool{}
	for _, item := range result {
		seen[item.ID] = true
	}
	needsCurl, needsBash := false, false
	for _, item := range missing {
		if goos != "windows" && isAgent(item.ID) {
			needsCurl = true
		}
		needsBash = needsBash || (goos != "windows" && item.ID == "opencode")
	}
	for _, prerequisite := range []struct {
		needed  bool
		id      string
		command string
	}{{needsCurl, "curl", "curl"}, {needsBash, "bash", "bash"}} {
		if !prerequisite.needed || seen[prerequisite.id] {
			continue
		}
		if _, err := lookPath(prerequisite.command); err == nil {
			continue
		}
		result = append(result, requirement{ID: prerequisite.id, Command: prerequisite.command})
		seen[prerequisite.id] = true
	}
	return result
}

func packageNames(goos, id string) []string {
	linux := map[string][]string{
		"bash": {"bash"}, "curl": {"curl", "ca-certificates"}, "git": {"git"}, "rg": {"ripgrep"}, "fd": {"fd-find"}, "unzip": {"unzip"}, "nvim": {"neovim"},
		"node": {"nodejs", "npm"}, "npm": {"nodejs", "npm"},
		"go": {"golang-go"}, "python": {"python3", "python3-venv"}, "pip": {"python3-pip"},
		"rust": {"cargo", "rustc"}, "java": {"default-jdk-headless"}, "clang": {"clang"}, "ruby": {"ruby-full"}, "php": {"php-cli"}, "lua": {"lua5.4"},
		"jq": {"jq"}, "wget": {"wget"}, "zip": {"zip"}, "fzf": {"fzf"}, "bat": {"bat"}, "tree": {"tree"}, "shellcheck": {"shellcheck"},
		"ip": {"iproute2"}, "ping": {"iputils-ping"}, "dns": {"dnsutils"}, "net-tools": {"net-tools"}, "traceroute": {"traceroute"}, "netcat": {"netcat-openbsd"},
		"nmap": {"nmap"}, "mtr": {"mtr-tiny"}, "tcpdump": {"tcpdump"}, "whois": {"whois"},
	}
	darwin := map[string][]string{
		"bash": {"bash"}, "curl": {"curl"}, "git": {"git"}, "rg": {"ripgrep"}, "fd": {"fd"}, "unzip": {"unzip"}, "nvim": {"neovim"},
		"node": {"node"}, "npm": {"node"}, "go": {"go"}, "python": {"python"}, "pip": {"python"},
		"rust": {"rust"}, "java": {"cask:temurin"}, "clang": {"llvm"}, "ruby": {"ruby"}, "php": {"php"}, "lua": {"lua"},
		"jq": {"jq"}, "wget": {"wget"}, "zip": {"zip"}, "fzf": {"fzf"}, "bat": {"bat"}, "tree": {"tree"}, "shellcheck": {"shellcheck"},
		"ip": {"iproute2mac"}, "nmap": {"nmap"}, "mtr": {"mtr"},
	}
	windows := map[string][]string{
		"curl": {"cURL.cURL"}, "git": {"Git.Git"}, "rg": {"BurntSushi.ripgrep.MSVC"},
		"fd": {"sharkdp.fd"}, "nvim": {"Neovim.Neovim"}, "node": {"OpenJS.NodeJS.LTS"}, "npm": {"OpenJS.NodeJS.LTS"},
		"go": {"GoLang.Go"}, "python": {"Python.Python.3.13"}, "pip": {"Python.Python.3.13"},
		"rust": {"Rustlang.Rustup"}, "java": {"EclipseAdoptium.Temurin.21.JDK"}, "clang": {"LLVM.LLVM"}, "ruby": {"RubyInstallerTeam.RubyWithDevKit.3.3"}, "php": {"PHP.PHP.8.4"}, "lua": {"DEVCOM.Lua"},
		"jq": {"jqlang.jq"}, "wget": {"JernejSimoncic.Wget"}, "zip": {"GnuWin32.Zip"}, "fzf": {"junegunn.fzf"}, "bat": {"sharkdp.bat"}, "shellcheck": {"koalaman.shellcheck"},
		"netcat": {"Insecure.Nmap"}, "nmap": {"Insecure.Nmap"}, "whois": {"Microsoft.Sysinternals"},
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

func agentInstall(goos, id string) (installCommand, error) {
	userBin, err := userBinDirectory()
	if err != nil {
		return installCommand{}, err
	}
	if goos == "windows" {
		if id == "opencode" {
			// OpenCode documents Scoop as a native Windows installer. Do not
			// fall back to npm when Scoop is unavailable; ensure reports that
			// missing command as the installation error.
			return installCommand{Name: "scoop", Args: []string{"install", "opencode"}}, nil
		}
		specifications := map[string]struct {
			URL  string
			Args []string
			Env  []string
		}{
			"codex": {
				URL: "https://chatgpt.com/codex/install.ps1",
				Env: []string{
					"CODEX_INSTALL_DIR=" + userBin,
					"CODEX_NON_INTERACTIVE=true",
					"CODEX_INSTALLER_USE_RELEASES_OPENAI_COM=false",
				},
			},
			"claude": {URL: "https://claude.ai/install.ps1"},
			"kimi":   {URL: "https://code.kimi.com/kimi-code/install.ps1"},
			"omp": {
				URL:  "https://omp.sh/install.ps1",
				Args: []string{"-Binary"},
				Env:  []string{"PI_INSTALL_DIR=" + userBin},
			},
		}
		specification, ok := specifications[id]
		if !ok {
			return installCommand{}, fmt.Errorf("no standalone installer for agent %s on %s", id, goos)
		}
		command := windowsAgentInstallCommand(specification.URL, specification.Args...)
		command.Env = specification.Env
		return command, nil
	}
	if goos != "linux" && goos != "darwin" {
		return installCommand{}, fmt.Errorf("no standalone installer for agent %s on %s", id, goos)
	}
	specifications := map[string]struct {
		URL         string
		Interpreter string
		Args        []string
		Env         []string
	}{
		"codex": {
			URL:         "https://chatgpt.com/codex/install.sh",
			Interpreter: "sh",
			Env: []string{
				"CODEX_INSTALL_DIR=" + userBin,
				"CODEX_NON_INTERACTIVE=true",
				"CODEX_INSTALLER_USE_RELEASES_OPENAI_COM=false",
			},
		},
		"opencode": {
			URL:         "https://opencode.ai/install",
			Interpreter: "bash",
			Args:        []string{"--no-modify-path"},
			Env:         []string{"OPENCODE_INSTALL_DIR=" + userBin},
		},
		"claude": {URL: "https://claude.ai/install.sh", Interpreter: "sh"},
		"kimi":   {URL: "https://code.kimi.com/kimi-code/install.sh", Interpreter: "sh"},
		"omp": {
			URL:         "https://omp.sh/install",
			Interpreter: "sh",
			Args:        []string{"--binary"},
			Env:         []string{"PI_INSTALL_DIR=" + userBin},
		},
	}
	specification, ok := specifications[id]
	if !ok {
		return installCommand{}, fmt.Errorf("no standalone installer for agent %s on %s", id, goos)
	}
	command := unixAgentInstallCommand(specification.URL, specification.Interpreter, specification.Args...)
	command.Env = specification.Env
	return command, nil
}

const unixAgentInstaller = `set -eu
url="$1"
interpreter="$2"
shift 2
script="$(mktemp)"
trap 'unlink "$script"' EXIT
curl -fsSL -o "$script" "$url"
"$interpreter" "$script" "$@"`

const windowsAgentInstaller = `& { param([string]$Uri)
$Script = Join-Path ([IO.Path]::GetTempPath()) (([IO.Path]::GetRandomFileName()) + '.ps1')
try {
  Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $Script
  & $Script @args
} finally {
  Remove-Item -LiteralPath $Script -Force -ErrorAction SilentlyContinue
}
}`

func unixAgentInstallCommand(url, interpreter string, arguments ...string) installCommand {
	args := []string{"-c", unixAgentInstaller, "lisan-agent-install", url, interpreter}
	return installCommand{Name: "sh", Args: append(args, arguments...)}
}

func windowsAgentInstallCommand(url string, arguments ...string) installCommand {
	command := windowsAgentInstaller + " '" + strings.ReplaceAll(url, "'", "''") + "'"
	for _, argument := range arguments {
		command += " '" + strings.ReplaceAll(argument, "'", "''") + "'"
	}
	return installCommand{Name: "powershell", Args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command}}
}

func userBinDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate user home for agent installer: %w", err)
	}
	return filepath.Clean(filepath.Join(home, ".local", "bin")), nil
}

// prepareEnvironment makes per-user agent installs visible to this process and
// every embedded child without requiring the user to restart their shell.
func prepareEnvironment() error {
	userBin, err := userBinDirectory()
	if err != nil {
		return fmt.Errorf("locate dependency PATH: %w", err)
	}
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
	for _, candidate := range appconfig.AgentIDs() {
		if id == candidate {
			return true
		}
	}
	return false
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
