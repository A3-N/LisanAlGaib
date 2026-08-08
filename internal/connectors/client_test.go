package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lisanalgaib/internal/appconfig"
)

func TestProtocolV3ClientSurfaces(t *testing.T) {
	artifactData := []byte("field report\n")
	sum := sha256.Sum256(artifactData)
	artifact := Artifact{ID: "report", Name: "report.md", Size: int64(len(artifactData)), SHA256: hex.EncodeToString(sum[:])}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /v3/manifest":
			_ = json.NewEncoder(writer).Encode(validManifest())
		case "GET /v3/views/overview":
			_ = json.NewEncoder(writer).Encode(View{ID: "overview", Title: "Overview", Blocks: []Block{{ID: "state", Kind: BlockStatus, Text: "ready\x1b"}}})
		case "POST /v3/jobs":
			_ = json.NewEncoder(writer).Encode(Job{ID: "job-1", ActionID: "survey", Status: JobRunning, Progress: 50})
		case "GET /v3/jobs/job-1":
			_ = json.NewEncoder(writer).Encode(Job{ID: "job-1", ActionID: "survey", Status: JobSucceeded, Progress: 100, Artifacts: []Artifact{artifact}})
		case "GET /v3/jobs/job-1/artifacts/report":
			writer.Header().Set("Content-Type", "text/markdown")
			_, _ = writer.Write(artifactData)
		case "POST /v3/sessions":
			_ = json.NewEncoder(writer).Encode(Session{ID: "session-1", SessionID: "console", Status: "open", Output: "ready"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	manifest, err := FetchManifest(context.Background(), server.URL)
	if err != nil || manifest.ID != "test" {
		t.Fatalf("manifest: %#v %v", manifest, err)
	}
	view, err := FetchView(context.Background(), server.URL, "overview")
	if err != nil || view.Blocks[0].Text != "ready" {
		t.Fatalf("view was not fetched and sanitized: %#v %v", view, err)
	}
	job, err := StartJob(context.Background(), server.URL, StartJobRequest{ActionID: "survey"})
	if err != nil || job.Progress != 50 {
		t.Fatalf("start job: %#v %v", job, err)
	}
	job, err = FetchJob(context.Background(), server.URL, job.ID)
	if err != nil || !job.Terminal() {
		t.Fatalf("fetch job: %#v %v", job, err)
	}
	data, err := FetchArtifact(context.Background(), server.URL, job.ID, job.Artifacts[0])
	if err != nil || !bytes.Equal(data, artifactData) {
		t.Fatalf("artifact: %q %v", data, err)
	}
	session, err := OpenSession(context.Background(), server.URL, OpenSessionRequest{SessionID: "console", Rows: 20, Columns: 80})
	if err != nil || session.Status != "open" {
		t.Fatalf("session: %#v %v", session, err)
	}
}

func TestScanFetchesOnlyEnabledExtensionsAndViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v3/manifest":
			_ = json.NewEncoder(writer).Encode(validManifest())
		case "/v3/views/overview":
			_ = json.NewEncoder(writer).Encode(View{ID: "overview", Title: "Overview"})
		}
	}))
	defer server.Close()
	states := Scan(context.Background(), []appconfig.ConnectorConfig{{ID: "off", Enabled: false, Endpoint: "not-a-url"}, {ID: "test", Enabled: true, Endpoint: server.URL}})
	if len(states) != 1 || !states[0].Online || states[0].Views["overview"].Title != "Overview" {
		t.Fatalf("unexpected scan: %#v", states)
	}
}

func TestProtocolV3ValidationRejectsAmbiguousOrUnsafeData(t *testing.T) {
	manifest := validManifest()
	manifest.ProtocolVersion = 2
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("old protocol manifest was accepted")
	}
	manifest = validManifest()
	manifest.Views = append(manifest.Views, manifest.Views[0])
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("duplicate view was accepted")
	}
	manifest = validManifest()
	manifest.Actions[0].Inputs = []InputSpec{{ID: "choice", Label: "Choice", Kind: InputSelect}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("empty select was accepted")
	}
	malformedTable := View{ID: "view", Title: "View", Blocks: []Block{
		{ID: "table", Kind: BlockTable, Columns: []Column{{ID: "one", Title: "One"}}, Rows: [][]string{{"one", "two"}}},
	}}
	if err := ValidateView(malformedTable); err == nil {
		t.Fatal("malformed table was accepted")
	}
	if err := ValidateJob(Job{ID: "job", ActionID: "action", Status: JobSucceeded, Progress: 100, Artifacts: []Artifact{{ID: "bad", Name: "bad", SHA256: "not-a-checksum"}}}); err == nil {
		t.Fatal("invalid artifact metadata was accepted")
	}
}

func TestProtocolV3ClientBoundsAndEndpointValidation(t *testing.T) {
	if err := decodeLimited(bytes.NewReader(make([]byte, MaxResponseBytes+1)), &Manifest{}); err == nil {
		t.Fatal("oversized extension response was accepted")
	}
	if err := decodeLimited(strings.NewReader(`{} {}`), &Manifest{}); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
	if _, err := StartJob(context.Background(), "http://127.0.0.1", StartJobRequest{ActionID: "bad action"}); err == nil {
		t.Fatal("invalid action id reached the network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := FetchManifest(ctx, "file:///tmp/socket"); err == nil {
		t.Fatal("non-HTTP endpoint was accepted")
	}
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(validManifest())
	}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.RedirectHandler(redirectTarget.URL, http.StatusFound))
	defer redirector.Close()
	if _, err := FetchManifest(context.Background(), redirector.URL); err == nil {
		t.Fatal("extension protocol redirect was followed")
	}
}

func validManifest() Manifest {
	return Manifest{
		ProtocolVersion: ProtocolVersion, ID: "test", Name: "Test", Version: "3.0.0",
		Views:    []ViewDescriptor{{ID: "overview", Title: "Overview", Default: true}},
		Actions:  []ActionDescriptor{{ID: "survey", Name: "Survey"}},
		Sessions: []SessionDescriptor{{ID: "console", Name: "Console"}},
	}
}
