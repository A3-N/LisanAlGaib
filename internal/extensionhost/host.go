// Package extensionhost provides an optional Go server adapter for protocol v3.
// Extensions may use it, or implement the documented HTTP contract in any
// language without importing Lisan code.
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
	"strings"
	"time"

	"lisanalgaib/internal/connectors"
)

var ErrNotFound = errors.New("extension resource not found")

type ArtifactPayload struct {
	Metadata connectors.Artifact
	Data     []byte
}

// Backend is the complete protocol-v3 extension surface. Every method runs in
// the extension process, never in Lisan's core process.
type Backend interface {
	Manifest(context.Context) (connectors.Manifest, error)
	View(context.Context, string) (connectors.View, error)
	StartJob(context.Context, connectors.StartJobRequest) (connectors.Job, error)
	Job(context.Context, string) (connectors.Job, error)
	CancelJob(context.Context, string) (connectors.Job, error)
	Artifact(context.Context, string, string) (ArtifactPayload, error)
	OpenSession(context.Context, connectors.OpenSessionRequest) (connectors.Session, error)
	SessionInput(context.Context, string, connectors.SessionInputRequest) (connectors.Session, error)
	ResizeSession(context.Context, string, connectors.ResizeSessionRequest) (connectors.Session, error)
	CloseSession(context.Context, string) (connectors.Session, error)
}

func Serve(ctx context.Context, listen string, backend Backend, logger *log.Logger) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           Handler(backend),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	if logger != nil {
		logger.Printf("extension protocol v3 listening on %s", listener.Addr())
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func Handler(backend Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v3/health", func(writer http.ResponseWriter, request *http.Request) {
		manifest, err := backend.Manifest(request.Context())
		if err != nil {
			writeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "extension": manifest.ID, "protocol": "3"})
	})
	mux.HandleFunc("GET /v3/manifest", func(writer http.ResponseWriter, request *http.Request) {
		manifest, err := backend.Manifest(request.Context())
		if err != nil {
			writeError(writer, err)
			return
		}
		if err := connectors.ValidateManifest(manifest); err != nil {
			writeError(writer, fmt.Errorf("invalid backend manifest: %w", err))
			return
		}
		writeJSON(writer, http.StatusOK, manifest)
	})
	mux.HandleFunc("GET /v3/views/{view}", func(writer http.ResponseWriter, request *http.Request) {
		view, err := backend.View(request.Context(), request.PathValue("view"))
		if err != nil {
			writeError(writer, err)
			return
		}
		if err := connectors.ValidateView(view); err != nil {
			writeError(writer, fmt.Errorf("invalid backend view: %w", err))
			return
		}
		writeJSON(writer, http.StatusOK, view)
	})
	mux.HandleFunc("POST /v3/jobs", func(writer http.ResponseWriter, request *http.Request) {
		var input connectors.StartJobRequest
		if err := decodeRequest(writer, request, &input); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		job, err := backend.StartJob(request.Context(), input)
		if err != nil {
			writeError(writer, err)
			return
		}
		if err := connectors.ValidateJob(job); err != nil {
			writeError(writer, fmt.Errorf("invalid backend job: %w", err))
			return
		}
		writeJSON(writer, http.StatusAccepted, job)
	})
	mux.HandleFunc("GET /v3/jobs/{job}", func(writer http.ResponseWriter, request *http.Request) {
		job, err := backend.Job(request.Context(), request.PathValue("job"))
		writeJob(writer, job, err)
	})
	mux.HandleFunc("DELETE /v3/jobs/{job}", func(writer http.ResponseWriter, request *http.Request) {
		job, err := backend.CancelJob(request.Context(), request.PathValue("job"))
		writeJob(writer, job, err)
	})
	mux.HandleFunc("GET /v3/jobs/{job}/artifacts/{artifact}", func(writer http.ResponseWriter, request *http.Request) {
		payload, err := backend.Artifact(request.Context(), request.PathValue("job"), request.PathValue("artifact"))
		if err != nil {
			writeError(writer, err)
			return
		}
		if int64(len(payload.Data)) != payload.Metadata.Size || len(payload.Data) > connectors.MaxArtifactBytes {
			writeError(writer, errors.New("backend artifact data does not match metadata"))
			return
		}
		mediaType := payload.Metadata.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		writer.Header().Set("Content-Type", mediaType)
		writer.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(payload.Metadata.Name, `"`, "")+`"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(payload.Data)
	})
	mux.HandleFunc("POST /v3/sessions", func(writer http.ResponseWriter, request *http.Request) {
		var input connectors.OpenSessionRequest
		if err := decodeRequest(writer, request, &input); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		session, err := backend.OpenSession(request.Context(), input)
		writeSession(writer, session, err)
	})
	mux.HandleFunc("POST /v3/sessions/{session}/input", func(writer http.ResponseWriter, request *http.Request) {
		var input connectors.SessionInputRequest
		if err := decodeRequest(writer, request, &input); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		session, err := backend.SessionInput(request.Context(), request.PathValue("session"), input)
		writeSession(writer, session, err)
	})
	mux.HandleFunc("POST /v3/sessions/{session}/resize", func(writer http.ResponseWriter, request *http.Request) {
		var input connectors.ResizeSessionRequest
		if err := decodeRequest(writer, request, &input); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		session, err := backend.ResizeSession(request.Context(), request.PathValue("session"), input)
		writeSession(writer, session, err)
	})
	mux.HandleFunc("DELETE /v3/sessions/{session}", func(writer http.ResponseWriter, request *http.Request) {
		session, err := backend.CloseSession(request.Context(), request.PathValue("session"))
		writeSession(writer, session, err)
	})
	return mux
}

func writeJob(writer http.ResponseWriter, job connectors.Job, err error) {
	if err != nil {
		writeError(writer, err)
		return
	}
	if err := connectors.ValidateJob(job); err != nil {
		writeError(writer, fmt.Errorf("invalid backend job: %w", err))
		return
	}
	writeJSON(writer, http.StatusOK, job)
}

func writeSession(writer http.ResponseWriter, session connectors.Session, err error) {
	if err != nil {
		writeError(writer, err)
		return
	}
	if err := connectors.ValidateSession(session); err != nil {
		writeError(writer, fmt.Errorf("invalid backend session: %w", err))
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid request")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
