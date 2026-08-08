// Package extensionbundle discovers extension lifecycle metadata without
// loading extension code into the Lisan process.
package extensionbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/safefile"
)

const (
	SchemaVersion      = 1
	maxBundleFileBytes = 256 << 10
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type Bundle struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Version       string           `json:"version"`
	Icon          string           `json:"icon,omitempty"`
	Description   string           `json:"description,omitempty"`
	Docker        DockerRuntime    `json:"docker"`
	Native        NativeRuntime    `json:"native"`
	External      *ExternalRuntime `json:"external,omitempty"`
	Requests      Grants           `json:"requests,omitempty"`
	Directory     string           `json:"-"`
}

type DockerRuntime struct {
	Image       string   `json:"image"`
	Context     string   `json:"context"`
	Dockerfile  string   `json:"dockerfile"`
	Container   string   `json:"container,omitempty"`
	User        string   `json:"user"`
	Port        int      `json:"port"`
	Tmpfs       []string `json:"tmpfs,omitempty"`
	Environment []string `json:"environment,omitempty"`
}

type NativeRuntime struct {
	Executable    string   `json:"executable"`
	SourcePackage string   `json:"source_package,omitempty"`
	Arguments     []string `json:"arguments,omitempty"`
}

type ExternalRuntime struct {
	Endpoint string `json:"endpoint"`
}

type Grants struct {
	Internet        bool `json:"internet,omitempty"`
	PersistentState bool `json:"persistent_state,omitempty"`
	SharedRead      bool `json:"shared_read,omitempty"`
	SharedWrite     bool `json:"shared_write,omitempty"`
}

// FindRoot locates an installed runtime or source checkout containing the
// extension bundle directory.
func FindRoot(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		root, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(filepath.Join(root, "extensions")); err == nil && info.IsDir() {
			return root, nil
		}
		return "", fmt.Errorf("configured runtime has no extensions directory: %s", root)
	}
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for candidate := working; ; candidate = filepath.Dir(candidate) {
		if info, statErr := os.Stat(filepath.Join(candidate, "extensions")); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		if candidate == filepath.Dir(candidate) {
			break
		}
	}
	return "", nil
}

// Discover loads each immediate extensions/*/bundle.json file. One malformed
// bundle fails discovery so an extension cannot be silently half-installed.
func Discover(root string) ([]Bundle, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	extensionsRoot := filepath.Join(root, "extensions")
	entries, err := os.ReadDir(extensionsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read extension directory: %w", err)
	}
	var bundles []Bundle
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundlePath := filepath.Join(extensionsRoot, entry.Name(), "bundle.json")
		data, readErr := safefile.Read(bundlePath, maxBundleFileBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("read extension bundle %s: %w", entry.Name(), readErr)
		}
		var bundle Bundle
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&bundle); err != nil {
			return nil, fmt.Errorf("decode extension bundle %s: %w", entry.Name(), err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("extension bundle %s must contain one JSON value", entry.Name())
		}
		bundle.Directory = filepath.ToSlash(filepath.Join("extensions", entry.Name()))
		if err := Validate(root, bundle); err != nil {
			return nil, fmt.Errorf("extension bundle %s: %w", entry.Name(), err)
		}
		if seen[bundle.ID] {
			return nil, fmt.Errorf("extension id %q is duplicated", bundle.ID)
		}
		seen[bundle.ID] = true
		bundles = append(bundles, bundle)
		if len(bundles) > appconfig.MaxConnectors {
			return nil, fmt.Errorf("extension runtime exceeds the %d bundle limit", appconfig.MaxConnectors)
		}
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].Name < bundles[j].Name })
	return bundles, nil
}

