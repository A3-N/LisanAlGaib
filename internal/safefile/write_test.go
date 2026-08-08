package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesAndReplacesProtectedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	for _, content := range []string{"first\n", "second\n"} {
		if err := Write(path, []byte(content), 0o700, 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content {
			t.Fatalf("got %q, want %q", data, content)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode is %o", info.Mode().Perm())
	}
}

func TestReadEnforcesLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path, 4); err == nil {
		t.Fatal("oversized state file was accepted")
	}
	data, err := Read(path, 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("bounded read = %q, %v", data, err)
	}
}
