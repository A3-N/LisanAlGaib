package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/cliout"
)

type CleanupOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

var dockerImageID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

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

// Cleanup is an idempotent full reset of Lisan-owned Docker state. The dedicated
// host shared directory is a bind mount, not a Docker object, and is deliberately
// never touched. Native Wormsign extensions are in-process and cannot survive
// their owning Lisan process.
func Cleanup(ctx context.Context, options CleanupOptions) error {
	stdout, stderr := options.Stdout, options.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if _, err := exec.LookPath("docker"); err != nil {
		cliout.Result(stdout, "Cleaning Lisan Docker state", "skipped")
		cliout.Detail(stdout, "reason", "Docker is not installed; native extensions already stop with Lisan")
		return nil
	}
	info := exec.CommandContext(ctx, "docker", "info")
	info.Stdout, info.Stderr = io.Discard, io.Discard
	if err := info.Run(); err != nil {
		cliout.Result(stdout, "Cleaning Lisan Docker state", "skipped")
		cliout.Detail(stdout, "reason", "Docker daemon unavailable; native extensions already stop with Lisan")
		return nil
	}

	var result error
	connectorImages := map[string]bool{}
	output, err := dockerOutput(ctx, "ps", "--all", "--filter", "label=io.lisanalgaib.connector", "--format", `{{.Names}}`)
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
			imageID, _ := dockerOutput(ctx, "inspect", "--format", `{{.Image}}`, name)
			addDockerImageID(connectorImages, imageID)
			if err := runDocker(ctx, stdout, stderr, "rm", "--force", name); err != nil {
				result = errors.Join(result, fmt.Errorf("remove managed extension %s: %w", name, err))
			} else {
				cliout.Detail(stdout, "removed", "managed extension "+name)
			}
		}
	}

	if dockerObjectExists(ctx, "container", workspaceContainer) && dockerWorkspaceOwned(ctx, workspaceContainer) {
		if err := runDocker(ctx, stdout, stderr, "rm", "--force", workspaceContainer); err != nil {
			result = errors.Join(result, fmt.Errorf("remove Docker workspace: %w", err))
		} else {
			cliout.Detail(stdout, "removed", "Sietch Tabr workspace "+workspaceContainer)
		}
	}

	imageOutput, imageErr := dockerOutput(ctx, "image", "ls", "--no-trunc", "--filter", "label=io.lisanalgaib.connector-image", "--format", `{{.ID}}`)
	if imageErr != nil {
		result = errors.Join(result, fmt.Errorf("list managed extension images: %w", imageErr))
	} else {
		for _, imageID := range strings.Fields(imageOutput) {
			addDockerImageID(connectorImages, imageID)
		}
	}
	for imageID := range connectorImages {
		if err := runDocker(ctx, stdout, stderr, "image", "rm", imageID); err != nil {
			result = errors.Join(result, fmt.Errorf("remove managed extension image %s: %w", imageID, err))
		} else {
			cliout.Detail(stdout, "removed", "managed extension image "+imageID)
		}
	}

	if dockerObjectExists(ctx, "image", workspaceImage) && dockerWorkspaceImageOwned(ctx, workspaceImage) {
		if err := runDocker(ctx, stdout, stderr, "image", "rm", workspaceImage); err != nil {
			result = errors.Join(result, fmt.Errorf("remove Docker workspace image: %w", err))
		} else {
			cliout.Detail(stdout, "removed", "Docker workspace image "+workspaceImage)
		}
	}
	if dockerObjectExists(ctx, "network", workspaceNetwork) && dockerNetworkOwned(ctx, workspaceNetwork) {
		attached, attachedErr := dockerOutput(ctx, "network", "inspect", "--format", `{{range .Containers}}{{.Name}} {{end}}`, workspaceNetwork)
		if attachedErr != nil {
			result = errors.Join(result, fmt.Errorf("list Docker network attachments: %w", attachedErr))
		} else {
			for _, name := range strings.Fields(attached) {
				if !dockerName.MatchString(name) {
					continue
				}
				if err := runDocker(ctx, stdout, stderr, "network", "disconnect", "--force", workspaceNetwork, name); err != nil {
					result = errors.Join(result, fmt.Errorf("disconnect %s from Docker network: %w", name, err))
				}
			}
		}
		if err := runDocker(ctx, stdout, stderr, "network", "rm", workspaceNetwork); err != nil {
			result = errors.Join(result, fmt.Errorf("remove Docker network: %w", err))
		} else {
			cliout.Detail(stdout, "removed", "Docker network "+workspaceNetwork)
		}
	}
	if dockerObjectExists(ctx, "volume", workspaceVolume) && dockerVolumeOwned(ctx, workspaceVolume) {
		if err := runDocker(ctx, stdout, stderr, "volume", "rm", workspaceVolume); err != nil {
			result = errors.Join(result, fmt.Errorf("remove Docker home volume: %w", err))
		} else {
			cliout.Detail(stdout, "removed", "Docker home volume "+workspaceVolume)
		}
	}
	if result == nil {
		cliout.Result(stdout, "Cleaning Lisan Docker state", "done")
		cliout.Detail(stdout, "preserved", "shared files")
	}
	return result
}

// addDockerImageID canonicalizes Docker's sha256/full/short representations
// and keeps only the longest form of the same local image. Docker inspect and
// image ls otherwise make one image look like two cleanup targets.
func addDockerImageID(images map[string]bool, value string) {
	id := strings.TrimPrefix(strings.TrimSpace(value), "sha256:")
	if !dockerImageID.MatchString(id) {
		return
	}
	for existing := range images {
		switch {
		case strings.HasPrefix(existing, id):
			return
		case strings.HasPrefix(id, existing):
			delete(images, existing)
		}
	}
	images[id] = true
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

func dockerWorkspaceImageOwned(ctx context.Context, name string) bool {
	label, err := dockerOutput(ctx, "image", "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.build-signature"}}`, name)
	return err == nil && strings.TrimSpace(label) != ""
}

func dockerNetworkOwned(ctx context.Context, name string) bool {
	labels, err := dockerOutput(ctx, "network", "inspect", "--format", `{{index .Labels "io.lisanalgaib.network"}}|{{index .Labels "com.docker.compose.project"}}|{{index .Labels "com.docker.compose.network"}}`, name)
	return err == nil && composeResourceOwned(labels, "default")
}

func dockerVolumeOwned(ctx context.Context, name string) bool {
	labels, err := dockerOutput(ctx, "volume", "inspect", "--format", `{{index .Labels "io.lisanalgaib.volume"}}|{{index .Labels "com.docker.compose.project"}}|{{index .Labels "com.docker.compose.volume"}}`, name)
	return err == nil && composeResourceOwned(labels, "usul")
}

func composeResourceOwned(labels, composeResource string) bool {
	parts := strings.Split(strings.TrimSpace(labels), "|")
	if len(parts) != 3 {
		return false
	}
	return parts[0] == "1" || (parts[1] == "arrakis" && parts[2] == composeResource)
}
