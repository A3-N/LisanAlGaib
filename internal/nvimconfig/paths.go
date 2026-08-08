// Package nvimconfig centralizes the platform-specific paths shared by the
// installer, inventory, and embedded editor launcher.
package nvimconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const ChocolateBackground = "#252221"

func ConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		root, err := windowsDataRoot()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "nvim"), nil
	}
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "nvim"), nil
}

func DataDir() (string, error) {
	if runtime.GOOS == "windows" {
		root, err := windowsDataRoot()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "nvim-data"), nil
	}
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "nvim"), nil
}

func windowsDataRoot() (string, error) {
	if root := os.Getenv("LOCALAPPDATA"); root != "" {
		return root, nil
	}
	return "", errors.New("LOCALAPPDATA is not defined")
}

func NvChadInstalled() bool {
	config, configErr := ConfigDir()
	data, dataErr := DataDir()
	if configErr != nil || dataErr != nil {
		return false
	}
	return regularFile(filepath.Join(config, "lua", "chadrc.lua")) &&
		directory(filepath.Join(data, "lazy", "NvChad"))
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func directory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
