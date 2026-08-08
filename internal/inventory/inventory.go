package inventory

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"lisanalgaib/internal/nvimconfig"
	"lisanalgaib/internal/textsafe"
)

type Tool struct {
	ID          string
	Name        string
	Category    string
	Description string
	Command     string
	Path        string
	Version     string
	Package     string
	Installed   bool
	Agent       bool
	AuthHints   []string
	Docs        string
}

type Package struct {
	Name    string
	Version string
}

type Snapshot struct {
	Tools     []Tool
	APTManual []Package
}

type Selection struct {
	// IDs is nil for the legacy full scan. An empty non-nil map scans no tools.
	IDs       map[string]bool
	APTManual bool
}

type spec struct {
	Tool
	VersionArgs []string
}

var specs = []spec{
	{Tool: Tool{ID: "codex", Name: "Codex", Category: "Agent CLIs", Command: "codex", Agent: true, Description: "OpenAI coding agent CLI", AuthHints: []string{"Launch Codex and use its login flow", "Credentials remain owned by the CLI"}, Docs: "https://developers.openai.com/codex/cli"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "opencode", Name: "OpenCode", Category: "Agent CLIs", Command: "opencode", Agent: true, Description: "Provider-flexible coding agent TUI", AuthHints: []string{"Launch and use /connect", "Or configure providers in opencode.json"}, Docs: "https://opencode.ai/docs/"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "claude", Name: "Claude Code", Category: "Agent CLIs", Command: "claude", Agent: true, Description: "Anthropic coding agent CLI", AuthHints: []string{"Run claude auth login", "API billing: claude auth login --console"}, Docs: "https://code.claude.com/docs/"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "kimi", Name: "Kimi Code", Category: "Agent CLIs", Command: "kimi", Agent: true, Description: "Moonshot AI coding agent TUI", AuthHints: []string{"Launch and use /login", "Config is normally stored below ~/.kimi-code"}, Docs: "https://www.kimi.com/code/docs/"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "bash", Name: "Bash", Category: "Shell", Command: "bash", Description: "Bourne Again Shell"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "fish", Name: "Fish", Category: "Shell", Command: "fish", Description: "Friendly interactive shell"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "zsh", Name: "Zsh", Category: "Shell", Command: "zsh", Description: "Z shell"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "sh", Name: "POSIX sh", Category: "Shell", Command: "sh", Description: "Minimal POSIX shell"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "pwsh", Name: "PowerShell", Category: "Shell", Command: "pwsh", Description: "Cross-platform PowerShell"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "powershell", Name: "Windows PowerShell", Category: "Shell", Command: "powershell", Description: "Windows PowerShell"}, VersionArgs: []string{"-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}},
	{Tool: Tool{ID: "cmd", Name: "Command Prompt", Category: "Shell", Command: "cmd", Description: "Windows command shell"}, VersionArgs: []string{"/C", "ver"}},
	{Tool: Tool{ID: "nvim", Name: "Neovim", Category: "Editors", Command: "nvim", Description: "Vim-compatible editor hosting the embedded NvChad file pane"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "git", Name: "Git", Category: "Core", Command: "git", Description: "Version control and worktree engine"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "rg", Name: "ripgrep", Category: "Core", Command: "rg", Description: "Fast recursive search"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "node", Name: "Node.js", Category: "Language Runtimes", Command: "node", Description: "JavaScript runtime"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "npm", Name: "npm", Category: "Language Runtimes", Command: "npm", Description: "JavaScript package manager"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "go", Name: "Go", Category: "Language Runtimes", Command: "go", Description: "Go compiler and toolchain"}, VersionArgs: []string{"version"}},
	{Tool: Tool{ID: "python", Name: "Python", Category: "Language Runtimes", Command: windowsCommand("python3", "python"), Description: "Python 3 runtime"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "pip", Name: "pip", Category: "Language Runtimes", Command: windowsCommand("pip3", "pip"), Description: "Python package manager"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "rust", Name: "Rust", Category: "Language Runtimes", Command: "rustc", Description: "Rust compiler"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "java", Name: "Java JDK", Category: "Language Runtimes", Command: "javac", Description: "Java compiler and runtime"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "clang", Name: "Clang", Category: "Language Runtimes", Command: "clang", Description: "C and C++ compiler toolchain"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "ruby", Name: "Ruby", Category: "Language Runtimes", Command: "ruby", Description: "Ruby runtime"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "php", Name: "PHP", Category: "Language Runtimes", Command: "php", Description: "PHP command-line runtime"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "lua", Name: "Lua", Category: "Language Runtimes", Command: "lua", Description: "Lua runtime"}, VersionArgs: []string{"-v"}},
	{Tool: Tool{ID: "curl", Name: "curl", Category: "Utilities", Command: "curl", Description: "HTTP and data-transfer client"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "jq", Name: "jq", Category: "Utilities", Command: "jq", Description: "JSON query and transformation tool"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "wget", Name: "wget", Category: "Utilities", Command: "wget", Description: "Non-interactive network downloader"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "zip", Name: "zip", Category: "Utilities", Command: "zip", Description: "ZIP archive creator"}, VersionArgs: []string{"-v"}},
	{Tool: Tool{ID: "fd", Name: "fd", Category: "Utilities", Command: linuxCommand("fdfind", "fd"), Description: "Fast filesystem search"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "fzf", Name: "fzf", Category: "Utilities", Command: "fzf", Description: "Fuzzy finder"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "bat", Name: "bat", Category: "Utilities", Command: linuxCommand("batcat", "bat"), Description: "Syntax-highlighted file viewer"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "tree", Name: "tree", Category: "Utilities", Command: "tree", Description: "Directory tree viewer"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "shellcheck", Name: "ShellCheck", Category: "Utilities", Command: "shellcheck", Description: "Shell script static analysis"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "ip", Name: "IProute2", Category: "Networking", Command: windowsCommand("ip", "ipconfig"), Description: "IP address and route inspection"}, VersionArgs: []string{"-Version"}},
	{Tool: Tool{ID: "ping", Name: "Ping", Category: "Networking", Command: "ping", Description: "ICMP reachability checks"}, VersionArgs: []string{"--help"}},
	{Tool: Tool{ID: "dns", Name: "DNS utilities", Category: "Networking", Command: windowsCommand("dig", "nslookup"), Description: "DNS lookup and diagnostics"}, VersionArgs: []string{"-v"}},
	{Tool: Tool{ID: "net-tools", Name: "Net-tools", Category: "Networking", Command: windowsCommand("ifconfig", "netstat"), Description: "Interface and connection inspection"}, VersionArgs: []string{"--help"}},
	{Tool: Tool{ID: "traceroute", Name: "Traceroute", Category: "Networking", Command: windowsCommand("traceroute", "tracert"), Description: "Network path tracing"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "netcat", Name: "Netcat", Category: "Networking", Command: windowsCommand("nc", "ncat"), Description: "TCP and UDP diagnostics"}, VersionArgs: []string{"-h"}},
	{Tool: Tool{ID: "nmap", Name: "Nmap", Category: "Networking", Command: "nmap", Description: "Network discovery and port scanning"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "mtr", Name: "MTR", Category: "Networking", Command: windowsCommand("mtr", "pathping"), Description: "Route and latency diagnostics"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "tcpdump", Name: "tcpdump", Category: "Networking", Command: windowsCommand("tcpdump", "pktmon"), Description: "Packet capture and inspection"}, VersionArgs: []string{"--version"}},
	{Tool: Tool{ID: "whois", Name: "Whois", Category: "Networking", Command: "whois", Description: "Domain and network registration lookup"}, VersionArgs: []string{"--version"}},
}

func windowsCommand(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func linuxCommand(linux, other string) string {
	if runtime.GOOS == "linux" {
		return linux
	}
	return other
}

func ScanSelected(ctx context.Context, selection Selection) Snapshot {
	snapshot := Snapshot{}

	selectedSpecs := make([]spec, 0, len(specs))
	for _, candidate := range specs {
		if selection.IDs == nil || selection.IDs[candidate.ID] {
			selectedSpecs = append(selectedSpecs, candidate)
		}
	}
	tools := make([]Tool, len(selectedSpecs))
	var wg sync.WaitGroup
	for i := range selectedSpecs {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			tools[i] = inspect(ctx, selectedSpecs[i])
		}()
	}
	wg.Wait()
	if selection.IDs == nil || selection.IDs["nvchad"] {
		nvChad := Tool{
			ID: "nvchad", Name: "NvChad", Category: "Editors", Command: "nvim",
			Description: "Neovim distribution used by Lisan's file workspace",
			Docs:        "https://nvchad.com/",
		}
		if nvimconfig.NvChadInstalled() {
			nvChad.Installed = true
			nvChad.Path, _ = nvimconfig.ConfigDir()
			nvChad.Version = "configured"
			nvChad.Package = "user config"
		}
		tools = append(tools, nvChad)
	}

	for i := range tools {
		if tools[i].Installed && tools[i].Package == "" {
			tools[i].Package = packageOwner(ctx, tools[i].Path)
		}
	}
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].Category == tools[j].Category {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].Category < tools[j].Category
	})
	snapshot.Tools = tools
	if selection.APTManual {
		snapshot.APTManual = aptManual(ctx)
	}
	return snapshot
}

