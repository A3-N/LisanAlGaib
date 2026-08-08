package files

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveWithin follows symlinks only when their final target remains inside
// root. Only regular files are accepted.
func ResolveWithin(root, path string) (string, bool) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return resolvedPath, true
}
