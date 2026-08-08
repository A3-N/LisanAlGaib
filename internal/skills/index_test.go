package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanWorkspaceSkills(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, ".agents", "skills", "go-review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(validPath), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: go-review\ndescription: Review Go code for correctness\n---\n\nDo the review.\n"
	if err := os.WriteFile(validPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var found *Skill
	for _, candidate := range Scan(root) {
		if candidate.Path == validPath {
			copy := candidate
			found = &copy
			break
		}
	}
	if found == nil {
		t.Fatal("workspace skill not found")
	}
	if !found.Valid || found.Name != "go-review" || found.Scope != "workspace" || found.Provider != "shared" {
		t.Fatalf("unexpected skill: %#v", *found)
	}
}

func TestInvalidSkillIsIndexedWithError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".agents", "skills", "broken", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("no frontmatter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range Scan(root) {
		if candidate.Path == path {
			if candidate.Valid || candidate.Error == "" {
				t.Fatalf("expected invalid indexed skill, got %#v", candidate)
			}
			return
		}
	}
	t.Fatal("invalid skill should still be visible")
}

func TestUnterminatedFrontmatterIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: broken\ndescription: never closed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := parse(path)
	if skill.Valid || skill.Error != "unterminated YAML frontmatter" {
		t.Fatalf("unexpected parse result: %#v", skill)
	}
}

func TestSkillControlsAreRemovedAndSymlinksIgnored(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".agents", "skills", "safe")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "SKILL.md")
	content := "---\nname: safe\x1b[31m\ndescription: left\u202eright\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := parse(path)
	if !skill.Valid || strings.ContainsAny(skill.Name+skill.Description, "\x1b\u202e") {
		t.Fatalf("unsafe skill metadata survived: %#v", skill)
	}

	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("---\nname: outside\ndescription: must not be indexed\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDirectory := filepath.Join(root, ".agents", "skills", "linked")
	if err := os.MkdirAll(linkDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDirectory, "SKILL.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range Scan(root) {
		if candidate.Path == link {
			t.Fatal("symlinked skill manifest escaped its configured root")
		}
	}
}
