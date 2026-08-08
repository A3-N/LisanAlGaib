//go:build !windows

package nvimconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestXDGPathsAndNvChadDetection(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "config")
	dataRoot := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_DATA_HOME", dataRoot)
	config, err := ConfigDir()
	if err != nil || config != filepath.Join(configRoot, "nvim") {
		t.Fatalf("config path = %q, %v", config, err)
	}
	data, err := DataDir()
	if err != nil || data != filepath.Join(dataRoot, "nvim") {
		t.Fatalf("data path = %q, %v", data, err)
	}
	if NvChadInstalled() {
		t.Fatal("empty XDG roots reported NvChad")
	}
	if err := os.MkdirAll(filepath.Join(config, "lua"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "lua", "chadrc.lua"), []byte("return {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "lazy", "NvChad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !NvChadInstalled() {
		t.Fatal("complete NvChad paths were not detected")
	}
}
