package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestDockerBuildPlanResolvesOnlySelectedAndRequiredTools(t *testing.T) {
	minimal := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	minimal.Terminal.DockerShell = "sh"
	plan := resolveDockerBuildPlan(minimal)
	if plan.ShellPath != "/bin/sh" || plan.Git || plan.Neovim || plan.Node || plan.Codex {
		t.Fatalf("minimal profile leaked Docker tools: %#v", plan)
	}

	minimal.Set(appconfig.Features, "files", true)
	plan = resolveDockerBuildPlan(minimal)
	if !plan.NvChad || !plan.Neovim || !plan.Git || !plan.Ripgrep || plan.Node || plan.Go || plan.Python {
		t.Fatalf("Files dependencies were not resolved precisely: %#v", plan)
	}

	minimal.Set(appconfig.Features, "files", false)
	minimal.Set(appconfig.Features, "agents", true)
	minimal.Set(appconfig.Agents, "codex", true)
	plan = resolveDockerBuildPlan(minimal)
	if !plan.Codex || plan.Node || plan.OpenCode || plan.Git {
		t.Fatalf("Codex dependencies leaked unrelated tools: %#v", plan)
	}
}

func TestDockerfileKeepsProfilePackagesAfterStableBase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	base := strings.Index(dockerfile, "apt-get install -y --no-install-recommends ca-certificates sudo")
	profileArgs := strings.Index(dockerfile, "ARG LISAN_INSTALL_GIT=0")
	selected := strings.Index(dockerfile, "apt-get install -y --no-install-recommends $packages")
	label := strings.Index(dockerfile, "LABEL io.lisanalgaib.build-signature")
	if base < 0 || profileArgs <= base || selected <= profileArgs || label <= selected {
		t.Fatalf("Docker cache-sensitive layers are out of order: base=%d args=%d selected=%d label=%d", base, profileArgs, selected, label)
	}
}

func TestDockerNvChadSelectionSurvivesAnExistingHomeVolume(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	entrypoint, err := os.ReadFile(filepath.Join("..", "..", "docker", "lisan-entrypoint"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "LISAN_NVCHAD=${LISAN_INSTALL_NVCHAD}") {
		t.Fatal("Docker image does not retain the selected NvChad state")
	}
	guard := `if [ "${LISAN_NVCHAD:-0}" = 1 ] && [ ! -e /home/fremen/.config/nvim ]`
	if !strings.Contains(string(entrypoint), guard) || !strings.Contains(string(entrypoint), `cp -a "$nvim_template/." /home/fremen/.config/nvim/`) {
		t.Fatal("entrypoint does not safely seed NvChad into an existing home volume")
	}
}

func TestDockerfileInstallsCodexAsPinnedNativeBinary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	start := strings.Index(dockerfile, `if [ "$LISAN_INSTALL_CODEX" = 1 ]`)
	end := strings.Index(dockerfile, `if [ "$LISAN_INSTALL_OPENCODE" = 1 ]`)
	if start < 0 || end <= start {
		t.Fatal("Dockerfile Codex install block is missing")
	}
	codexBlock := dockerfile[start:end]
	if strings.Contains(codexBlock, "npm") || !strings.Contains(codexBlock, "github.com/openai/codex/releases/download") || !strings.Contains(codexBlock, "sha256sum -c") {
		t.Fatalf("Codex must be a verified native download without npm:\n%s", codexBlock)
	}
}

func TestDockerfilePinsEveryAgentPayloadWithoutManifestTooling(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, argument := range []string{
		"CODEX_SHA256_AMD64", "CODEX_SHA256_ARM64",
		"OPENCODE_SHA256_AMD64", "OPENCODE_SHA256_ARM64",
		"KIMI_SHA256_AMD64", "KIMI_SHA256_ARM64",
		"CLAUDE_SHA256_AMD64", "CLAUDE_SHA256_ARM64",
	} {
		if !strings.Contains(dockerfile, "ARG "+argument+"=") {
			t.Fatalf("Dockerfile does not pin %s", argument)
		}
	}
	if strings.Contains(dockerfile, "manifest.json") || strings.Contains(dockerfile, " jq") {
		t.Fatal("agent builder still trusts live manifests or installs jq")
	}
}

func TestConnectorDockerfileCopiesOnlyItsDependencyPackages(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docker", "connectors", "host-check", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	if strings.Contains(dockerfile, "COPY internal ./internal") {
		t.Fatal("connector build copies every application package")
	}
	for _, dependency := range []string{"appconfig", "childproc", "connectors", "extensionhost", "lifecycle", "safefile", "textsafe"} {
		if !strings.Contains(dockerfile, "COPY internal/"+dependency+" ./internal/"+dependency) {
			t.Fatalf("connector build omits dependency package %s", dependency)
		}
	}
}

func TestDockerBuildArgumentsContainResolvedPlan(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Terminal.DockerShell = "zsh"
	profile.Set(appconfig.Tools, "go", true)
	joined := strings.Join(resolveDockerBuildPlan(profile).buildArguments("digest"), " ")
	for _, expected := range []string{"LISAN_BUILD_SIGNATURE=digest", "LISAN_DOCKER_SHELL=zsh", "LISAN_SHELL_PATH=/usr/bin/zsh", "LISAN_INSTALL_GO=1", "LISAN_INSTALL_NODE=0"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Docker build arguments omit %q: %s", expected, joined)
		}
	}
}

func TestDockerfileDeclaresEveryGeneratedBuildArgument(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	arguments := resolveDockerBuildPlan(appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())).buildArguments("digest")
	for index := 0; index < len(arguments); index++ {
		if arguments[index] != "--build-arg" || index+1 >= len(arguments) {
			continue
		}
		name := strings.SplitN(arguments[index+1], "=", 2)[0]
		if !strings.Contains(string(data), "ARG "+name+"=") {
			t.Fatalf("Dockerfile does not declare generated build argument %s", name)
		}
		index++
	}
}

func TestDockerBuildSignatureTracksPlanAndInputs(t *testing.T) {
	root := dockerBuildFixture(t)
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	first, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	profile.Set(appconfig.Tools, "python", true)
	second, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("Docker dependency change did not invalidate image signature")
	}
	for _, ignored := range []string{"source_test.go", "source_windows.go"} {
		if err := os.WriteFile(filepath.Join(root, "internal", ignored), []byte("ignored\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	if second != filtered {
		t.Fatal("build-only source invalidated the Linux image signature")
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "source.go"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("Docker source change did not invalidate image signature")
	}
}

func dockerBuildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"cmd", "internal", "docker/nvim", "docker/home"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{".dockerignore", "Dockerfile", "go.mod", "go.sum", "docker/lisan-entrypoint"} {
		path := filepath.Join(root, file)
		if err := os.WriteFile(path, []byte(file+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
