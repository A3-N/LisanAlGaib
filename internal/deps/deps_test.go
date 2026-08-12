package deps

import (
	"os"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestMinimalProfileRequiresNothing(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	if required := required(profile); len(required) != 0 {
		t.Fatalf("minimal config should not probe dependencies: %#v", required)
	}
}

func TestFilesRequiresCompleteNvChadToolchain(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "files", true)
	seen := map[string]bool{}
	for _, item := range required(profile) {
		seen[item.ID] = true
	}
	for _, id := range []string{"nvim", "git", "rg", "fd"} {
		if !seen[id] {
			t.Fatalf("Files dependency set omits %s: %#v", id, seen)
		}
	}
	if seen["node"] || seen["go"] || seen["python"] {
		t.Fatalf("Files dependency set leaked unrelated runtimes: %#v", seen)
	}
}

func TestSelectedLanguagesUtilitiesAndNetworkToolsBecomeRequirements(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	for _, id := range []string{"pip", "rust", "jq", "ip", "dns", "netcat"} {
		profile.Set(appconfig.Tools, id, true)
	}
	seen := map[string]bool{}
	for _, item := range required(profile) {
		seen[item.ID] = true
	}
	for _, id := range []string{"pip", "rust", "jq", "ip", "dns", "netcat"} {
		if !seen[id] {
			t.Fatalf("selected tool %q is missing from requirements: %#v", id, seen)
		}
	}
	for _, id := range []string{"python", "java", "wget", "nmap"} {
		if seen[id] {
			t.Fatalf("disabled tool %q leaked into requirements: %#v", id, seen)
		}
	}
}

func TestEverySelectableToolHasANativeCommandOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, option := range appconfig.Options {
			if option.Category != appconfig.Tools || option.ID == "nvchad" {
				continue
			}
			if command := toolCommand(goos, option.ID); command == "" {
				t.Fatalf("tool %q has no %s command mapping", option.ID, goos)
			}
		}
	}
}

func TestHostTerminalDoesNotInstallOrChooseAShell(t *testing.T) {
	t.Setenv("LISAN_CONTAINER", "")
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "terminal", true)
	if required := required(profile); len(required) != 0 {
		t.Fatalf("host terminal invented shell dependencies: %#v", required)
	}
}

func TestDockerTerminalRequiresConfiguredShell(t *testing.T) {
	t.Setenv("LISAN_CONTAINER", "1")
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "terminal", true)
	profile.Terminal.DockerShell = "zsh"
	required := required(profile)
	if len(required) != 1 || required[0].ID != "zsh" || required[0].Command != "zsh" {
		t.Fatalf("Docker terminal did not require only its selected shell: %#v", required)
	}
}

func TestAgentSelectionRequiresOnlySelectedAgent(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "agents", true)
	profile.Set(appconfig.Agents, "omp", true)
	required := required(profile)
	seen := map[string]bool{}
	for _, item := range required {
		seen[item.ID] = true
	}
	if !seen["omp"] || seen["npm"] || seen["bash"] || seen["claude"] || seen["codex"] || seen["nvim"] {
		t.Fatalf("agent requirements leaked disabled components: %#v", required)
	}
}

func TestAgentInstallerPrerequisitesAreConditional(t *testing.T) {
	missing := []requirement{{ID: "codex", Command: "codex"}, {ID: "opencode", Command: "opencode"}}
	available := func(string) (string, error) { return "/available", nil }
	if got := withInstallerPrerequisites("linux", missing, available); len(got) != len(missing) {
		t.Fatalf("available installer tools still became configured requirements: %#v", got)
	}
	unavailable := func(string) (string, error) { return "", os.ErrNotExist }
	got := withInstallerPrerequisites("linux", missing, unavailable)
	seen := map[string]bool{}
	for _, item := range got {
		seen[item.ID] = true
	}
	if !seen["curl"] || !seen["bash"] || seen["npm"] {
		t.Fatalf("standalone installer prerequisites are incorrect: %#v", got)
	}
}

