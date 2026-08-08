package runtimebundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedArchiveIsOptionalForSourceBuilds(t *testing.T) {
	if Available() {
		t.Skip("release build contains a generated runtime")
	}
	if err := Extract(t.TempDir()); err == nil {
		t.Fatal("source build unexpectedly extracted a runtime")
	}
}

func TestArchivePathValidation(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := extractReader(bytes.NewReader(compressed.Bytes()), root); err == nil {
		t.Fatal("unsafe archive path was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped destination: %v", err)
	}
}

func TestRuntimeSourceFilterExcludesBuildOnlyFiles(t *testing.T) {
	for _, path := range []string{
		"cmd/lisan-runtime-pack",
		"cmd/lisan-runtime-pack/main.go",
		"internal/ui/model_test.go",
		"internal/terminal/session_windows.go",
		"internal/runtimebundle/assets/runtime.tar.gz",
	} {
		if IncludeSource(path, path == "cmd/lisan-runtime-pack") {
			t.Fatalf("build-only path included in runtime: %s", path)
		}
	}
	for _, path := range []string{"Dockerfile", "cmd/lisan/main.go", "internal/ui/model.go", "docker/lisan-entrypoint"} {
		if !IncludeSource(path, false) {
			t.Fatalf("runtime input was excluded: %s", path)
		}
	}
}