func inspect(ctx context.Context, candidate spec) Tool {
	tool := candidate.Tool
	path, err := exec.LookPath(candidate.Command)
	if err != nil {
		return tool
	}
	tool.Installed = true
	tool.Path = path
	tool.Version = safeVersion(ctx, path, candidate.VersionArgs)
	return tool
}

func safeVersion(parent context.Context, path string, args []string) string {
	ctx, cancel := context.WithTimeout(parent, 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil && output.Len() == 0 {
		return "version unavailable"
	}
	line := strings.SplitN(output.String(), "\n", 2)[0]
	return textsafe.Label(line, 100)
}

func packageOwner(parent context.Context, path string) string {
	if runtime.GOOS != "linux" {
		return "manual or external"
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dpkg-query", "-S", path).Output()
	if err != nil {
		return "manual or external"
	}
	owner := strings.SplitN(string(out), ":", 2)[0]
	return strings.TrimSpace(owner)
}

func aptManual(parent context.Context) []Package {
	if runtime.GOOS != "linux" {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	manualOut, err := exec.CommandContext(ctx, "apt-mark", "showmanual").Output()
	if err != nil {
		return nil
	}
	versionOut, _ := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${binary:Package}\\t${Version}\\n").Output()
	versions := make(map[string]string)
	for _, line := range strings.Split(string(versionOut), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			versions[parts[0]] = parts[1]
			versions[strings.SplitN(parts[0], ":", 2)[0]] = parts[1]
		}
	}
	var packages []Package
	for _, name := range strings.Fields(string(manualOut)) {
		packages = append(packages, Package{Name: name, Version: versions[name]})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages
}
