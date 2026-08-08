package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"lisanalgaib/internal/extensionbundle"
	"lisanalgaib/internal/runtimebundle"
)

func main() {
	if len(os.Args) == 5 && (os.Args[1] == "prepare" || os.Args[1] == "clean") {
		if err := manageNativeExtensions(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, "lisan-runtime-pack:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: lisan-runtime-pack ROOT OUTPUT")
		os.Exit(2)
	}
	if err := pack(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "lisan-runtime-pack:", err)
		os.Exit(1)
	}
}

func manageNativeExtensions(action, root, goos, goarch string) error {
	bundles, err := extensionbundle.Discover(root)
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		if bundle.Native.SourcePackage == "" {
			continue
		}
		relative := strings.ReplaceAll(bundle.Native.Executable, "${os}", goos)
		relative = strings.ReplaceAll(relative, "${arch}", goarch)
		output := filepath.Join(root, filepath.FromSlash(relative))
		if action == "clean" {
			if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			_ = os.Remove(filepath.Dir(output))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		packagePath := "./" + strings.TrimPrefix(filepath.ToSlash(bundle.Native.SourcePackage), "./")
		command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", output, packagePath)
		command.Dir = root
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("build native extension %s: %w: %s", bundle.ID, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func pack(root, output string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	var paths []string
	for _, relative := range runtimebundle.SourceRoots() {
		start := filepath.Join(root, relative)
		if err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if samePath(path, output) || !runtimebundle.IncludeSource(relative, entry.IsDir()) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symbolic link in runtime: %s", path)
			}
			paths = append(paths, path)
			return nil
		}); err != nil {
			return err
		}
	}
	sort.Strings(paths)
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".runtime-*.tar.gz")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()
	gz, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime = time.Unix(0, 0)
	gz.Header.OS = 255
	tw := tar.NewWriter(gz)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			return fmt.Errorf("runtime path escaped root: %s", path)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		if removeErr := os.Remove(output); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, removeErr)
		}
		if err := os.Rename(temporaryPath, output); err != nil {
			return err
		}
	}
	ok = true
	return nil
}

func samePath(first, second string) bool {
	first, second = filepath.Clean(first), filepath.Clean(second)
	return first == second || (runtime.GOOS == "windows" && strings.EqualFold(first, second))
}
