package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"lisanalgaib/internal/appconfig"
)

const (
	workspaceContainer            = "sietch-tabr"
	workspaceImage                = "lisanalgaib:latest"
	workspaceVolume               = "arrakis-usul"
	workspaceNetwork              = "arrakis-shield-wall"
	legacyExtensionControlNetwork = "arrakis-extension-control"
	sharedDirectoryEnvironment    = "LISAN_SHARED_DIR"
)

type DockerOptions struct {
	RuntimeRoot string
	Workspace   string
	Profile     appconfig.Profile
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

func RunDocker(ctx context.Context, options DockerOptions) (resultErr error) {
	profile, err := appconfig.NormalizeProfile(options.Profile)
	if err != nil {
		return err
	}
	options.Profile = profile
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker is required for docker mode; install Docker Desktop/Engine or choose vm")
	}
	root, err := resolveRuntimeRoot(options.RuntimeRoot)
	if err != nil {
		return err
	}
	sharedRoot, err := PrepareSharedDirectory(root, options.RuntimeRoot != "")
	if err != nil {
		return err
	}
	compose := []string{"compose", "--project-directory", root, "-f", filepath.Join(root, "compose.yaml")}
	composeCommand := func(arguments ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, "docker", append(compose, arguments...)...)
		command.Env = append(os.Environ(), sharedDirectoryEnvironment+"="+sharedRoot)
		command.Stdin = options.Stdin
		return command
	}
	runInteractive := func(arguments ...string) error {
		command := composeCommand(arguments...)
		command.Stdout, command.Stderr = options.Stdout, options.Stderr
		return command.Run()
	}
	info := exec.CommandContext(ctx, "docker", "info")
	info.Stdout, info.Stderr = io.Discard, options.Stderr
	if err := info.Run(); err != nil {
		return errors.New("docker is installed but its daemon is unavailable; start Docker Desktop/Engine and retry")
	}
	buildPlan := resolveDockerBuildPlan(options.Profile)
	buildSignature, err := dockerBuildSignature(root, buildPlan)
	if err != nil {
		return err
	}
	imageSignature, inspectErr := dockerOutput(ctx, "image", "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.build-signature"}}`, workspaceImage)
	rebuilt := inspectErr != nil || strings.TrimSpace(imageSignature) != buildSignature
	if rebuilt {
		buildArguments := buildPlan.buildArguments(buildSignature)
		structured := append([]string{"--progress=json"}, buildArguments...)
		if err := runDockerProgress(options.Stderr, "Building Sietch Tabr", composeCommand(structured...), composeCommand(buildArguments...)); err != nil {
			return fmt.Errorf("build configured Docker workspace: %w", err)
		}
	}
	upArguments := []string{"up", "-d", "--no-build"}
	if rebuilt {
		upArguments = append(upArguments, "--force-recreate")
	}
	upArguments = append(upArguments, "workspace")
	structuredUp := append([]string{"--progress=json"}, upArguments...)
	if err := runDockerProgress(options.Stderr, "Starting Sietch Tabr", composeCommand(structuredUp...), composeCommand(upArguments...)); err != nil {
		return fmt.Errorf("start configured Docker workspace: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := cleanupDockerSession(cleanupCtx, options.Stdout, options.Stderr); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup launched Docker services: %w", err))
		}
	}()
	if err := syncConnectors(ctx, root, sharedRoot, options.Profile.Connectors, options.Stdout, options.Stderr); err != nil {
		return err
	}
	encoded, err := appconfig.EncodeProfile(options.Profile)
	if err != nil {
		return err
	}
	arguments := dockerExecArguments(options, encoded)
	user := nonempty(options.Profile.Terminal.DockerUser, "fremen")
	if err := runInteractive(arguments...); err != nil {
		return fmt.Errorf("run Lisan as %s in Docker: %w", user, err)
	}
	return nil
}

// PrepareSharedDirectory keeps exchange data beside a release runtime so a
// later install can atomically replace the runtime without touching user files.
// Source checkouts use the requested root-level shared directory directly.
func PrepareSharedDirectory(runtimeRoot string, installedRuntime bool) (string, error) {
	sharedRoot := filepath.Join(runtimeRoot, "shared")
	if installedRuntime {
		sharedRoot = filepath.Join(filepath.Dir(runtimeRoot), "shared")
	}
	sharedRoot, err := filepath.Abs(sharedRoot)
	if err != nil {
		return "", fmt.Errorf("resolve shared directory: %w", err)
	}
	if strings.ContainsRune(sharedRoot, ',') || strings.IndexFunc(sharedRoot, func(r rune) bool { return r < ' ' || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("shared directory path contains characters unsupported by Docker mounts: %s", sharedRoot)
	}
	if info, err := os.Lstat(sharedRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("shared path must be a real directory, not a link or file: %s", sharedRoot)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect shared directory: %w", err)
	}
	if err := os.MkdirAll(sharedRoot, 0o700); err != nil {
		return "", fmt.Errorf("create shared directory: %w", err)
	}
	if err := os.Chmod(sharedRoot, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return "", fmt.Errorf("secure shared directory permissions: %w", err)
	}
	return sharedRoot, nil
}

func containerRunning(ctx context.Context, name string) bool {
	value, err := dockerOutput(ctx, "inspect", "--format", `{{.State.Running}}`, name)
	return err == nil && strings.TrimSpace(value) == "true"
}

func dockerExecArguments(options DockerOptions, encoded string) []string {
	user := nonempty(options.Profile.Terminal.DockerUser, "fremen")
	workdir := nonempty(options.Profile.Terminal.DockerWorkdir, "/home/fremen")
	arguments := []string{
		"exec", "-e", "TERM=" + nonempty(os.Getenv("TERM"), "xterm-256color"),
		"-e", "COLORTERM=" + nonempty(os.Getenv("COLORTERM"), "truecolor"),
		"-e", appconfig.EnvironmentProfile + "=" + encoded,
		"-e", "LISAN_SHARED_DIR=/home/fremen/shared",
		"-e", "HOME=" + dockerUserHome(user),
		"--user", user, "--workdir", workdir, "workspace", "lisan", "run",
	}
	if strings.TrimSpace(options.Workspace) != "" {
		arguments = append(arguments, options.Workspace)
	}
	return arguments
}

func dockerUserHome(user string) string {
	if user == "root" {
		return "/root"
	}
	return "/home/" + user
}

func resolveRuntimeRoot(configured string) (string, error) {
	if configured != "" {
		if isFile(filepath.Join(configured, "compose.yaml")) && isFile(filepath.Join(configured, "Dockerfile")) {
			return configured, nil
		}
		return "", fmt.Errorf("configured Docker runtime is incomplete: %s", configured)
	}
	working, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate Docker runtime: %w", err)
	}
	for candidate := working; ; candidate = filepath.Dir(candidate) {
		if isFile(filepath.Join(candidate, "compose.yaml")) && isFile(filepath.Join(candidate, "Dockerfile")) {
			return candidate, nil
		}
		if candidate == filepath.Dir(candidate) {
			break
		}
	}
	return "", errors.New("docker runtime not found; run 'lisan install' from a release binary or source checkout")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
