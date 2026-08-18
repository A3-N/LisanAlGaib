package launcher

import (
	"encoding/json"
	"os"
	"os/exec"
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
	if !plan.Codex || plan.Node || plan.OpenCode || plan.OMP || plan.Git {
		t.Fatalf("Codex dependencies leaked unrelated tools: %#v", plan)
	}
	minimal.Set(appconfig.Agents, "codex", false)
	minimal.Set(appconfig.Agents, "omp", true)
	plan = resolveDockerBuildPlan(minimal)
	if !plan.OMP || plan.Codex || plan.OpenCode || plan.Claude || plan.Kimi {
		t.Fatalf("Oh My Pi selection leaked other Mentats: %#v", plan)
	}
	if arguments := strings.Join(plan.buildArguments("signature"), " "); !strings.Contains(arguments, "LISAN_INSTALL_OMP=1") {
		t.Fatalf("Oh My Pi selection did not reach Docker build args: %s", arguments)
	}

	minimal.Set(appconfig.Tools, "rust", true)
	minimal.Set(appconfig.Tools, "jq", true)
	plan = resolveDockerBuildPlan(minimal)
	if packages := strings.Join(plan.ExtraPackages, " "); packages != "cargo jq rustc" {
		t.Fatalf("selected extra packages = %q, want %q", packages, "cargo jq rustc")
	}
}

func TestEveryAdditionalToolHasASelectiveDockerPackageMapping(t *testing.T) {
	special := map[string]bool{
		"git": true, "rg": true, "nvim": true, "nvchad": true,
		"node": true, "go": true, "python": true,
	}
	for _, option := range appconfig.Options {
		if option.Category != appconfig.Tools || special[option.ID] {
			continue
		}
		if len(dockerExtraPackages[option.ID]) == 0 {
			t.Fatalf("tool %q has no Docker package mapping", option.ID)
		}
	}
}

func TestDockerBuildLeavesProgressModeToTheComposeWrapper(t *testing.T) {
	minimal := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	joined := strings.Join(resolveDockerBuildPlan(minimal).buildArguments("signature"), " ")
	if !strings.HasPrefix(joined, "build ") || strings.Contains(joined, "--progress") {
		t.Fatalf("build plan unexpectedly owns the Compose progress mode: %s", joined)
	}
}

func TestComposeMountsOnlyTheDedicatedHostShare(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, expected := range []string{
		"${LISAN_SHARED_DIR:-./shared}",
		"target: /home/fremen/shared",
		"io.lisanalgaib.volume",
		"io.lisanalgaib.network",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("Compose share/ownership metadata omits %q", expected)
		}
	}
	if strings.Contains(compose, "/var/run/docker.sock") {
		t.Fatal("Compose unexpectedly mounts the host Docker socket")
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
	for _, expected := range []string{`keys = "fr"`, "browse_filesystem", "lisan-file-browser.lua"} {
		if !strings.Contains(string(entrypoint), expected) {
			t.Fatalf("persistent NvChad migration omits %q", expected)
		}
	}
}

