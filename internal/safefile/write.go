// Package safefile reads and writes bounded application state files. Writes do
// not expose a partially written destination; temporary and backup files stay
// in the destination directory so activation uses a same-filesystem rename.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Read(path string, maximum int64) ([]byte, error) {
	if maximum < 0 {
		return nil, errors.New("maximum file size cannot be negative")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return data, nil
}

func Write(path string, data []byte, directoryMode, fileMode fs.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".write-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(fileMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	return Replace(temporaryPath, path)
}

// Replace activates a completed temporary file. Both paths must be on the
// same filesystem. On success the temporary path no longer exists.
func Replace(temporaryPath, destination string) error {
	if err := os.Rename(temporaryPath, destination); err == nil {
		return nil
	}
	return replaceExisting(temporaryPath, destination)
}

// replaceExisting handles platforms such as Windows where Rename cannot
// replace an existing destination. The previous file is restored if activating
// the new one fails.
func replaceExisting(temporaryPath, destination string) error {
	backupFile, err := os.CreateTemp(filepath.Dir(destination), ".write-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("reserve backup path: %w", err)
	}
	backupPath := backupFile.Name()
	defer os.Remove(backupPath)
	if err := backupFile.Close(); err != nil {
		return fmt.Errorf("close backup reservation: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("release backup reservation: %w", err)
	}
	if err := os.Rename(destination, backupPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("back up existing file: %w", err)
		}
		if retryErr := os.Rename(temporaryPath, destination); retryErr != nil {
			return fmt.Errorf("activate new file: %w", retryErr)
		}
		return nil
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		if restoreErr := os.Rename(backupPath, destination); restoreErr != nil {
			return errors.Join(fmt.Errorf("activate new file: %w", err), fmt.Errorf("restore previous file: %w", restoreErr))
		}
		return fmt.Errorf("activate new file: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("remove previous file: %w", err)
	}
	return nil
}
