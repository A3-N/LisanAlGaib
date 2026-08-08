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
	shared, err := prepareSharedDirectory(root, false)
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
	shared, err := prepareSharedDirectory(runtimeRoot, true)
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
