package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"lisanalgaib/internal/appconfig"
)

var dockerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type connectorSyncResult struct {
	started []appconfig.ConnectorConfig
}

func syncConnectors(ctx context.Context, runtimeRoot string, configured []appconfig.ConnectorConfig, stdout, stderr io.Writer) (connectorSyncResult, error) {
	var result connectorSyncResult
	for _, connector := range configured {
		if err := validateConnector(connector); err != nil {
			return result, err
		}
		if !connector.Enabled {
			if connector.Managed {
				if err := stopManagedConnector(ctx, connector, stdout, stderr); err != nil {
					return result, err
				}
			}
			continue
		}
		if err := ensureNetwork(ctx, connector.Network, stdout, stderr); err != nil {
			return result, err
		}
		if err := connectContainer(ctx, workspaceContainer, connector.Network, stdout, stderr); err != nil {
			return result, err
		}
		if connector.Managed {
			started, err := ensureManagedConnector(ctx, runtimeRoot, connector, stdout, stderr)
			if err != nil {
				return result, err
			}
			if started {
				result.started = append(result.started, connector)
			}
		} else if !dockerObjectExists(ctx, "container", connector.Container) {
			return result, fmt.Errorf("external connector %s container is not present: %s", connector.ID, connector.Container)
		}
		if err := connectContainer(ctx, connector.Container, connector.Network, stdout, stderr); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validateConnector(connector appconfig.ConnectorConfig) error {
	if !dockerName.MatchString(connector.ID) || !dockerName.MatchString(connector.Container) || !dockerName.MatchString(connector.Network) {
		return fmt.Errorf("connector %q has an invalid id, container, or network name", connector.ID)
	}
	if connector.Managed && strings.TrimSpace(connector.Image) == "" {
		return fmt.Errorf("managed connector %q requires an image", connector.ID)
	}
	if connector.Managed && !validDockerUser(connector.User) {
		return fmt.Errorf("managed connector %q requires a valid container user", connector.ID)
	}
	return nil
}

func validDockerUser(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !dockerName.MatchString(part) {
			return false
		}
	}
	return true
}

func ensureManagedConnector(ctx context.Context, runtimeRoot string, connector appconfig.ConnectorConfig, stdout, stderr io.Writer) (bool, error) {
	if !dockerObjectExists(ctx, "image", connector.Image) {
		if connector.BuildContext == "" {
			return false, fmt.Errorf("connector %s image is missing and has no build context: %s", connector.ID, connector.Image)
		}
		contextPath, err := resolveRuntimePath(runtimeRoot, connector.BuildContext)
		if err != nil {
			return false, fmt.Errorf("connector %s build context: %w", connector.ID, err)
		}
		arguments := []string{"build", "--tag", connector.Image}
		if connector.Dockerfile != "" {
			dockerfile, err := resolveRuntimePath(runtimeRoot, connector.Dockerfile)
			if err != nil {
				return false, fmt.Errorf("connector %s Dockerfile: %w", connector.ID, err)
			}
			arguments = append(arguments, "--file", dockerfile)
		}
		arguments = append(arguments, contextPath)
		if err := runDocker(ctx, stdout, stderr, arguments...); err != nil {
			return false, fmt.Errorf("build connector %s: %w", connector.ID, err)
		}
	}
	if dockerObjectExists(ctx, "container", connector.Container) {
		label, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector"}}`, connector.Container)
		if strings.TrimSpace(label) != connector.ID {
			return false, fmt.Errorf("refusing to manage existing unowned container %s", connector.Container)
		}
		running, _ := dockerOutput(ctx, "inspect", "--format", `{{.State.Running}}`, connector.Container)
		containerImage, _ := dockerOutput(ctx, "inspect", "--format", `{{.Config.Image}}`, connector.Container)
		containerConfig, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector-config"}}`, connector.Container)
		if strings.TrimSpace(containerImage) != connector.Image || strings.TrimSpace(containerConfig) != connectorRuntimeSignature(connector) {
			if strings.TrimSpace(running) == "true" {
				if err := runDocker(ctx, stdout, stderr, "stop", connector.Container); err != nil {
					return false, fmt.Errorf("stop outdated connector %s: %w", connector.ID, err)
				}
			}
			if err := runDocker(ctx, stdout, stderr, "rm", connector.Container); err != nil {
				return false, fmt.Errorf("replace outdated connector %s: %w", connector.ID, err)
			}
		} else if strings.TrimSpace(running) != "true" {
			if err := runDocker(ctx, stdout, stderr, "start", connector.Container); err != nil {
				return false, fmt.Errorf("start connector %s: %w", connector.ID, err)
			}
			return true, nil
		} else {
			return false, nil
		}
	}
	arguments := connectorRunArguments(connector)
	if err := runDocker(ctx, stdout, stderr, arguments...); err != nil {
		return false, fmt.Errorf("run connector %s: %w", connector.ID, err)
	}
	return true, nil
}

func connectorRunArguments(connector appconfig.ConnectorConfig) []string {
	arguments := []string{
		"run", "--detach", "--name", connector.Container,
		"--hostname", connector.ID, "--network", connector.Network,
		"--user", connector.User,
		"--restart", "unless-stopped", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
	}
	for _, tmpfs := range connector.Tmpfs {
		arguments = append(arguments, "--tmpfs", tmpfs)
	}
	return append(arguments,
		"--label", "io.lisanalgaib.connector="+connector.ID,
		"--label", "io.lisanalgaib.connector-config="+connectorRuntimeSignature(connector),
		connector.Image,
	)
}

func connectorRuntimeSignature(connector appconfig.ConnectorConfig) string {
	payload := struct {
		ID      string   `json:"id"`
		Image   string   `json:"image"`
		Network string   `json:"network"`
		User    string   `json:"user"`
		Tmpfs   []string `json:"tmpfs,omitempty"`
	}{
		ID: connector.ID, Image: connector.Image,
		Network: connector.Network, User: connector.User, Tmpfs: connector.Tmpfs,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

func stopManagedConnector(ctx context.Context, connector appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	if !dockerObjectExists(ctx, "container", connector.Container) {
		return nil
	}
	label, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector"}}`, connector.Container)
	if strings.TrimSpace(label) != connector.ID {
		return nil
	}
	running, _ := dockerOutput(ctx, "inspect", "--format", `{{.State.Running}}`, connector.Container)
	if strings.TrimSpace(running) == "true" {
		if err := runDocker(ctx, stdout, stderr, "stop", connector.Container); err != nil {
			return fmt.Errorf("stop disabled connector %s: %w", connector.ID, err)
		}
	}
	return nil
}

func ensureNetwork(ctx context.Context, network string, stdout, stderr io.Writer) error {
	if dockerObjectExists(ctx, "network", network) {
		return nil
	}
	if err := runDocker(ctx, stdout, stderr, "network", "create", "--label", "io.lisanalgaib.network=1", network); err != nil {
		return fmt.Errorf("create connector network %s: %w", network, err)
	}
	return nil
}

func connectContainer(ctx context.Context, container, network string, stdout, stderr io.Writer) error {
	connected, _ := dockerOutput(ctx, "inspect", "--format", `{{if index .NetworkSettings.Networks "`+network+`"}}true{{end}}`, container)
	if strings.TrimSpace(connected) == "true" {
		return nil
	}
	if err := runDocker(ctx, stdout, stderr, "network", "connect", network, container); err != nil {
		return fmt.Errorf("connect %s to Docker network %s: %w", container, network, err)
	}
	return nil
}

func resolveRuntimePath(root, relative string) (string, error) {
	if root == "" {
		return "", errors.New("runtime root is empty")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve runtime root: %w", err)
	}
	candidate := relative
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve runtime path: %w", err)
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the installed runtime")
	}
	return candidate, nil
}

func dockerObjectExists(ctx context.Context, kind, name string) bool {
	return exec.CommandContext(ctx, "docker", kind, "inspect", name).Run() == nil
}

func dockerOutput(ctx context.Context, arguments ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "docker", arguments...).CombinedOutput()
	return string(output), err
}

func runDocker(ctx context.Context, stdout, stderr io.Writer, arguments ...string) error {
	command := exec.CommandContext(ctx, "docker", arguments...)
	command.Stdout, command.Stderr = stdout, stderr
	return command.Run()
}
