package skills

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lisanalgaib/internal/textsafe"
)

type Skill struct {
	Name        string
	Description string
	Path        string
	Scope       string
	Provider    string
	Valid       bool
	Error       string
}

func Scan(projectRoot string) []Skill {
	type rootSpec struct {
		path     string
		scope    string
		provider string
	}
	roots := []rootSpec{
		{filepath.Join(projectRoot, ".agents", "skills"), "workspace", "shared"},
		{filepath.Join(projectRoot, ".codex", "skills"), "workspace", "codex"},
		{filepath.Join(projectRoot, ".opencode", "skills"), "workspace", "opencode"},
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			rootSpec{filepath.Join(home, ".agents", "skills"), "user", "shared"},
			rootSpec{filepath.Join(home, ".codex", "skills"), "user", "codex"},
			rootSpec{filepath.Join(home, ".config", "opencode", "skills"), "user", "opencode"},
			rootSpec{filepath.Join(home, ".claude", "skills"), "user", "claude-compatible"},
		)
	}
	roots = append(roots, rootSpec{"/etc/codex/skills", "admin", "codex"})
	seen := make(map[string]bool)
	var result []Skill
	for _, root := range roots {
		_ = filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == ".tmp" || entry.Name() == "node_modules" || entry.Name() == "cache" {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.Name() != "SKILL.md" || seen[path] {
				return nil
			}
			seen[path] = true
			skill := parse(path)
			skill.Scope = root.scope
			skill.Provider = root.provider
			result = append(result, skill)
			return nil
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := strings.ToLower(result[i].Name), strings.ToLower(result[j].Name)
		if left == right {
			return result[i].Path < result[j].Path
		}
		return left < right
	})
	return result
}

func parse(path string) Skill {
	skill := Skill{Path: path, Name: filepath.Base(filepath.Dir(path))}
	file, err := os.Open(path)
	if err != nil {
		skill.Error = err.Error()
		return skill
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	inFrontmatter := false
	frontmatterSeen := false
	frontmatterClosed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			if !frontmatterSeen {
				frontmatterSeen = true
				inFrontmatter = true
				continue
			}
			if inFrontmatter {
				frontmatterClosed = true
				break
			}
		}
		if !inFrontmatter {
			if line != "" {
				break
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		}
	}
	if err := scanner.Err(); err != nil {
		skill.Error = err.Error()
		return skill
	}
	if !frontmatterSeen {
		skill.Error = "missing YAML frontmatter"
		return skill
	}
	if !frontmatterClosed {
		skill.Error = "unterminated YAML frontmatter"
		return skill
	}
	if skill.Name == "" || skill.Description == "" {
		skill.Error = "frontmatter requires name and description"
		return skill
	}
	skill.Name = textsafe.Label(skill.Name, 100)
	skill.Description = textsafe.Label(skill.Description, 300)
	if skill.Name == "" || skill.Description == "" {
		skill.Error = "frontmatter name and description contain no displayable text"
		return skill
	}
	skill.Valid = true
	return skill
}
