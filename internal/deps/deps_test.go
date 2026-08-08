package deps

import (
	"os"
	"path/filepath"
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

func TestAgentSelectionRequiresOnlySelectedAgentAndInstaller(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "agents", true)
	profile.Set(appconfig.Agents, "codex", true)
	required := required(profile)
	seen := map[string]bool{}
	for _, item := range required {
		seen[item.ID] = true
	}
	if !seen["codex"] || !seen["npm"] || seen["bash"] || seen["claude"] || seen["nvim"] {
		t.Fatalf("agent requirements leaked disabled components: %#v", required)
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

func TestUnixNPMInstallUsesWritableUserPrefix(t *testing.T) {
	arguments := npmGlobalArgs("linux", "opencode-ai")
	joined := strings.Join(arguments, " ")
	home, _ := os.UserHomeDir()
	if !strings.Contains(joined, "--prefix "+filepath.Join(home, ".local")) {
		t.Fatalf("npm install would target the system prefix: %q", joined)
	}
}

func TestHostDockerRuntimeAlsoRequiresComposePlugin(t *testing.T) {
	requirements := dockerRequirements()
	if len(requirements) != 2 || requirements[1].ID != "docker-compose" {
		t.Fatalf("Docker requirements omit the Compose plugin: %#v", requirements)
	}
}

func TestAgentInstallersDownloadCompletelyBeforeExecution(t *testing.T) {
	unix := strings.Join(agentInstall("linux", "claude").Args, " ")
	windows := strings.Join(agentInstall("windows", "kimi").Args, " ")
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
