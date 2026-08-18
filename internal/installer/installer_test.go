package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestSyncLisanNvimAssetsRefreshesManagedModules(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nvim")
	chadrc := filepath.Join(target, "lua", "chadrc.lua")
	if err := os.MkdirAll(filepath.Dir(chadrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chadrc, []byte("local M = {}\nM.nvdash = {\n  buttons = {\n    { txt = \"Open File\", keys = \"ff\", cmd = \"lua require('lisan.picker').open_file()\" },\n  },\n}\nreturn M\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncLisanNvimAssets(filepath.Join("..", ".."), target); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(chadrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "lisan.header") {
		t.Fatalf("NvChad config did not receive the shared header: %s", updated)
	}
	if !strings.Contains(string(updated), `keys = "fr"`) || !strings.Contains(string(updated), "browse_filesystem") {
		t.Fatalf("NvChad dashboard did not receive the filesystem-root browser: %s", updated)
	}
	if err := syncLisanNvimAssets(filepath.Join("..", ".."), target); err != nil {
		t.Fatal(err)
	}
	idempotent, err := os.ReadFile(chadrc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(idempotent), `keys = "fr"`) != 1 {
		t.Fatalf("NvChad dashboard migration duplicated its filesystem-root action: %s", idempotent)
	}
	headerPath := filepath.Join(target, "lua", "lisan", "header.lua")
	header, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("NvChad header loader was not installed: %v", err)
	}
	if !strings.Contains(string(header), "LISAN AL-GAIB") || !strings.Contains(string(header), "Powered by NvChad") {
		t.Fatal("NvChad header does not declare the LisanAlGaib wordmark")
	}
	picker, err := os.ReadFile(filepath.Join(target, "lua", "lisan", "picker.lua"))
	if err != nil {
		t.Fatalf("NvChad workspace picker was not refreshed: %v", err)
	}
	if strings.Contains(string(picker), `vim.cmd "enew"`) || !strings.Contains(string(picker), `tree.open { path = path }`) {
		t.Fatal("NvChad picker does not open NvimTree as the selected workspace view")
	}
	if !strings.Contains(string(picker), "browse_filesystem") || !strings.Contains(string(picker), "update_root = true") {
		t.Fatal("NvChad picker does not browse the filesystem root or track Neovim's current directory")
	}
	fileBrowser, err := os.ReadFile(filepath.Join(target, "lua", "plugins", "lisan-file-browser.lua"))
	if err != nil {
		t.Fatalf("NvChad file-browser plugin declaration was not refreshed: %v", err)
	}
	if !strings.Contains(string(fileBrowser), `"<C-n>"`) || !strings.Contains(string(fileBrowser), "toggle_tree") || !strings.Contains(string(fileBrowser), `"VimEnter"`) {
		t.Fatal("NvChad Ctrl-N does not use Lisan's current-directory tree toggle")
	}
	if cursor, err := os.ReadFile(filepath.Join(target, "lua", "plugins", "lisan-cursor.lua")); err != nil {
		t.Fatalf("NvChad cursor plugin declaration was not refreshed: %v", err)
	} else if !strings.Contains(string(cursor), "SmoothCursor.nvim") || !strings.Contains(string(cursor), "smear-cursor.nvim") {
		t.Fatal("NvChad cursor plugin declaration is incomplete")
	}
}

func TestUserAssetsFollowActiveProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	config := filepath.Join(t.TempDir(), "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "agents", true)
	profile.Set(appconfig.Agents, "omp", true)
	if err := SeedUserAssets(filepath.Join("..", ".."), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "omp", "AGENTS.md")); err != nil {
		t.Fatalf("selected Oh My Pi workspace was not seeded: %v", err)
	}
	for _, absent := range []string{
		filepath.Join(home, "agents", "kimi"),
		filepath.Join(home, "agents", "codex"),
		filepath.Join(config, "nvim"),
	} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Fatalf("unselected host asset was seeded: %s: %v", absent, err)
		}
	}
}

