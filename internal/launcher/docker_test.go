package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestDockerExecForcesConfiguredSietchIdentity(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	arguments := dockerExecArguments(DockerOptions{Profile: profile, Workspace: "/home/fremen/projects/test"}, "encoded-profile")
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{
		"--user fremen", "--workdir /home/fremen", "HOME=/home/fremen",
		appconfig.EnvironmentProfile + "=encoded-profile", "run /home/fremen/projects/test",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("Docker launch is missing %q: %s", expected, joined)
		}
	}
}

func TestDockerWorkdirDoesNotReplaceUserHome(t *testing.T) {
	profile := appconfig.ProfileFromPreset(appconfig.Presets[0], time.Now())
	profile.Terminal.DockerWorkdir = "/home/fremen/projects/example"
	joined := strings.Join(dockerExecArguments(DockerOptions{Profile: profile}, "profile"), " ")
	if !strings.Contains(joined, "--workdir /home/fremen/projects/example") || !strings.Contains(joined, "HOME=/home/fremen") {
		t.Fatalf("Docker identity mixed workdir and home: %s", joined)
	}
}

func TestConfiguredRuntimeRequiresComposeAndDockerfile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntimeRoot(root); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete configured runtime was accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveRuntimeRoot(root)
	if err != nil || resolved != root {
		t.Fatalf("complete configured runtime was rejected: %q, %v", resolved, err)
	}
}

func TestSharedDirectoryLivesAtSourceRoot(t *testing.T) {
	root := t.TempDir()
	shared, err := PrepareSharedDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if shared != filepath.Join(root, "shared") {
		t.Fatalf("source shared directory = %q", shared)
	}
	if info, err := os.Stat(shared); err != nil || !info.IsDir() {
		t.Fatalf("source shared directory was not created: %v", err)
	}
}

func TestInstalledSharedDirectorySurvivesBesideRuntime(t *testing.T) {
	configRoot := t.TempDir()
	runtimeRoot := filepath.Join(configRoot, "runtime")
	shared, err := PrepareSharedDirectory(runtimeRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	if shared != filepath.Join(configRoot, "shared") {
		t.Fatalf("installed shared directory = %q", shared)
	}
	if strings.HasPrefix(shared, runtimeRoot+string(filepath.Separator)) {
		t.Fatalf("installed shared directory would be replaced with runtime: %q", shared)
	}
}

func TestSharedDirectoryRejectsSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "shared")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := PrepareSharedDirectory(root, false); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("shared symlink target was accepted: %v", err)
	}
}

func TestSharedDirectoryRejectsDockerMountDelimiter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime,unsafe")
	if _, err := PrepareSharedDirectory(root, false); err == nil || !strings.Contains(err.Error(), "unsupported by Docker mounts") {
		t.Fatalf("expected Docker mount delimiter error, got %v", err)
	}
}

func TestWorkspaceComposeExposesOnlyTheSharedHostDirectory(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	compose := string(data)
	for _, required := range []string{
		"privileged: false",
		"source: ${LISAN_SHARED_DIR:-./shared}",
		"target: /home/fremen/shared",
		"create_host_path: false",
		"- usul:/home/fremen",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("workspace boundary omits %q", required)
		}
	}
	if count := strings.Count(compose, "type: bind"); count != 1 {
		t.Fatalf("workspace has %d host bind mounts, want exactly one", count)
	}
	for _, forbidden := range []string{"/var/run/docker.sock", "docker.sock", "privileged: true", "network_mode: host", "pid: host", "ipc: host", "/dev/"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("workspace compose exposes forbidden capability %q", forbidden)
		}
	}
}
