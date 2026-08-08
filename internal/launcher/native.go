package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"lisanalgaib/internal/appconfig"
	"lisanalgaib/internal/childproc"
	"lisanalgaib/internal/cliout"
	"lisanalgaib/internal/connectors"
)

// NativeRuntime owns extension processes started for one vm launch. A release
// runs the platform binary packed beside each bundle; a source checkout may
// fall back to `go run` for extension development.
type NativeRuntime struct {
	processes []*nativeProcess
	temporary []string
}

type nativeProcess struct {
	command *exec.Cmd
	done    chan struct{}
	mu      sync.RWMutex
	err     error
}

func superviseNativeProcess(command *exec.Cmd) *nativeProcess {
	process := &nativeProcess{command: command, done: make(chan struct{})}
	go func() {
		err := command.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process
}

func (process *nativeProcess) waitError() error {
	process.mu.RLock()
	defer process.mu.RUnlock()
	return process.err
}

func StartNativeConnectors(ctx context.Context, runtimeRoot string, profile appconfig.Profile, output io.Writer) (appconfig.Profile, *NativeRuntime, error) {
	validated, err := appconfig.NormalizeProfile(profile)
	if err != nil {
		return profile, nil, err
	}
	active := validated.Clone()
	runtimeState := &NativeRuntime{}
	fail := func(err error) (appconfig.Profile, *NativeRuntime, error) {
		return profile, nil, errors.Join(err, runtimeState.Close())
	}
	for index := range active.Connectors {
		connector := &active.Connectors[index]
		if !connector.Enabled || !connector.Managed {
			continue
		}
		listen, err := availableLoopbackAddress()
		if err != nil {
			return fail(fmt.Errorf("allocate native extension %s endpoint: %w", connector.ID, err))
		}
		command, temporary, err := nativeExtensionCommand(ctx, runtimeRoot, *connector, listen)
		if err != nil {
			return fail(err)
		}
		if temporary != "" {
			runtimeState.temporary = append(runtimeState.temporary, temporary)
		}
		command.Dir = runtimeRoot
		command.Env, err = nativeExtensionEnvironment(*connector, listen)
		if err != nil {
			return fail(err)
		}
		command.Stdout = output
		command.Stderr = output
		childproc.Configure(command)
		if err := command.Start(); err != nil {
			return fail(fmt.Errorf("start native extension %s: %w", connector.ID, err))
		}
		process := superviseNativeProcess(command)
		runtimeState.processes = append(runtimeState.processes, process)
		connector.Endpoint = "http://" + listen
		connector.Network = "native-loopback"
		if err := waitForNativeExtension(ctx, *connector, process); err != nil {
			return fail(err)
		}
		if output != nil {
			cliout.Success(output, "Starting extension "+connector.Name)
			cliout.Detail(output, "endpoint", connector.Endpoint)
		}
	}
	return active, runtimeState, nil
}

func availableLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func nativeExtensionCommand(ctx context.Context, root string, connector appconfig.ConnectorConfig, listen string) (*exec.Cmd, string, error) {
	executable, err := resolveRuntimePath(root, connector.NativeExecutable)
	if err == nil {
		arguments := append([]string{"--listen", listen}, connector.NativeArguments...)
		return exec.CommandContext(ctx, executable, arguments...), "", nil
	}
	if strings.TrimSpace(connector.NativePackage) == "" {
		return nil, "", fmt.Errorf("native extension %s executable: %w", connector.ID, err)
	}
	if _, lookErr := exec.LookPath("go"); lookErr != nil {
		return nil, "", fmt.Errorf("native extension %s executable is missing and Go is unavailable for source fallback", connector.ID)
	}
	packagePath := strings.TrimPrefix(filepath.ToSlash(connector.NativePackage), "./")
	if _, pathErr := resolveRuntimePath(root, packagePath); pathErr != nil {
		return nil, "", fmt.Errorf("native extension %s source package: %w", connector.ID, pathErr)
	}
	temporary, err := os.MkdirTemp("", "lisan-extension-"+connector.ID+"-*")
	if err != nil {
		return nil, "", err
	}
	binaryName := connector.ID
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(temporary, binaryName)
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", binary, "./"+packagePath)
	build.Dir = root
	childproc.Configure(build)
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		_ = os.RemoveAll(temporary)
		return nil, "", fmt.Errorf("build native extension %s: %w: %s", connector.ID, buildErr, strings.TrimSpace(string(output)))
	}
	arguments := []string{"--listen", listen}
	arguments = append(arguments, connector.NativeArguments...)
	return exec.CommandContext(ctx, binary, arguments...), temporary, nil
}

func nativeExtensionEnvironment(connector appconfig.ConnectorConfig, listen string) ([]string, error) {
	environment := append([]string(nil), os.Environ()...)
	stateDirectory, sharedDirectory := "", ""
	if connector.Grants.PersistentState {
		configRoot, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locate extension state directory: %w", err)
		}
		stateDirectory = filepath.Join(configRoot, "lisanalgaib", "extensions", connector.ID)
		if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
			return nil, fmt.Errorf("create extension state directory: %w", err)
		}
	}
	if connector.Grants.SharedRead {
		sharedDirectory = strings.TrimSpace(os.Getenv("LISAN_SHARED_DIR"))
	}
	environment = append(environment, connector.Environment...)
	environment = append(environment,
		"LISAN_EXTENSION_ID="+connector.ID,
		"LISAN_EXTENSION_LISTEN="+listen,
		"LISAN_EXTENSION_STATE="+stateDirectory,
		"LISAN_EXTENSION_SHARED="+sharedDirectory,
	)
	return environment, nil
}

func waitForNativeExtension(ctx context.Context, connector appconfig.ConnectorConfig, process *nativeProcess) error {
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		manifest, err := connectors.FetchManifest(ctx, connector.Endpoint)
		if err == nil {
			if manifest.ID != connector.ID {
				return fmt.Errorf("native extension id %q does not match bundle id %q", manifest.ID, connector.ID)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-process.done:
			if err := process.waitError(); err != nil {
				return fmt.Errorf("native extension %s exited before becoming ready: %w", connector.ID, err)
			}
			return fmt.Errorf("native extension %s exited before becoming ready", connector.ID)
		case <-deadline.C:
			return fmt.Errorf("native extension %s did not become ready", connector.ID)
		case <-ticker.C:
		}
	}
}

func (runtimeState *NativeRuntime) Close() error {
	if runtimeState == nil {
		return nil
	}
	var result error
	for index := len(runtimeState.processes) - 1; index >= 0; index-- {
		process := runtimeState.processes[index]
		command := process.command
		if command.Process == nil {
			continue
		}
		select {
		case <-process.done:
			continue
		default:
		}
		if err := command.Cancel(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, err)
		}
		<-process.done
	}
	runtimeState.processes = nil
	for _, directory := range runtimeState.temporary {
		if err := os.RemoveAll(directory); err != nil {
			result = errors.Join(result, err)
		}
	}
	runtimeState.temporary = nil
	return result
}
