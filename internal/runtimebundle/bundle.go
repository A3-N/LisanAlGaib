package runtimebundle

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const archivePath = "assets/runtime.tar.gz"

const (
	maxRuntimeEntries      = 10_000
	maxRuntimeEntryBytes   = 128 << 20
	maxRuntimeArchiveBytes = 512 << 20
)

var sourceRoots = []string{".dockerignore", "Dockerfile", "compose.yaml", "go.mod", "go.sum", "cmd", "internal", "docker", "extensions"}

// assets contains the generated release runtime when scripts/build-release is
// used. Source and container builds retain only the placeholder and fall back
// to the checkout runtime.
//
//go:embed assets/*
var assets embed.FS

// Available reports whether this executable contains a release runtime.
func Available() bool {
	info, err := fs.Stat(assets, archivePath)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// SourceRoots returns the files and directories required to build the Linux
// Docker workspace and its managed extensions.
func SourceRoots() []string {
	return append([]string(nil), sourceRoots...)
}

// IncludeSource reports whether a source-relative runtime path belongs in an
// installed runtime. Release tooling, tests, generated archives, and
// Windows-only Go files are unnecessary for Linux container builds.
func IncludeSource(relative string, directory bool) bool {
	relative = filepath.ToSlash(filepath.Clean(relative))
	if relative == "cmd/lisan-runtime-pack" || strings.HasPrefix(relative, "cmd/lisan-runtime-pack/") {
		return false
	}
	if relative == "internal/runtimebundle/assets/runtime.tar.gz" {
		return false
	}
	if strings.HasPrefix(relative, "internal/runtimebundle/assets/.runtime-") && strings.HasSuffix(relative, ".tar.gz") {
		return false
	}
	if !directory && (strings.HasSuffix(relative, "_test.go") || strings.HasSuffix(relative, "_windows.go")) {
		return false
	}
	return true
}

// Extract expands the embedded runtime into destination. Only regular files
// and directories with relative paths are accepted.
func Extract(destination string) error {
	archive, err := assets.Open(archivePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("this executable does not contain an embedded Docker runtime")
		}
		return err
	}
	defer archive.Close()
	return extractReader(archive, destination)
}

func extractReader(archive io.Reader, destination string) error {
	return extractReaderWithLimits(archive, destination, maxRuntimeEntryBytes, maxRuntimeArchiveBytes)
}

func extractReaderWithLimits(archive io.Reader, destination string, maxEntryBytes, maxArchiveBytes int64) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open embedded runtime: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var entries int
	var extracted int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read embedded runtime: %w", nextErr)
		}
		entries++
		if entries > maxRuntimeEntries {
			return fmt.Errorf("embedded runtime exceeds %d entries", maxRuntimeEntries)
		}
		if header.Size < 0 || header.Size > maxEntryBytes || extracted > maxArchiveBytes-header.Size {
			return fmt.Errorf("embedded runtime entry %q exceeds extraction limits", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Size != 0 {
			return fmt.Errorf("embedded runtime entry %q has data for a non-file type", header.Name)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("embedded runtime contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(header.Mode&0o755)); err != nil {
				return err
			}
		case tar.TypeReg:
			extracted += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(header.Mode&0o755))
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				_ = os.Remove(target)
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("embedded runtime contains unsupported entry %q", header.Name)
		}
	}
	return nil
}