func Validate(root string, bundle Bundle) error {
	if bundle.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported bundle schema %d", bundle.SchemaVersion)
	}
	if !identifier.MatchString(bundle.ID) || strings.TrimSpace(bundle.Name) == "" || strings.TrimSpace(bundle.Version) == "" {
		return errors.New("id, name, and version are required")
	}
	if bundle.External != nil {
		parsed, err := url.Parse(strings.TrimSpace(bundle.External.Endpoint))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return errors.New("external endpoint must be an http(s) URL without credentials")
		}
		return nil
	}
	if bundle.Docker.Image == "" || bundle.Docker.Context == "" || bundle.Docker.Dockerfile == "" || bundle.Docker.User == "" || bundle.Docker.Port < 1 || bundle.Docker.Port > 65535 {
		return errors.New("docker image, context, Dockerfile, user, and port are required")
	}
	if !appconfig.ValidExtensionContainerUser(bundle.Docker.User) {
		return errors.New("docker user must be an explicit non-root numeric uid or uid:gid")
	}
	if err := appconfig.ValidateExtensionImageArgument(bundle.Docker.Image); err != nil {
		return err
	}
	if bundle.Native.Executable == "" {
		return errors.New("native executable is required")
	}
	for _, candidate := range []string{bundle.Directory, bundle.Docker.Context, bundle.Docker.Dockerfile, bundle.Native.Executable} {
		if err := safeRelative(root, candidate); err != nil {
			return err
		}
	}
	if bundle.Native.SourcePackage != "" {
		if err := safeRelative(root, strings.TrimPrefix(bundle.Native.SourcePackage, "./")); err != nil {
			return err
		}
	}
	for _, value := range bundle.Native.Arguments {
		if strings.ContainsRune(value, '\x00') {
			return errors.New("runtime arguments cannot contain NUL")
		}
	}
	for _, value := range bundle.Docker.Environment {
		if err := appconfig.ValidateExtensionEnvironment(value); err != nil {
			return err
		}
	}
	for _, value := range bundle.Docker.Tmpfs {
		if err := appconfig.ValidateExtensionTmpfs(value); err != nil {
			return err
		}
	}
	return nil
}

func safeRelative(root, value string) error {
	if value == "" || filepath.IsAbs(value) {
		return fmt.Errorf("bundle path %q must be relative", value)
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("bundle path %q escapes the runtime", value)
	}
	return nil
}

// Reconcile updates profiles from discovered bundle metadata while preserving
// enabled state and grants. Removed bundles disappear; hand-authored external
// endpoint entries (Bundle == "") remain untouched.
func Reconcile(document *appconfig.Document, bundles []Bundle) {
	for profileIndex := range document.Profiles {
		profile := &document.Profiles[profileIndex]
		configured := make(map[string]appconfig.ConnectorConfig, len(profile.Connectors))
		var external []appconfig.ConnectorConfig
		for _, connector := range profile.Connectors {
			configured[connector.ID] = connector
			if connector.Bundle == "" {
				external = append(external, connector)
			}
		}
		profile.Connectors = external
		for _, bundle := range bundles {
			previous := configured[bundle.ID]
			connector := bundle.ConnectorConfig()
			connector.Enabled = previous.Enabled
			if previous.Bundle != "" {
				connector.Grants = previous.Grants
			}
			profile.Connectors = append(profile.Connectors, connector)
		}
	}
}

func (bundle Bundle) ConnectorConfig() appconfig.ConnectorConfig {
	if bundle.External != nil {
		return appconfig.ConnectorConfig{
			ID: bundle.ID, Name: bundle.Name, Icon: bundle.Icon, Description: bundle.Description,
			Bundle: bundle.Directory, Version: bundle.Version, External: true,
			Container: bundle.ID, Network: "external", Endpoint: bundle.External.Endpoint,
		}
	}
	container := bundle.Docker.Container
	if container == "" {
		container = "lisan-" + bundle.ID
	}
	executable := strings.ReplaceAll(bundle.Native.Executable, "${os}", runtime.GOOS)
	executable = strings.ReplaceAll(executable, "${arch}", runtime.GOARCH)
	return appconfig.ConnectorConfig{
		ID: bundle.ID, Name: bundle.Name, Icon: bundle.Icon, Description: bundle.Description,
		Managed: true, Bundle: bundle.Directory, Version: bundle.Version,
		Image: bundle.Docker.Image, BuildContext: bundle.Docker.Context,
		Dockerfile: bundle.Docker.Dockerfile, Container: container,
		User: bundle.Docker.User, Network: appconfig.ExtensionControlNetworkName(bundle.ID),
		Endpoint:         fmt.Sprintf("http://%s:%d", container, bundle.Docker.Port),
		Tmpfs:            append([]string(nil), bundle.Docker.Tmpfs...),
		Environment:      append([]string(nil), bundle.Docker.Environment...),
		NativeExecutable: executable, NativePackage: bundle.Native.SourcePackage,
		NativeArguments: append([]string(nil), bundle.Native.Arguments...),
		Requests:        appconfig.ExtensionGrants(bundle.Requests),
	}
}
