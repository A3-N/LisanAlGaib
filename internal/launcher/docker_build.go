package launcher

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/runtimebundle"
)

type dockerBuildPlan struct {
	Shell         string
	ShellPath     string
	Git           bool
	Ripgrep       bool
	Neovim        bool
	NvChad        bool
	Node          bool
	Go            bool
	Python        bool
	ExtraPackages []string
	Codex         bool
	OpenCode      bool
	Claude        bool
	Kimi          bool
}

var dockerExtraPackages = map[string][]string{
	"pip":        {"python3-pip"},
	"rust":       {"cargo", "rustc"},
	"java":       {"default-jdk-headless"},
	"clang":      {"clang"},
	"ruby":       {"ruby-full"},
	"php":        {"php-cli"},
	"lua":        {"lua5.4"},
	"curl":       {"curl"},
	"jq":         {"jq"},
	"wget":       {"wget"},
	"zip":        {"zip"},
	"fd":         {"fd-find"},
	"fzf":        {"fzf"},
	"bat":        {"bat"},
	"tree":       {"tree"},
	"shellcheck": {"shellcheck"},
	"ip":         {"iproute2"},
	"ping":       {"iputils-ping"},
	"dns":        {"dnsutils"},
	"net-tools":  {"net-tools"},
	"traceroute": {"traceroute"},
	"netcat":     {"netcat-openbsd"},
	"nmap":       {"nmap"},
	"mtr":        {"mtr-tiny"},
	"tcpdump":    {"tcpdump"},
	"whois":      {"whois"},
}

func resolveDockerBuildPlan(profile appconfig.Profile) dockerBuildPlan {
	plan := dockerBuildPlan{Shell: profile.Terminal.DockerShell}
	plan.ShellPath = map[string]string{
		"fish": "/usr/bin/fish",
		"bash": "/usr/bin/bash",
		"zsh":  "/usr/bin/zsh",
		"sh":   "/bin/sh",
	}[plan.Shell]

	plan.NvChad = profile.Feature("files") || profile.Tool("nvchad")
	plan.Neovim = profile.Feature("files") || profile.Tool("nvim") || plan.NvChad
	plan.Git = profile.Feature("files") || profile.Tool("git") || plan.NvChad
	plan.Ripgrep = profile.Feature("files") || profile.Tool("rg") || plan.NvChad
	plan.Node = profile.Tool("node")
	plan.Go = profile.Tool("go")
	plan.Python = profile.Tool("python")
	extraPackages := map[string]bool{}
	for id, packages := range dockerExtraPackages {
		if !profile.Tool(id) {
			continue
		}
		for _, name := range packages {
			extraPackages[name] = true
		}
	}
	for name := range extraPackages {
		plan.ExtraPackages = append(plan.ExtraPackages, name)
	}
	sort.Strings(plan.ExtraPackages)

	if profile.Feature("agents") {
		plan.Codex = profile.Agent("codex")
		plan.OpenCode = profile.Agent("opencode")
		plan.Claude = profile.Agent("claude")
		plan.Kimi = profile.Agent("kimi")
	}
	return plan
}

func (plan dockerBuildPlan) buildArguments(signature string) []string {
	values := []struct {
		name  string
		value string
	}{
		{"LISAN_BUILD_SIGNATURE", signature},
		{"LISAN_DOCKER_SHELL", plan.Shell},
		{"LISAN_SHELL_PATH", plan.ShellPath},
		{"LISAN_INSTALL_GIT", buildBool(plan.Git)},
		{"LISAN_INSTALL_RG", buildBool(plan.Ripgrep)},
		{"LISAN_INSTALL_NVIM", buildBool(plan.Neovim)},
		{"LISAN_INSTALL_NVCHAD", buildBool(plan.NvChad)},
		{"LISAN_INSTALL_NODE", buildBool(plan.Node)},
		{"LISAN_INSTALL_GO", buildBool(plan.Go)},
		{"LISAN_INSTALL_PYTHON", buildBool(plan.Python)},
		{"LISAN_INSTALL_EXTRA_PACKAGES", strings.Join(plan.ExtraPackages, " ")},
		{"LISAN_INSTALL_CODEX", buildBool(plan.Codex)},
		{"LISAN_INSTALL_OPENCODE", buildBool(plan.OpenCode)},
		{"LISAN_INSTALL_CLAUDE", buildBool(plan.Claude)},
		{"LISAN_INSTALL_KIMI", buildBool(plan.Kimi)},
	}
	arguments := []string{"build"}
	for _, value := range values {
		arguments = append(arguments, "--build-arg", value.name+"="+value.value)
	}
	return append(arguments, "workspace")
}

func buildBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func dockerBuildSignature(runtimeRoot string, plan dockerBuildPlan) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%#v\n", plan)
	paths := []string{".dockerignore", "Dockerfile", "go.mod", "go.sum", "cmd", "internal", "docker/lisan-entrypoint", "docker/nvim", "docker/home"}
	if err := writeRuntimeFingerprint(hash, runtimeRoot, paths); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// writeRuntimeFingerprint hashes a deterministic, runtime-relative view of
// build inputs. It is shared by the workspace and extension cache keys so the
// two image paths cannot drift into subtly different file handling.
func writeRuntimeFingerprint(output io.Writer, runtimeRoot string, paths []string) error {
	var files []string
	seen := map[string]bool{}
	for _, relative := range paths {
		path := filepath.Join(runtimeRoot, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("fingerprint Docker build input %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fingerprint Docker build input %s: symbolic links are not supported", relative)
		}
		if info.Mode().IsRegular() {
			if runtimebundle.IncludeSource(relative, false) && !seen[path] {
				files = append(files, path)
				seen[path] = true
			}
			continue
		}
		err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			runtimeRelative, err := filepath.Rel(runtimeRoot, candidate)
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
				return fmt.Errorf("fingerprint Docker build input %s: symbolic links are not supported", runtimeRelative)
			}
			if entry.Type().IsRegular() && !seen[candidate] {
				files = append(files, candidate)
				seen[candidate] = true
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("fingerprint Docker build input %s: %w", relative, err)
		}
	}
	sort.Strings(files)
	for _, path := range files {
		relative, err := filepath.Rel(runtimeRoot, path)
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("docker build input escaped runtime root: %s", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s %o\n", filepath.ToSlash(relative), info.Mode().Perm())
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
