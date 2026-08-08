//go:build !windows

package installer

import (
	"errors"
	"os"
)

func removeInstalledExecutable(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