func TestEverySelectedAgentReceivesRecursiveDropInsWithoutOverwrite(t *testing.T) {
	sourceRoot := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	config := filepath.Join(t.TempDir(), "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)

	profile := appconfig.ProfileFromPreset(appconfig.Presets[3], time.Now())
	profile.Set(appconfig.Features, "agents", true)
	for _, id := range appconfig.AgentIDs() {
		profile.Set(appconfig.Agents, id, true)
		path := filepath.Join(sourceRoot, "docker", "home", "agents", id, "nested", "config.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	existing := filepath.Join(home, "agents", "codex", "nested", "config.json")
	if err := os.MkdirAll(filepath.Dir(existing), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SeedUserAssets(sourceRoot, profile); err != nil {
		t.Fatal(err)
	}
	for _, id := range appconfig.AgentIDs() {
		data, err := os.ReadFile(filepath.Join(home, "agents", id, "nested", "config.json"))
		if err != nil {
			t.Fatalf("%s nested drop-in was not seeded: %v", id, err)
		}
		want := id
		if id == "codex" {
			want = "user-owned"
		}
		if string(data) != want {
			t.Fatalf("%s nested drop-in = %q, want %q", id, data, want)
		}
	}
}

func TestInstallRuntimeReplacesStaleFiles(t *testing.T) {
	root := t.TempDir()
	makeRuntimeFixture(t, root)
	destination := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(filepath.Join(destination, "docker", "connectors", "removed-example"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(destination, "docker", "connectors", "removed-example", "extension.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := installRuntime(root, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale runtime file survived replacement: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "go.mod")); err != nil {
		t.Fatalf("new runtime file was not installed: %v", err)
	}
}

func TestInstallRuntimeExcludesBuildOnlySource(t *testing.T) {
	root := t.TempDir()
	makeRuntimeFixture(t, root)
	archive := filepath.Join(root, "internal", "runtimebundle", "assets", "runtime.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("nested release"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{
		filepath.Join(root, "internal", "source_test.go"),
		filepath.Join(root, "internal", "source_windows.go"),
		filepath.Join(root, "cmd", "lisan-runtime-pack", "main.go"),
	} {
		if err := os.MkdirAll(filepath.Dir(excluded), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(excluded, []byte("package excluded\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "runtime")
	if err := installRuntime(root, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "internal", "runtimebundle", "assets", "runtime.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("generated archive was copied into installed runtime: %v", err)
	}
	for _, excluded := range []string{
		filepath.Join(destination, "internal", "source_test.go"),
		filepath.Join(destination, "internal", "source_windows.go"),
		filepath.Join(destination, "cmd", "lisan-runtime-pack"),
	} {
		if _, err := os.Stat(excluded); !os.IsNotExist(err) {
			t.Fatalf("build-only source was installed: %s: %v", excluded, err)
		}
	}
}

func TestUninstallPreservesConfigAndRemovesInstalledFiles(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "config.json")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv(appconfig.EnvironmentConfig, configPath)
	paths, err := nativePaths()
	if err != nil {
		t.Fatal(err)
	}
	document := appconfig.DefaultDocument(time.Now())
	document.RuntimeRoot = paths.runtime
	if err := appconfig.Save(configPath, document); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(document.RuntimeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(nil); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{paths.binary, document.RuntimeRoot} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("installed path survived uninstall: %s: %v", removed, err)
		}
	}
	loaded, err := appconfig.Load(configPath)
	if err != nil {
		t.Fatalf("preserved config cannot be loaded: %v", err)
	}
	if loaded.RuntimeRoot != "" || len(loaded.Profiles) == 0 {
		t.Fatalf("uninstall damaged preserved config: %#v", loaded)
	}
}

func TestUninstallContinuesWhenPreservedConfigIsInvalid(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv(appconfig.EnvironmentConfig, filepath.Join(root, "config", "config.json"))
	paths, err := nativePaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Dir(paths.binary), paths.runtime, filepath.Dir(paths.config)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.config, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(nil); err == nil {
		t.Fatal("invalid preserved config did not produce a diagnostic")
	}
	for _, removed := range []string{paths.binary, paths.runtime} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("uninstall stopped before removing %s: %v", removed, err)
		}
	}
	if _, err := os.Stat(paths.config); err != nil {
		t.Fatalf("invalid user config was not preserved: %v", err)
	}
}

func TestRuntimeCopyRejectsSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(target, []byte("not runtime data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(root, filepath.Join(t.TempDir(), "copy")); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("runtime symlink was followed: %v", err)
	}
}

func TestSourceRuntimeIsDiscoveredFromAncestor(t *testing.T) {
	root := t.TempDir()
	makeRuntimeFixture(t, root)
	nested := filepath.Join(root, "nested", "workspace")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	oldWorking, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorking) })
	discovered, err := resolveSourceRoot()
	if err != nil || discovered != root {
		t.Fatalf("source runtime discovery = %q, %v; want %q", discovered, err, root)
	}
}

func makeRuntimeFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		".dockerignore": "dist\n",
		"Dockerfile":    "FROM scratch\n",
		"compose.yaml":  "services: {}\n",
		"go.mod":        "module example\n",
		"go.sum":        "",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"cmd", "internal", "docker"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
