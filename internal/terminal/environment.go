package terminal

import (
	"strings"

	"lisanalgaib/internal/textsafe"
)

func cleanTitle(value string) string {
	return textsafe.Label(value, 120)
}

// Environment returns base with each KEY=value override applied exactly once.
func Environment(base []string, overrides ...string) []string {
	result := append([]string(nil), base...)
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok || key == "" {
			continue
		}
		filtered := result[:0]
		for _, item := range result {
			itemKey, _, _ := strings.Cut(item, "=")
			if !sameEnvironmentKey(itemKey, key) {
				filtered = append(filtered, item)
			}
		}
		result = append(filtered, override)
	}
	return result
}

// EnvironmentWithout removes variables which would make a nested application
// believe it can speak directly to the outer Kitty/Tmux/Neovim instance.
func EnvironmentWithout(base []string, keys ...string) []string {
	result := make([]string, 0, len(base))
	for _, item := range base {
		itemKey, _, _ := strings.Cut(item, "=")
		remove := false
		for _, key := range keys {
			if sameEnvironmentKey(itemKey, key) {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, item)
		}
	}
	return result
}

func sameEnvironmentKey(left, right string) bool {
	if caseInsensitiveEnvironment {
		return strings.EqualFold(left, right)
	}
	return left == right
}