func TestDockerAgentTemplatesAreRecursivePreservingDropIns(t *testing.T) {
	root := filepath.Join("..", "..")
	script := filepath.Join(root, "docker", "lisan-seed-agent-assets")
	source := filepath.Join(t.TempDir(), "templates")
	destination := filepath.Join(t.TempDir(), "agents")

	for _, id := range appconfig.AgentIDs() {
		path := filepath.Join(source, id, "nested", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(destination, "opencode", "nested", "config.json")
	if err := os.MkdirAll(filepath.Dir(existing), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(source, "opencode", "nested", "deeper", "drop-in.json")
	if err := os.MkdirAll(filepath.Dir(added), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(added, []byte("new-template"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("/bin/sh", script, source, destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("seed Docker agent templates: %v: %s", err, output)
	}
	for _, id := range appconfig.AgentIDs() {
		data, err := os.ReadFile(filepath.Join(destination, id, "nested", "config.json"))
		if err != nil {
			t.Fatalf("%s nested drop-in was not copied: %v", id, err)
		}
		want := id
		if id == "opencode" {
			want = "user-owned"
		}
		if string(data) != want {
			t.Fatalf("%s nested drop-in = %q, want %q", id, data, want)
		}
	}
	if data, err := os.ReadFile(filepath.Join(destination, "opencode", "nested", "deeper", "drop-in.json")); err != nil || string(data) != "new-template" {
		t.Fatalf("new nested drop-in was not merged into an existing agent directory: %q %v", data, err)
	}

	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	entrypoint, err := os.ReadFile(filepath.Join(root, "docker", "lisan-entrypoint"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "COPY docker/home /usr/local/share/lisan/home") ||
		!strings.Contains(string(dockerfile), "COPY docker/lisan-seed-agent-assets /usr/local/bin/lisan-seed-agent-assets") ||
		!strings.Contains(string(entrypoint), `lisan-seed-agent-assets "$template/agents" /home/fremen/agents`) {
		t.Fatal("Docker runtime does not install and invoke the recursive agent seeder")
	}
}

func TestDockerAgentTemplateDirectoriesMatchCatalog(t *testing.T) {
	root := filepath.Join("..", "..", "docker", "home", "agents")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	directories := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			directories[entry.Name()] = true
		}
	}
	for _, id := range appconfig.AgentIDs() {
		if !directories[id] {
			t.Errorf("agent %q has no Docker drop-in directory", id)
		}
		delete(directories, id)
	}
	if len(directories) != 0 {
		t.Fatalf("Docker agent drop-in directories are not selectable: %#v", directories)
	}
}

func TestDockerFishPromptIsManagedAcrossExistingHomeVolumes(t *testing.T) {
	root := filepath.Join("..", "..")
	entrypoint, err := os.ReadFile(filepath.Join(root, "docker", "lisan-entrypoint"))
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(filepath.Join(root, "docker", "home", ".config", "fish", "conf.d", "lisan-prompt.fish"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"conf.d/lisan-prompt.fish", "cp \"$template/.config/fish/conf.d/lisan-prompt.fish\""} {
		if !strings.Contains(string(entrypoint), expected) {
			t.Fatalf("persistent Fish prompt migration omits %q", expected)
		}
	}
	for _, expected := range []string{"$prompt_user", "prompt_hostname", "prompt_pwd", "$last_status"} {
		if !strings.Contains(string(prompt), expected) {
			t.Fatalf("Fish prompt omits %q", expected)
		}
	}
}

func TestNvChadTreeTracksLaunchDirectoryAndOffersRootBrowser(t *testing.T) {
	root := filepath.Join("..", "..")
	picker, err := os.ReadFile(filepath.Join(root, "docker", "nvim", "lua", "lisan", "picker.lua"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"function M.toggle_tree()", "path = root", "update_root = true", "function M.browse_filesystem()", `return "/"`} {
		if !strings.Contains(string(picker), expected) {
			t.Fatalf("NvChad picker omits %q", expected)
		}
	}
	plugin, err := os.ReadFile(filepath.Join(root, "docker", "nvim", "lua", "plugins", "lisan-file-browser.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plugin), `"<C-n>"`) || !strings.Contains(string(plugin), "toggle_tree") || !strings.Contains(string(plugin), `"VimEnter"`) {
		t.Fatal("Ctrl-N is not bound to the current-directory-aware NvimTree toggle")
	}
	chadrc, err := os.ReadFile(filepath.Join(root, "docker", "nvim", "lua", "chadrc.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chadrc), `keys = "fr"`) || !strings.Contains(string(chadrc), "browse_filesystem") {
		t.Fatal("NvChad dashboard does not offer the filesystem-root browser")
	}
}

func TestNvChadCursorAnimationsArePinnedAndSynced(t *testing.T) {
	root := filepath.Join("..", "..")
	plugin, err := os.ReadFile(filepath.Join(root, "docker", "nvim", "lua", "plugins", "lisan-cursor.lua"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := string(plugin)
	for _, expected := range []string{
		`"gen740/SmoothCursor.nvim"`, `commit = "12518b284e1e3f7c6c703b346815968e1620bee2"`,
		`type = "default"`, `threshold = 3`,
		`"sphamba/smear-cursor.nvim"`, `commit = "9e9378d6ee34bb3782e0e8c63d9ec8ca618b479b"`,
		`cursor_color = "none"`, `smear_terminal_mode = false`,
	} {
		if !strings.Contains(configuration, expected) {
			t.Fatalf("NvChad cursor configuration omits %q", expected)
		}
	}

	lockData, err := os.ReadFile(filepath.Join(root, "docker", "nvim", "lazy-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]struct {
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SmoothCursor.nvim", "smear-cursor.nvim"} {
		if len(lock[name].Commit) != 40 {
			t.Fatalf("NvChad cursor plugin %s is not commit-pinned", name)
		}
	}

	entrypoint, err := os.ReadFile(filepath.Join(root, "docker", "lisan-entrypoint"))
	if err != nil {
		t.Fatal(err)
	}
	migration := string(entrypoint)
	for _, expected := range []string{
		"lua/plugins/lisan-cursor.lua",
		"lazy/SmoothCursor.nvim",
		"lazy/smear-cursor.nvim",
	} {
		if !strings.Contains(migration, expected) {
			t.Fatalf("persistent NvChad migration omits %q", expected)
		}
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

func TestDockerfileProvidesCodexSandboxPrerequisite(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	finalStage := strings.LastIndex(dockerfile, "\nFROM ")
	user := strings.LastIndex(dockerfile, "\nUSER fremen")
	if finalStage < 0 || user <= finalStage {
		t.Fatal("Dockerfile final runtime stage is missing")
	}
	runtimeRoot := dockerfile[finalStage:user]
	for _, expected := range []string{
		"ARG LISAN_INSTALL_CODEX=0",
		`if [ "$LISAN_INSTALL_CODEX" = 1 ]; then packages="$packages bubblewrap"; fi`,
		`if [ "$LISAN_INSTALL_CODEX" = 1 ]; then command -v bwrap >/dev/null; bwrap --version >/dev/null; fi`,
	} {
		if !strings.Contains(runtimeRoot, expected) {
			t.Fatalf("Codex runtime sandbox setup omits %q", expected)
		}
	}
}

func TestDockerfileAgentPayloadsNeverInstallWithNPM(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	start := strings.Index(dockerfile, " AS mentat_payloads")
	if start < 0 {
		t.Fatal("Dockerfile agent payload stage is missing")
	}
	endOffset := strings.Index(dockerfile[start+1:], "\nFROM ")
	if endOffset < 0 {
		t.Fatal("Dockerfile agent payload stage has no following stage")
	}
	payloadStage := dockerfile[start : start+1+endOffset]
	if strings.Contains(strings.ToLower(payloadStage), "npm") {
		t.Fatalf("Docker agent payload stage still uses npm:\n%s", payloadStage)
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
		"OMP_SHA256_AMD64", "OMP_SHA256_ARM64",
	} {
		if !strings.Contains(dockerfile, "ARG "+argument+"=") {
			t.Fatalf("Dockerfile does not pin %s", argument)
		}
	}
	if strings.Contains(dockerfile, "manifest.json") || strings.Contains(dockerfile, " jq") {
		t.Fatal("agent builder still trusts live manifests or installs jq")
	}
	ompStart := strings.Index(dockerfile, `if [ "$LISAN_INSTALL_OMP" = 1 ]`)
	if ompStart < 0 {
		t.Fatal("Dockerfile Oh My Pi install block is missing")
	}
	ompBlock := dockerfile[ompStart:]
	if !strings.Contains(ompBlock, "github.com/can1357/oh-my-pi/releases/download") ||
		!strings.Contains(ompBlock, "omp-linux-${agent_arch}") ||
		strings.Contains(ompBlock, "omp-linux-musl-") ||
		!strings.Contains(ompBlock, "sha256sum -c") ||
		!strings.Contains(ompBlock, "/opt/lisan-agents/bin/omp --version") {
		t.Fatalf("Oh My Pi must be a verified, build-tested glibc download:\n%s", ompBlock)
	}
	for _, checksum := range []string{
		"OMP_SHA256_AMD64=6c75331bf09d5a9e9433bd592b3ee993d751a15d5b7450c1a334cc0684996f30",
		"OMP_SHA256_ARM64=f176edf8174db252abe1aa6e84df284e1b83b8dd7ef34ac7faf7884a5e172a4c",
	} {
		if !strings.Contains(dockerfile, checksum) {
			t.Fatalf("Dockerfile does not pin the compatible Oh My Pi payload: %s", checksum)
		}
	}
	copyIndex := strings.Index(dockerfile, "COPY --from=mentat_payloads /opt/lisan-agents /opt/lisan-agents")
	runtimeCheck := strings.LastIndex(dockerfile, "/opt/lisan-agents/bin/omp --version")
	if copyIndex < 0 || runtimeCheck <= copyIndex {
		t.Fatal("final Docker runtime does not execute-check the copied Oh My Pi binary")
	}
}

func TestDockerfilesPinPatchedMultiArchitectureBaseImages(t *testing.T) {
	const goBuilder = "FROM golang:1.26.5-bookworm@sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599"
	const workspace = "FROM ubuntu:26.04@sha256:678c6550cc43645e08669028bc177f50be4e7c5b8cca677067b1914d4afc7a03"
	path := filepath.Join("..", "..", "Dockerfile")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{goBuilder, workspace} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("%s does not pin %q", path, required)
		}
	}
}

func TestDockerBuildArgumentsContainResolvedPlan(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Terminal.DockerShell = "zsh"
	profile.Set(appconfig.Tools, "go", true)
	profile.Set(appconfig.Tools, "jq", true)
	joined := strings.Join(resolveDockerBuildPlan(profile).buildArguments("digest"), " ")
	for _, expected := range []string{"LISAN_BUILD_SIGNATURE=digest", "LISAN_DOCKER_SHELL=zsh", "LISAN_SHELL_PATH=/usr/bin/zsh", "LISAN_INSTALL_GO=1", "LISAN_INSTALL_NODE=0", "LISAN_INSTALL_EXTRA_PACKAGES=jq"} {
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

func TestAgentConfigDropInInvalidatesDockerBuildSignature(t *testing.T) {
	root := dockerBuildFixture(t)
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	before, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "docker", "home", "agents", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := dockerBuildSignature(root, resolveDockerBuildPlan(profile))
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("agent configuration drop-in did not invalidate the Docker image signature")
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
	for _, file := range []string{".dockerignore", "Dockerfile", "go.mod", "go.sum", "docker/lisan-entrypoint", "docker/lisan-seed-agent-assets"} {
		path := filepath.Join(root, file)
		if err := os.WriteFile(path, []byte(file+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