func TestOhMyPiInstallerUsesOfficialDownloadedScript(t *testing.T) {
	missing := []requirement{{ID: "omp", Command: "omp"}}
	unavailable := func(string) (string, error) { return "", os.ErrNotExist }
	got := withInstallerPrerequisites("linux", missing, unavailable)
	if len(got) != 2 || got[1].ID != "curl" {
		t.Fatalf("missing curl was not added for Oh My Pi: %#v", got)
	}
	unixCommand, unixErr := agentInstall("linux", "omp")
	windowsCommand, windowsErr := agentInstall("windows", "omp")
	if unixErr != nil || windowsErr != nil {
		t.Fatalf("Oh My Pi installer mapping failed: unix=%v windows=%v", unixErr, windowsErr)
	}
	unix := strings.Join(unixCommand.Args, " ")
	windows := strings.Join(windowsCommand.Args, " ")
	if !strings.Contains(unix, "https://omp.sh/install") || !strings.Contains(unix, "--binary") ||
		!strings.Contains(windows, "https://omp.sh/install.ps1") || !strings.Contains(windows, "-Binary") {
		t.Fatalf("Oh My Pi installers are not official: unix=%q windows=%q", unix, windows)
	}
}

func TestAgentInstallersNeverUseNPMFallbacks(t *testing.T) {
	tests := []struct {
		goos, id, required string
	}{
		{"linux", "codex", "https://chatgpt.com/codex/install.sh"},
		{"darwin", "opencode", "https://opencode.ai/install"},
		{"windows", "codex", "https://chatgpt.com/codex/install.ps1"},
	}
	for _, test := range tests {
		command, err := agentInstall(test.goos, test.id)
		if err != nil {
			t.Fatalf("%s/%s installer: %v", test.goos, test.id, err)
		}
		joined := strings.Join(append(append([]string{command.Name}, command.Args...), command.Env...), " ")
		if strings.Contains(strings.ToLower(joined), "npm") || !strings.Contains(joined, test.required) {
			t.Fatalf("%s/%s did not use only its standalone installer: %q", test.goos, test.id, joined)
		}
	}
	windowsOpenCode, err := agentInstall("windows", "opencode")
	if err != nil || windowsOpenCode.Name != "scoop" {
		t.Fatalf("Windows OpenCode must use its documented Scoop installer: %#v %v", windowsOpenCode, err)
	}
}

func TestAgentInstallerHasNoImplicitFallbackMapping(t *testing.T) {
	for _, test := range []struct{ goos, id string }{{"plan9", "codex"}, {"linux", "unknown"}} {
		if _, err := agentInstall(test.goos, test.id); err == nil || !strings.Contains(err.Error(), "no standalone installer") {
			t.Fatalf("%s/%s did not fail without an explicit installer: %v", test.goos, test.id, err)
		}
	}
}

func TestInstallPlansAreNative(t *testing.T) {
	missing := []requirement{{ID: "rg"}, {ID: "codex"}}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		plan, err := installPlan(goos, missing)
		if err != nil || len(plan) < 2 {
			t.Fatalf("%s plan: %#v %v", goos, plan, err)
		}
	}
}

func TestDockerCheckNeverInstallsHostDependencies(t *testing.T) {
	t.Setenv("PATH", "")
	err := CheckDocker()
	if err == nil || !strings.Contains(err.Error(), "docker, docker-compose") {
		t.Fatalf("Docker check did not report every host prerequisite: %v", err)
	}
	if os.Getenv("PATH") != "" {
		t.Fatalf("Docker check mutated the host PATH: %q", os.Getenv("PATH"))
	}
}

func TestAgentInstallersDownloadCompletelyBeforeExecution(t *testing.T) {
	unixCommand, unixErr := agentInstall("linux", "claude")
	windowsCommand, windowsErr := agentInstall("windows", "kimi")
	if unixErr != nil || windowsErr != nil {
		t.Fatalf("agent installer mapping failed: unix=%v windows=%v", unixErr, windowsErr)
	}
	unix := strings.Join(unixCommand.Args, " ")
	windows := strings.Join(windowsCommand.Args, " ")
	if strings.Contains(unix, "| bash") || !strings.Contains(unix, `curl -fsSL -o`) {
		t.Fatalf("Unix installer still streams into an interpreter: %q", unix)
	}
	if strings.Contains(strings.ToLower(windows), "| iex") || !strings.Contains(windows, "-OutFile") {
		t.Fatalf("Windows installer still streams into an interpreter: %q", windows)
	}
}

func TestEmptyPathDoesNotAddCurrentDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	if err := prepareEnvironment(); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(os.Getenv("PATH"), string(os.PathListSeparator)) {
		t.Fatalf("empty PATH gained a current-directory entry: %q", os.Getenv("PATH"))
	}
}
