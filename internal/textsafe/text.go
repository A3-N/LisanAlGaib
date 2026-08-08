// Package textsafe normalizes untrusted text before it reaches a terminal.
package textsafe

import (
	"strings"
	"unicode"
)

// Label returns trimmed single-line display text. Terminal controls, Unicode
// formatting controls, private-use glyphs, and surrogate code points are
// removed before the rune limit is applied.
func Label(value string, maximum int) string {
	return clean(strings.TrimSpace(value), maximum, false, false)
}

// Icon is equivalent to Label but retains Unicode private-use code points,
// where Nerd Font stores its interface glyphs.
func Icon(value string, maximum int) string {
	return clean(strings.TrimSpace(value), maximum, true, false)
}

// Output preserves newlines and tabs while removing every other unsafe
// terminal or Unicode formatting code point.
func Output(value string) string {
	return clean(value, -1, false, true)
}

func clean(value string, maximum int, allowPrivateUse, allowLayout bool) string {
	value = strings.Map(func(r rune) rune {
		if allowLayout && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Cs) || (!allowPrivateUse && unicode.In(r, unicode.Co)) {
			return -1
		}
		return r
	}, value)
	if maximum < 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > maximum {
		runes = runes[:maximum]
	}
	return string(runes)
}
