package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"lisanalgaib/internal/appconfig"
)

type CleanupOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

func cleanupStartedDocker(ctx context.Context, workspaceStarted bool, connectors []appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	var result error
	for index := len(connectors) - 1; index >= 0; index-- {
		connector := connectors[index]
		if err := stopManagedConnector(ctx, connector, stdout, stderr); err != nil {
			result = errors.Join(result, err)
		}
	}
	if workspaceStarted && containerRunning(ctx, workspaceContainer) && dockerWorkspaceOwned(ctx, workspaceContainer) {
		if err := runDocker(ctx, stdout, stderr, "stop", workspaceContainer); err != nil {
			result = errors.Join(result, fmt.Errorf("stop Docker workspace: %w", err))
		}
	}
	return result
}

// Cleanup is an idempotent recovery command. It stops running Docker services
// carrying Lisan ownership labels but never removes images, containers,
// projects, or the persistent Usul volume. Native Wormsign extensions
// are in-process and cannot survive their owning Lisan process.
func Cleanup(ctx context.Context, options CleanupOptions) error {
	stdout, stderr := options.Stdout, options.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(stdout, "No Docker executable found; native extensions already stop with Lisan.")
		return nil
	}
	info := exec.CommandContext(ctx, "docker", "info")
	info.Stdout, info.Stderr = io.Discard, io.Discard
	if err := info.Run(); err != nil {
		fmt.Fprintln(stdout, "Docker daemon unavailable; native extensions already stop with Lisan.")
		return nil
	}

	var result error
	output, err := dockerOutput(ctx, "ps", "--filter", "label=io.lisanalgaib.connector", "--format", `{{.Names}}`)
	if err != nil {
		result = errors.Join(result, fmt.Errorf("list managed extensions: %w", err))
	} else {
		for _, name := range strings.Fields(output) {
			if !dockerName.MatchString(name) {
				continue
			}
			label, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector"}}`, name)
			if strings.TrimSpace(label) == "" {
				continue
			}
			if err := runDocker(ctx, stdout, stderr, "stop", name); err != nil {
				result = errors.Join(result, fmt.Errorf("stop managed extension %s: %w", name, err))
			} else {
				fmt.Fprintln(stdout, "Stopped managed extension", name)
			}
		}
	}

	if containerRunning(ctx, workspaceContainer) && dockerWorkspaceOwned(ctx, workspaceContainer) {
		if err := runDocker(ctx, stdout, stderr, "stop", workspaceContainer); err != nil {
			result = errors.Join(result, fmt.Errorf("stop Docker workspace: %w", err))
		} else {
			fmt.Fprintln(stdout, "Stopped Sietch Tabr workspace "+workspaceContainer)
		}
	}
	if result == nil {
		fmt.Fprintln(stdout, "Lisan cleanup complete; persistent containers and Usul data were retained.")
	}
	return result
}

func dockerWorkspaceOwned(ctx context.Context, name string) bool {
	labels, err := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.workspace"}}|{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}`, name)
	if err != nil {
		return false
	}
	parts := strings.Split(strings.TrimSpace(labels), "|")
	if len(parts) != 3 {
		return false
	}
	return parts[0] == "1" || (parts[1] == "arrakis" && parts[2] == "workspace")
}
