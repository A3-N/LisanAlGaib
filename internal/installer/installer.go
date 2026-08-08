package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/cliout"
	"lisanalgaib/internal/nvimconfig"
	"lisanalgaib/internal/runtimebundle"
	"lisanalgaib/internal/safefile"
)

type installPaths struct {
	config  string
	binary  string
	runtime string
}

func Install(output io.Writer) (string, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return "", fmt.Errorf("unsupported operating system %s", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	if output == nil {
		output = io.Discard
	}
	paths, err := nativePaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(paths.binary), 0o755); err != nil {
		return "", fmt.Errorf("create binary directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.config), 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	if runtimebundle.Available() {
		if err := installEmbeddedRuntime(paths.runtime); err != nil {
			return "", err
		}
	} else {
		sourceRoot, err := resolveSourceRoot()
		if err != nil {
			return "", err
		}
		if sourceRoot == "" {
			return "", errors.New("this development binary has no embedded runtime; run it from the LisanAlGaib source checkout")
		}
		if err := installRuntime(sourceRoot, paths.runtime); err != nil {
			return "", err
		}
	}
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate running executable: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	if same, sameErr := sameFile(binary, paths.binary); sameErr != nil {
		return "", sameErr
	} else if !same {
		if err := copyFile(binary, paths.binary, 0o755); err != nil {
			return "", fmt.Errorf("install lisan binary: %w", err)
		}
	}
	document, err := appconfig.Load(paths.config)
	if err != nil {
		return "", err
	}
	document.RuntimeRoot = paths.runtime
	if err := appconfig.Save(paths.config, document); err != nil {
		return "", err
	}
	cliout.Success(output, "Installing LisanAlGaib")
	cliout.Detail(output, "platform", runtime.GOOS+"/"+runtime.GOARCH)
	cliout.Detail(output, "binary", paths.binary)
	cliout.Detail(output, "config", paths.config)
	return paths.binary, nil
}

// Uninstall removes the installed executable and runtime bundle. User config,
// profiles, agent workspaces, editor config, and Docker state are preserved.
func Uninstall(output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	paths, err := nativePaths()
	if err != nil {
		return err
	}
	var result error
	if err := os.RemoveAll(paths.runtime); err != nil {
		result = errors.Join(result, fmt.Errorf("remove installed runtime: %w", err))
	}
	if info, statErr := os.Stat(paths.config); statErr == nil && info.Mode().IsRegular() {
		document, loadErr := appconfig.Load(paths.config)
		if loadErr != nil {
			result = errors.Join(result, loadErr)
		} else {
			document.RuntimeRoot = ""
			if saveErr := appconfig.Save(paths.config, document); saveErr != nil {
				result = errors.Join(result, saveErr)
			}
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		result = errors.Join(result, fmt.Errorf("inspect config: %w", statErr))
	}
	if err := removeInstalledExecutable(paths.binary); err != nil {
		result = errors.Join(result, fmt.Errorf("remove installed executable: %w", err))
	}
	if result != nil {
		return result
	}
	cliout.Success(output, "Uninstalling LisanAlGaib")
	cliout.Detail(output, "removed", paths.binary)
	cliout.Detail(output, "removed", paths.runtime)
	cliout.Detail(output, "preserved", paths.config+", user assets, and Docker state")
	return nil
}

func nativePaths() (installPaths, error) {
	configPath, err := appconfig.ConfigPath()
	if err != nil {
		return installPaths{}, err
	}
	binDir, err := installBinDir()
	if err != nil {
		return installPaths{}, err
	}
	binaryName := "lisan"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	return installPaths{
		config:  configPath,
		binary:  filepath.Join(binDir, binaryName),
		runtime: filepath.Join(filepath.Dir(configPath), "runtime"),
	}, nil
}

func installBinDir() (string, error) {
	if runtime.GOOS == "windows" {
		root := os.Getenv("LOCALAPPDATA")
		if root == "" {
			var err error
			root, err = os.UserConfigDir()
			if err != nil {
				return "", err
			}
		}
		return filepath.Join(root, "Programs", "LisanAlGaib"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func resolveSourceRoot() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate source checkout: %w", err)
	}
	seeds := []string{working}
	if executable, executableErr := os.Executable(); executableErr == nil {
		seeds = append(seeds, filepath.Dir(executable))
	}
	visited := map[string]bool{}
	for _, seed := range seeds {
		for candidate := seed; !visited[candidate]; candidate = filepath.Dir(candidate) {
			visited[candidate] = true
			if len(missingRuntimePaths(candidate)) == 0 {
				return candidate, nil
			}
			if candidate == filepath.Dir(candidate) {
				break
			}
		}
	}
	return "", nil
}

func installEmbeddedRuntime(destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".runtime-staging-*")
	if err != nil {
		return fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := runtimebundle.Extract(staging); err != nil {
		return fmt.Errorf("extract embedded runtime: %w", err)
	}
	return replaceDirectory(staging, destination)
}

func installRuntime(sourceRoot, destination string) error {
	if missing := missingRuntimePaths(sourceRoot); len(missing) > 0 {
		return fmt.Errorf("runtime source is missing: %s", strings.Join(missing, ", "))
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".runtime-staging-*")
	if err != nil {
		return fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	for _, relative := range runtimebundle.SourceRoots() {
		source := filepath.Join(sourceRoot, relative)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := copyRuntimePath(sourceRoot, relative, staging); err != nil {
			return fmt.Errorf("install runtime %s: %w", relative, err)
		}
	}
	return replaceDirectory(staging, destination)
}

func copyRuntimePath(sourceRoot, relative, destinationRoot string) error {
	source := filepath.Join(sourceRoot, relative)
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		runtimeRelative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if !runtimebundle.IncludeSource(runtimeRelative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symbolic link in runtime source: %s", path)
		}
		target := filepath.Join(destinationRoot, runtimeRelative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func replaceDirectory(staging, destination string) error {
	backup := ""
	if _, err := os.Stat(destination); err == nil {
		reserved, reserveErr := os.CreateTemp(filepath.Dir(destination), ".runtime-backup-*")
		if reserveErr != nil {
			return fmt.Errorf("reserve runtime backup path: %w", reserveErr)
		}
		backup = reserved.Name()
		if closeErr := reserved.Close(); closeErr != nil {
			return closeErr
		}
		if removeErr := os.Remove(backup); removeErr != nil {
			return removeErr
		}
		if renameErr := os.Rename(destination, backup); renameErr != nil {
			return fmt.Errorf("back up installed runtime: %w", renameErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect installed runtime: %w", err)
	}

	if err := os.Rename(staging, destination); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				return errors.Join(fmt.Errorf("activate installed runtime: %w", err), fmt.Errorf("restore previous runtime: %w", restoreErr))
			}
		}
		return fmt.Errorf("activate installed runtime: %w", err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove previous runtime: %w", err)
		}
	}
	return nil
}

func seedNvimAssets(sourceRoot string) error {
	nvimTarget, err := nvimconfig.ConfigDir()
	if err != nil {
		return err
	}
	nvimSource := filepath.Join(sourceRoot, "docker", "nvim")
	if _, err := os.Stat(nvimTarget); errors.Is(err, os.ErrNotExist) {
		if err := copyTree(nvimSource, nvimTarget); err != nil {
			return fmt.Errorf("seed NvChad config: %w", err)
		}
	}
	if err := syncLisanNvimAssets(sourceRoot, nvimTarget); err != nil {
		return fmt.Errorf("refresh Lisan NvChad assets: %w", err)
	}
	return nil
}

func seedAgentAssets(sourceRoot string, profile appconfig.Profile) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, id := range []string{"codex", "opencode", "claude", "kimi"} {
		if !profile.Agent(id) {
			continue
		}
		source := filepath.Join(sourceRoot, "docker", "home", "agents", id)
		destination := filepath.Join(home, "agents", id)
		if err := seedTree(source, destination); err != nil {
			return fmt.Errorf("seed %s agent workspace: %w", id, err)
		}
	}
	return nil
}

func syncLisanNvimAssets(sourceRoot, nvimTarget string) error {
	chadrc := filepath.Join(nvimTarget, "lua", "chadrc.lua")
	data, err := os.ReadFile(chadrc)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// Existing unrelated Neovim configurations remain untouched. The picker
	// module identifies a dashboard previously seeded by Lisan.
	if !strings.Contains(string(data), "lisan.picker") {
		return nil
	}
	assets := []struct{ source, destination string }{
		{filepath.Join(sourceRoot, "docker", "nvim", "lua", "lisan", "header.lua"), filepath.Join(nvimTarget, "lua", "lisan", "header.lua")},
		{filepath.Join(sourceRoot, "docker", "nvim", "lua", "lisan", "picker.lua"), filepath.Join(nvimTarget, "lua", "lisan", "picker.lua")},
		{filepath.Join(sourceRoot, "docker", "nvim", "lua", "plugins", "lisan-cursor.lua"), filepath.Join(nvimTarget, "lua", "plugins", "lisan-cursor.lua")},
		{filepath.Join(sourceRoot, "docker", "nvim", "lua", "plugins", "lisan-file-browser.lua"), filepath.Join(nvimTarget, "lua", "plugins", "lisan-file-browser.lua")},
	}
	for _, asset := range assets {
		if err := copyFile(asset.source, asset.destination, 0o644); err != nil {
			return err
		}
	}
	if strings.Contains(string(data), "lisan.header") {
		return nil
	}
	updated := strings.Replace(string(data), "M.nvdash = {", "M.nvdash = {\n  header = function() return require(\"lisan.header\").lines() end,", 1)
	if updated == string(data) {
		return errors.New("Lisan NvChad config has no nvdash block")
	}
	return safefile.Write(chadrc, []byte(updated), 0o755, 0o644)
}

// SeedUserAssets installs only user assets selected by the active Wormsign
// profile. Unrelated existing Neovim and agent files remain untouched.
func SeedUserAssets(sourceRoot string, profile appconfig.Profile) error {
	if sourceRoot == "" {
		return errors.New("runtime root is required to seed user assets")
	}
	if profile.Feature("files") || profile.Tool("nvchad") {
		if err := seedNvimAssets(sourceRoot); err != nil {
			return err
		}
	}
	if profile.Feature("agents") {
		if err := seedAgentAssets(sourceRoot, profile); err != nil {
			return err
		}
	}
	return nil
}

func seedTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symbolic link in user asset source: %s", path)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symbolic link in runtime source: %s", source)
	}
	if !info.IsDir() {
		return copyFile(source, destination, info.Mode().Perm())
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symbolic link in runtime source: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	temporary := output.Name()
	defer os.Remove(temporary)
	if err := output.Chmod(mode); err != nil {
		_ = output.Close()
		return err
	}
	if _, err = io.Copy(output, input); err == nil {
		err = output.Close()
	} else {
		_ = output.Close()
	}
	if err != nil {
		return err
	}
	if err := safefile.Replace(temporary, destination); err != nil {
		return err
	}
	return nil
}

func sameFile(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(second)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(firstInfo, secondInfo), nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func missingRuntimePaths(root string) []string {
	var missing []string
	for _, path := range []string{".dockerignore", "Dockerfile", "compose.yaml", "go.mod", "go.sum"} {
		if !isFile(filepath.Join(root, path)) {
			missing = append(missing, path)
		}
	}
	for _, path := range []string{"cmd", "internal", "docker"} {
		if !isDirectory(filepath.Join(root, path)) {
			missing = append(missing, path+string(filepath.Separator))
		}
	}
	return missing
}

func PathAdvice(binary string) string {
	directory := filepath.Clean(filepath.Dir(binary))
	for _, item := range filepath.SplitList(os.Getenv("PATH")) {
		item = filepath.Clean(item)
		if item == directory || (runtime.GOOS == "windows" && strings.EqualFold(item, directory)) {
			return ""
		}
	}
	if runtime.GOOS == "windows" {
		return "Add " + directory + " to your user PATH, then open a new terminal."
	}
	return "Add " + directory + " to PATH (for example in your shell profile)."
}
