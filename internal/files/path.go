package files

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

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

// SafeName reduces an extension-provided artifact name to one portable file
// name. It strips path components for both slash conventions, neutralizes
// Windows-reserved characters/names, and never returns dot navigation.
func SafeName(value, fallback string) string {
	normalize := func(candidate string) string {
		candidate = path.Base(strings.ReplaceAll(candidate, `\`, "/"))
		candidate = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
				return '_'
			}
			return r
		}, candidate)
		return strings.TrimRight(strings.TrimSpace(candidate), ". ")
	}
	value = normalize(value)
	if value == "" || value == "." || value == ".." {
		value = normalize(fallback)
		if value == "" || value == "." || value == ".." {
			value = "artifact.bin"
		}
	}
	stem := value
	if index := strings.IndexByte(stem, '.'); index >= 0 {
		stem = stem[:index]
	}
	if windowsReservedNames[strings.ToUpper(stem)] {
		value = "_" + value
	}
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}
