package extensionhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"

	"lisanalgaib/internal/connectors"
)

func TestProtocolV3HandlerExposesEverySurface(t *testing.T) {
	backend := &testBackend{}
	handler := Handler(backend)
	requests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/v3/health", "", http.StatusOK},
		{http.MethodGet, "/v3/manifest", "", http.StatusOK},
		{http.MethodGet, "/v3/views/overview", "", http.StatusOK},
		{http.MethodPost, "/v3/jobs", `{"action_id":"survey"}`, http.StatusAccepted},
		{http.MethodGet, "/v3/jobs/job-1", "", http.StatusOK},
		{http.MethodDelete, "/v3/jobs/job-1", "", http.StatusOK},
		{http.MethodGet, "/v3/jobs/job-1/artifacts/report", "", http.StatusOK},
		{http.MethodPost, "/v3/sessions", `{"session_id":"console","rows":20,"columns":80}`, http.StatusOK},
		{http.MethodPost, "/v3/sessions/session-1/input", `{"input":"help\\n"}`, http.StatusOK},
		{http.MethodPost, "/v3/sessions/session-1/resize", `{"rows":30,"columns":100}`, http.StatusOK},
		{http.MethodDelete, "/v3/sessions/session-1", "", http.StatusOK},
	}
	for _, test := range requests {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s returned %d: %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestProtocolV3HandlerIsStrictAndBounded(t *testing.T) {
	handler := Handler(&testBackend{})
	for _, body := range []string{`{"action_id":"survey","unknown":true}`, `{"action_id":"survey"} {}`} {
		request := httptest.NewRequest(http.MethodPost, "/v3/jobs", bytes.NewBufferString(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request returned %d", response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v3/views/missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing backend value returned %d", response.Code)
	}
}

func TestProtocolV3HandlerRejectsInvalidBackendData(t *testing.T) {
	backend := &testBackend{invalidManifest: true}
	response := httptest.NewRecorder()
	Handler(backend).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v3/manifest", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("invalid backend manifest returned %d", response.Code)
	}
}

func TestProtocolV3HandlerSanitizesArtifactFilename(t *testing.T) {
	response := httptest.NewRecorder()
	Handler(&testBackend{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v3/jobs/job-1/artifacts/report", nil))
	mediaType, parameters, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || mediaType != "attachment" || parameters["filename"] != "report_.md" {
		t.Fatalf("unsafe artifact disposition %q: type=%q filename=%q err=%v", response.Header().Get("Content-Disposition"), mediaType, parameters["filename"], err)
	}
}

type testBackend struct{ invalidManifest bool }

func (b *testBackend) Manifest(context.Context) (connectors.Manifest, error) {
	manifest := connectors.Manifest{ProtocolVersion: connectors.ProtocolVersion, ID: "test", Name: "Test", Version: "3.0.0", Views: []connectors.ViewDescriptor{{ID: "overview", Title: "Overview"}}, Actions: []connectors.ActionDescriptor{{ID: "survey", Name: "Survey"}}, Sessions: []connectors.SessionDescriptor{{ID: "console", Name: "Console"}}}
	if b.invalidManifest {
		manifest.ProtocolVersion = 2
	}
	return manifest, nil
}

func (b *testBackend) View(_ context.Context, id string) (connectors.View, error) {
	if id != "overview" {
		return connectors.View{}, ErrNotFound
	}
	return connectors.View{ID: id, Title: "Overview", Blocks: []connectors.Block{{ID: "ready", Kind: connectors.BlockStatus, Text: "ready"}}}, nil
}

func (b *testBackend) StartJob(context.Context, connectors.StartJobRequest) (connectors.Job, error) {
	return b.job(), nil
}
func (b *testBackend) Job(context.Context, string) (connectors.Job, error)       { return b.job(), nil }
func (b *testBackend) CancelJob(context.Context, string) (connectors.Job, error) { return b.job(), nil }
func (b *testBackend) Artifact(context.Context, string, string) (ArtifactPayload, error) {
	data := []byte("report")
	sum := sha256.Sum256(data)
	return ArtifactPayload{Metadata: connectors.Artifact{ID: "report", Name: `report".md`, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, Data: data}, nil
}
func (b *testBackend) OpenSession(context.Context, connectors.OpenSessionRequest) (connectors.Session, error) {
	return b.session("open"), nil
}
func (b *testBackend) SessionInput(context.Context, string, connectors.SessionInputRequest) (connectors.Session, error) {
	return b.session("open"), nil
}
func (b *testBackend) ResizeSession(context.Context, string, connectors.ResizeSessionRequest) (connectors.Session, error) {
	return b.session("open"), nil
}
func (b *testBackend) CloseSession(context.Context, string) (connectors.Session, error) {
	return b.session("closed"), nil
}
func (b *testBackend) job() connectors.Job {
	return connectors.Job{ID: "job-1", ActionID: "survey", Status: connectors.JobSucceeded, Progress: 100}
}
func (b *testBackend) session(status string) connectors.Session {
	return connectors.Session{ID: "session-1", SessionID: "console", Status: status}
}

func TestWriteJSONProducesJSON(t *testing.T) {
	response := httptest.NewRecorder()
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	var value map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || value["status"] != "ok" {
		t.Fatalf("invalid JSON response: %s %v", response.Body.String(), err)
	}
}
