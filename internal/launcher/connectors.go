package launcher

import (
	"bytes"
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
	"lisanalgaib/internal/cliout"
)

var dockerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func syncConnectors(ctx context.Context, runtimeRoot, sharedRoot string, configured []appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	if err := stopUnconfiguredManagedConnectors(ctx, configured, stdout, stderr); err != nil {
		return err
	}
	for _, connector := range configured {
		if err := validateConnector(connector); err != nil {
			return err
		}
		if !connector.Enabled {
			if connector.Managed {
				if err := stopManagedConnector(ctx, connector, stdout, stderr); err != nil {
					return err
				}
			}
			continue
		}
		if connector.External {
			continue
		}
		if err := ensureExtensionControlNetwork(ctx, connector, stdout, stderr); err != nil {
			return err
		}
		if err := connectContainer(ctx, workspaceContainer, connector.Network, stdout, stderr); err != nil {
			return err
		}
		if err := ensureManagedConnector(ctx, runtimeRoot, sharedRoot, connector, stdout, stderr); err != nil {
			return err
		}
		if err := connectContainer(ctx, connector.Container, connector.Network, stdout, stderr); err != nil {
			return err
		}
		if connector.Managed && connector.Grants.Internet {
			internetNetwork := extensionEgressNetwork(connector.ID)
			if err := ensureExtensionEgressNetwork(ctx, connector.ID, stdout, stderr); err != nil {
				return err
			}
			if err := connectContainer(ctx, connector.Container, internetNetwork, stdout, stderr); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopUnconfiguredManagedConnectors prevents a removed bundle or profile from
// leaving an old sidecar consuming CPU after the next launch. It stops only
// containers carrying Lisan's ownership label; cleanup remains responsible for
// deleting their state and images.
func stopUnconfiguredManagedConnectors(ctx context.Context, configured []appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	known := make(map[string]bool, len(configured))
	for _, connector := range configured {
		known[connector.ID] = true
	}
	output, err := dockerOutput(ctx, "ps", "--filter", "label=io.lisanalgaib.connector", "--format", `{{.Names}}|{{.Label "io.lisanalgaib.connector"}}`)
	if err != nil {
		return fmt.Errorf("list running managed extensions: %w", err)
	}
	for _, name := range unconfiguredManagedConnectorNames(output, known) {
		if err := runDocker(ctx, stdout, stderr, "stop", name); err != nil {
			return fmt.Errorf("stop unconfigured managed extension %s: %w", name, err)
		}
		cliout.Detail(stdout, "stopped", "unconfigured extension "+name)
	}
	return nil
}

func unconfiguredManagedConnectorNames(output string, known map[string]bool) []string {
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		name, id, ok := strings.Cut(line, "|")
		name, id = strings.TrimSpace(name), strings.TrimSpace(id)
		if !ok || !dockerName.MatchString(name) || !dockerName.MatchString(id) || known[id] {
			continue
		}
		names = append(names, name)
	}
	return names
}

func validateConnector(connector appconfig.ConnectorConfig) error {
	if connector.External {
		if !dockerName.MatchString(connector.ID) || connector.Managed {
			return fmt.Errorf("external extension %q has invalid lifecycle metadata", connector.ID)
		}
		return nil
	}
	if !connector.Managed {
		return fmt.Errorf("extension %q must use a managed bundle or external HTTP endpoint", connector.ID)
	}
	if !dockerName.MatchString(connector.ID) || !dockerName.MatchString(connector.Container) || !dockerName.MatchString(connector.Network) {
		return fmt.Errorf("connector %q has an invalid id, container, or network name", connector.ID)
	}
	if err := appconfig.ValidateExtensionImageArgument(connector.Image); err != nil {
		return fmt.Errorf("managed connector %q has an invalid image reference", connector.ID)
	}
	if !validDockerUser(connector.User) {
		return fmt.Errorf("managed connector %q requires a valid container user", connector.ID)
	}
	if connector.Grants.SharedWrite && !connector.Grants.SharedRead {
		return fmt.Errorf("managed connector %q cannot grant shared_write without shared_read", connector.ID)
	}
	if !grantsWithinRequests(connector.Grants, connector.Requests) {
		return fmt.Errorf("managed connector %q grants a capability the bundle did not request", connector.ID)
	}
	for _, value := range connector.Environment {
		if err := appconfig.ValidateExtensionEnvironment(value); err != nil {
			return fmt.Errorf("managed connector %q has invalid environment entry", connector.ID)
		}
	}
	if connector.Network != appconfig.ExtensionControlNetworkName(connector.ID) {
		return fmt.Errorf("managed connector %q must use its dedicated control network", connector.ID)
	}
	for _, tmpfs := range connector.Tmpfs {
		if err := appconfig.ValidateExtensionTmpfs(tmpfs); err != nil {
			return fmt.Errorf("managed connector %q: %w", connector.ID, err)
		}
	}
	return nil
}

func grantsWithinRequests(granted, requested appconfig.ExtensionGrants) bool {
	return (!granted.Internet || requested.Internet) &&
		(!granted.PersistentState || requested.PersistentState) &&
		(!granted.SharedRead || requested.SharedRead) &&
		(!granted.SharedWrite || requested.SharedWrite)
}

func validDockerUser(value string) bool {
	return appconfig.ValidExtensionContainerUser(value)
}

func ensureManagedConnector(ctx context.Context, runtimeRoot, sharedRoot string, connector appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	imageExists := dockerObjectExists(ctx, "image", connector.Image)
	buildSignature := ""
	buildRequired := !imageExists
	if connector.BuildContext != "" {
		var err error
		buildSignature, err = connectorBuildSignature(runtimeRoot, connector)
		if err != nil {
			return fmt.Errorf("connector %s build fingerprint: %w", connector.ID, err)
		}
		if imageExists {
			current, _ := dockerOutput(ctx, "image", "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector-build-signature"}}`, connector.Image)
			buildRequired = strings.TrimSpace(current) != buildSignature
		}
	}
	if buildRequired {
		if connector.BuildContext == "" {
			return fmt.Errorf("connector %s image is missing and has no build context: %s", connector.ID, connector.Image)
		}
		contextPath, err := resolveRuntimePath(runtimeRoot, connector.BuildContext)
		if err != nil {
			return fmt.Errorf("connector %s build context: %w", connector.ID, err)
		}
		arguments := []string{
			"build",
			"--label", "io.lisanalgaib.connector-image=" + connector.ID,
			"--label", "io.lisanalgaib.connector-build-signature=" + buildSignature,
			"--tag", connector.Image,
		}
		if connector.Dockerfile != "" {
			dockerfile, err := resolveRuntimePath(runtimeRoot, connector.Dockerfile)
			if err != nil {
				return fmt.Errorf("connector %s Dockerfile: %w", connector.ID, err)
			}
			arguments = append(arguments, "--file", dockerfile)
		}
		arguments = append(arguments, contextPath)
		structured := append([]string{"build", "--progress=rawjson"}, arguments[1:]...)
		if err := runDockerProgress(stderr, "Building extension "+connector.Name,
			exec.CommandContext(ctx, "docker", structured...), exec.CommandContext(ctx, "docker", arguments...)); err != nil {
			return fmt.Errorf("build connector %s: %w", connector.ID, err)
		}
	}
	desiredImageID, err := dockerOutput(ctx, "image", "inspect", "--format", `{{.Id}}`, connector.Image)
	if err != nil {
		return fmt.Errorf("inspect connector %s image after build: %w", connector.ID, err)
	}
	if strings.TrimSpace(desiredImageID) == "" {
		return fmt.Errorf("inspect connector %s image after build: Docker returned an empty image ID", connector.ID)
	}
	if dockerObjectExists(ctx, "container", connector.Container) {
		label, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector"}}`, connector.Container)
		if strings.TrimSpace(label) != connector.ID {
			return fmt.Errorf("refusing to manage existing unowned container %s", connector.Container)
		}
		running, _ := dockerOutput(ctx, "inspect", "--format", `{{.State.Running}}`, connector.Container)
		containerImageID, _ := dockerOutput(ctx, "inspect", "--format", `{{.Image}}`, connector.Container)
		containerConfig, _ := dockerOutput(ctx, "inspect", "--format", `{{index .Config.Labels "io.lisanalgaib.connector-config"}}`, connector.Container)
		if strings.TrimSpace(containerImageID) != strings.TrimSpace(desiredImageID) || strings.TrimSpace(containerConfig) != connectorRuntimeSignature(connector) {
			if strings.TrimSpace(running) == "true" {
				if err := runDocker(ctx, stdout, stderr, "stop", connector.Container); err != nil {
					return fmt.Errorf("stop outdated connector %s: %w", connector.ID, err)
				}
			}
			if err := runDocker(ctx, stdout, stderr, "rm", connector.Container); err != nil {
				return fmt.Errorf("replace outdated connector %s: %w", connector.ID, err)
			}
		} else if strings.TrimSpace(running) != "true" {
			if err := runDocker(ctx, stdout, stderr, "start", connector.Container); err != nil {
				return fmt.Errorf("start connector %s: %w", connector.ID, err)
			}
			cliout.Success(stdout, "Starting extension "+connector.Name)
			return nil
		} else {
			return nil
		}
	}
	if connector.Grants.PersistentState {
		if err := ensureExtensionVolume(ctx, connector, stdout, stderr); err != nil {
			return err
		}
	}
	arguments := connectorRunArguments(connector, sharedRoot)
	if err := runDocker(ctx, stdout, stderr, arguments...); err != nil {
		return fmt.Errorf("run connector %s: %w", connector.ID, err)
	}
	cliout.Success(stdout, "Starting extension "+connector.Name)
	return nil
}

func connectorBuildSignature(runtimeRoot string, connector appconfig.ConnectorConfig) (string, error) {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n%s\n%s\n%s\n", connector.ID, connector.Image, connector.BuildContext, connector.Dockerfile)
	paths := []string{".dockerignore", "go.mod", "go.sum", "internal"}
	if connector.Bundle != "" {
		paths = append(paths, connector.Bundle)
	}
	if connector.Dockerfile != "" {
		paths = append(paths, connector.Dockerfile)
	}
	if err := writeRuntimeFingerprint(hash, runtimeRoot, paths); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func connectorRunArguments(connector appconfig.ConnectorConfig, sharedRoot string) []string {
	arguments := []string{
		"run", "--detach", "--name", connector.Container,
		"--network", connector.Network,
		"--user", connector.User,
		"--restart", "no", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m",
	}
	for _, tmpfs := range connector.Tmpfs {
		arguments = append(arguments, "--tmpfs", tmpfs)
	}
	for _, value := range connector.Environment {
		arguments = append(arguments, "--env", value)
	}
	statePath, sharedPath := "", ""
	if connector.Grants.PersistentState {
		statePath = "/var/lib/lisan-extension"
		arguments = append(arguments,
			"--mount", "type=volume,source="+extensionVolumeName(connector.ID)+",target=/var/lib/lisan-extension",
		)
	}
	if connector.Grants.SharedRead {
		sharedPath = "/shared"
		mount := "type=bind,source=" + sharedRoot + ",target=/shared"
		if !connector.Grants.SharedWrite {
			mount += ",readonly"
		}
		arguments = append(arguments, "--mount", mount)
	}
	arguments = append(arguments,
		"--env", "LISAN_EXTENSION_ID="+connector.ID,
		"--env", "LISAN_EXTENSION_STATE="+statePath,
		"--env", "LISAN_EXTENSION_SHARED="+sharedPath,
	)
	return append(arguments,
		"--label", "io.lisanalgaib.connector="+connector.ID,
		"--label", "io.lisanalgaib.connector-config="+connectorRuntimeSignature(connector),
		connector.Image,
	)
}

func connectorRuntimeSignature(connector appconfig.ConnectorConfig) string {
	payload := struct {
		ID          string                    `json:"id"`
		Image       string                    `json:"image"`
		Network     string                    `json:"network"`
		User        string                    `json:"user"`
		Tmpfs       []string                  `json:"tmpfs,omitempty"`
		Environment []string                  `json:"environment,omitempty"`
		Grants      appconfig.ExtensionGrants `json:"grants"`
	}{
		ID: connector.ID, Image: connector.Image,
		Network: connector.Network, User: connector.User, Tmpfs: connector.Tmpfs,
		Environment: connector.Environment, Grants: connector.Grants,
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

func ensureExtensionControlNetwork(ctx context.Context, connector appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	name := appconfig.ExtensionControlNetworkName(connector.ID)
	if connector.Network != name {
		return fmt.Errorf("extension %s control network must be %s", connector.ID, name)
	}
	if dockerObjectExists(ctx, "network", name) {
		if !dockerExtensionControlOwned(ctx, name) {
			return fmt.Errorf("refusing to use unowned extension control network %s", name)
		}
		value, err := dockerOutput(ctx, "network", "inspect", "--format", `{{.Internal}}`, name)
		if err != nil || strings.TrimSpace(value) != "true" {
			return fmt.Errorf("extension control network %s is not internal", name)
		}
		return nil
	}
	if err := runDocker(ctx, stdout, stderr, "network", "create", "--internal",
		"--label", "io.lisanalgaib.network=1",
		"--label", "io.lisanalgaib.extension-control="+connector.ID,
		name,
	); err != nil {
		return fmt.Errorf("create extension %s control network: %w", connector.ID, err)
	}
	return nil
}

func extensionVolumeName(id string) string { return "lisan-extension-" + id }

func extensionEgressNetwork(id string) string { return "lisan-extension-egress-" + id }

func ensureExtensionEgressNetwork(ctx context.Context, id string, stdout, stderr io.Writer) error {
	name := extensionEgressNetwork(id)
	if dockerObjectExists(ctx, "network", name) {
		label, _ := dockerOutput(ctx, "network", "inspect", "--format", `{{index .Labels "io.lisanalgaib.extension-egress"}}`, name)
		if strings.TrimSpace(label) != id {
			return fmt.Errorf("refusing to use unowned extension egress network %s", name)
		}
		internal, err := dockerOutput(ctx, "network", "inspect", "--format", `{{.Internal}}`, name)
		if err != nil || strings.TrimSpace(internal) != "false" {
			return fmt.Errorf("extension egress network %s cannot provide outbound access", name)
		}
		return nil
	}
	if err := runDocker(ctx, stdout, stderr, "network", "create",
		"--label", "io.lisanalgaib.network=1",
		"--label", "io.lisanalgaib.extension-egress="+id,
		name,
	); err != nil {
		return fmt.Errorf("create extension %s egress network: %w", id, err)
	}
	return nil
}

func ensureExtensionVolume(ctx context.Context, connector appconfig.ConnectorConfig, stdout, stderr io.Writer) error {
	name := extensionVolumeName(connector.ID)
	if dockerObjectExists(ctx, "volume", name) {
		label, _ := dockerOutput(ctx, "volume", "inspect", "--format", `{{index .Labels "io.lisanalgaib.extension-volume"}}`, name)
		if strings.TrimSpace(label) != connector.ID {
			return fmt.Errorf("refusing to use unowned extension volume %s", name)
		}
		return nil
	}
	if err := runDocker(ctx, stdout, stderr, "volume", "create", "--label", "io.lisanalgaib.extension-volume="+connector.ID, name); err != nil {
		return fmt.Errorf("create extension %s state volume: %w", connector.ID, err)
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
	if dockerVerbose() {
		command.Stdout, command.Stderr = stdout, stderr
		return command.Run()
	}
	var captured bytes.Buffer
	command.Stdout, command.Stderr = &captured, &captured
	if err := command.Run(); err != nil {
		return dockerCommandError(err, captured.String())
	}
	return nil
}
