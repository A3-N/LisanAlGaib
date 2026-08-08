package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithinRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mustWrite(t, outside, "outside")
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := ResolveWithin(root, link); ok {
		t.Fatal("symlink outside workspace must be rejected")
	}

	inside := filepath.Join(root, "inside.txt")
	mustWrite(t, inside, "safe")
	insideLink := filepath.Join(root, "inside-link.txt")
	if err := os.Symlink(inside, insideLink); err != nil {
		t.Fatal(err)
	}
	resolved, ok := ResolveWithin(root, insideLink)
	if !ok || resolved != inside {
		t.Fatalf("expected safe internal symlink, got %q, %v", resolved, ok)
	}
}

func TestSafeNameIsPortableAndCannotNavigate(t *testing.T) {
	for input, expected := range map[string]string{
		"../report.md":        "report.md",
		`..\windows.txt`:      "windows.txt",
		"..":                  "artifact.bin",
		"CON.txt":             "_CON.txt",
		"survey:final?.md":    "survey_final_.md",
		"trailing dots...   ": "trailing dots",
	} {
		if got := SafeName(input, "artifact.bin"); got != expected {
			t.Fatalf("SafeName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
