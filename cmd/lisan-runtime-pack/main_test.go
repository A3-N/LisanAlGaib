package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackIsDeterministicAndExcludesBuildOnlySource(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	if err := pack(root, first); err != nil {
		t.Fatal(err)
	}
	if err := pack(root, second); err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstData, secondData) {
		t.Fatal("identical source produced different runtime archives")
	}

	files := archiveFiles(t, firstData)
	for _, required := range []string{".dockerignore", "Dockerfile", "cmd/lisan/main.go", "docker/lisan-entrypoint"} {
		if !files[required] {
			t.Fatalf("runtime archive is missing %s", required)
		}
	}
	for path := range files {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_windows.go") || strings.HasPrefix(path, "cmd/lisan-runtime-pack/") {
			t.Fatalf("runtime archive contains build-only source %s", path)
		}
	}
}

func archiveFiles(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	files := map[string]bool{}
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			files[header.Name] = true
		}
	}
}
