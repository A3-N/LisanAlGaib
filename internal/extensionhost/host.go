// Package extensionhost serves the manifest-driven extension protocol either
// as a standalone sidecar binary or as a native, lifecycle-bound host process.
package extensionhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"lisanalgaib/internal/childproc"
	"lisanalgaib/internal/connectors"
	"lisanalgaib/internal/safefile"
)

type Configuration struct {
	Manifest connectors.Manifest `json:"manifest"`
	Tools    []ToolSpec          `json:"tools,omitempty"`
	Actions  []ActionSpec        `json:"actions,omitempty"`
}

type ToolSpec struct {
	connectors.Tool
	Command     string   `json:"command"`
	VersionArgs []string `json:"version_args,omitempty"`
}

type ActionSpec struct {
	connectors.Action
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Output  string   `json:"output,omitempty"`
}

type Host struct {
	endpoint  string
	server    *http.Server
	cancel    context.CancelFunc
	done      chan struct{}
	errMu     sync.RWMutex
	err       error
	closeOnce sync.Once
}

func LoadConfig(path string) (Configuration, error) {
	data, err := safefile.Read(path, 1<<20)
	if err != nil {
		return Configuration{}, fmt.Errorf("read mandatory extension config: %w", err)
	}
	var config Configuration
	if err := decodeStrictJSON(strings.NewReader(string(data)), &config); err != nil {
		return Configuration{}, fmt.Errorf("decode extension config: %w", err)
	}
	config.Manifest.Tools = nil
	config.Manifest.Actions = nil
	if len(config.Tools) > connectors.MaxTools || len(config.Actions) > connectors.MaxActions {
		return Configuration{}, fmt.Errorf("extension config exceeds the %d tool/action limit", connectors.MaxTools)
	}
	seenTools := map[string]bool{}
	for _, tool := range config.Tools {
		if tool.ID == "" || tool.Name == "" || tool.Command == "" || seenTools[tool.ID] {
			return Configuration{}, errors.New("each extension tool requires a unique id, name, and command")
		}
		seenTools[tool.ID] = true
		config.Manifest.Tools = append(config.Manifest.Tools, tool.Tool)
	}
	seenActions := map[string]bool{}
	for _, action := range config.Actions {
		implementationCount := 0
		if action.Command != "" {
			implementationCount++
		}
		if action.Output != "" {
			implementationCount++
		}
		if action.ID == "" || action.Name == "" || implementationCount != 1 || seenActions[action.ID] {
			return Configuration{}, errors.New("each extension action requires a unique id and name, plus exactly one command or static output")
		}
		seenActions[action.ID] = true
		config.Manifest.Actions = append(config.Manifest.Actions, action.Action)
	}
	if err := connectors.ValidateManifest(config.Manifest); err != nil {
		return Configuration{}, fmt.Errorf("validate extension config: %w", err)
	}
	return config, nil
}

// StartNative binds an extension to an ephemeral loopback port. The returned
// endpoint is written into the in-memory Wormsign profile and is never
// persisted to the user's Docker configuration.
func StartNative(parent context.Context, configPath string) (*Host, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for native extension %s: %w", config.Manifest.ID, err)
	}
	return start(parent, listener, config), nil
}

func (h *Host) Endpoint() string { return h.endpoint }

func (h *Host) Close(ctx context.Context) error {
	h.closeOnce.Do(func() {
		h.cancel()
		_ = h.server.Shutdown(ctx)
	})
	select {
	case <-h.done:
		h.errMu.RLock()
		err := h.err
		h.errMu.RUnlock()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		_ = h.server.Close()
		return ctx.Err()
	}
}

// Serve runs the standalone extension-host used by the Docker images.
func Serve(ctx context.Context, listen, configPath string, logger *log.Logger) error {
	config, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	host := start(ctx, listener, config)
	if logger != nil {
		logger.Printf("extension %s listening on %s", config.Manifest.ID, listener.Addr())
	}
	<-host.done
	host.errMu.RLock()
	defer host.errMu.RUnlock()
	if errors.Is(host.err, http.ErrServerClosed) {
		return nil
	}
	return host.err
}

func start(parent context.Context, listener net.Listener, config Configuration) *Host {
	base, cancel := context.WithCancel(parent)
	server := &http.Server{
		Handler:           handler(config),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       30 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return base
		},
	}
	host := &Host{
		endpoint: "http://" + listener.Addr().String(),
		server:   server,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go func() {
		err := server.Serve(listener)
		host.errMu.Lock()
		host.err = err
		host.errMu.Unlock()
		close(host.done)
		cancel()
	}()
	go func() {
		<-base.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = host.Close(shutdown)
	}()
	return host
}

func handler(config Configuration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "extension": config.Manifest.ID})
	})
	mux.HandleFunc("GET /v1/manifest", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, buildManifest(config))
	})
	mux.HandleFunc("POST /v1/run", func(writer http.ResponseWriter, request *http.Request) {
		runHandler(config, writer, request)
	})
	return mux
}

func buildManifest(config Configuration) connectors.Manifest {
	manifest := config.Manifest
	manifest.Tools = make([]connectors.Tool, 0, len(config.Tools))
	for _, candidate := range config.Tools {
		path, err := exec.LookPath(candidate.Command)
		tool := candidate.Tool
		tool.Ready = err == nil
		if err == nil {
			arguments := candidate.VersionArgs
			if len(arguments) == 0 {
				arguments = []string{"--version"}
			}
			tool.Version = firstLine(runVersion(path, arguments))
		}
		manifest.Tools = append(manifest.Tools, tool)
	}
	manifest.Actions = make([]connectors.Action, 0, len(config.Actions))
	for _, action := range config.Actions {
		manifest.Actions = append(manifest.Actions, action.Action)
	}
	return manifest
}

func runHandler(config Configuration, writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, 8<<10)
	var input connectors.RunRequest
	if err := decodeStrictJSON(request.Body, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, connectors.RunResponse{Error: "invalid request"})
		return
	}
	for _, action := range config.Actions {
		if action.ID == input.ActionID {
			writeJSON(writer, http.StatusOK, runAction(request.Context(), action))
			return
		}
	}
	writeJSON(writer, http.StatusNotFound, connectors.RunResponse{ActionID: input.ActionID, Error: "unknown action"})
}

func runAction(parent context.Context, action ActionSpec) connectors.RunResponse {
	started := time.Now()
	if action.Output != "" {
		return connectors.RunResponse{
			ActionID:   action.ID,
			Output:     action.Output,
			DurationMS: time.Since(started).Milliseconds(),
		}
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, action.Command, action.Args...)
	childproc.Configure(command)
	output, err := command.CombinedOutput()
	if len(output) > 256<<10 {
		output = append(output[:256<<10], []byte("\n[output truncated]\n")...)
	}
	result := connectors.RunResponse{ActionID: action.ID, Output: string(output), DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.Error = "action timed out"
	}
	return result
}

func runVersion(path string, arguments []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, arguments...)
	childproc.Configure(command)
	output, _ := command.CombinedOutput()
	return string(output)
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	if len([]rune(line)) > 120 {
		line = string([]rune(line)[:120])
	}
	return line
}

func decodeStrictJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
