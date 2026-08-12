package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

func lineGraphemes(value string) []string {
	iterator := uniseg.NewGraphemes(value)
	result := make([]string, 0, utf8.RuneCountInString(value))
	for iterator.Next() {
		result = append(result, iterator.Str())
	}
	return result
}

func lineWithCursor(value string, cursor int) string {
	parts := lineGraphemes(value)
	cursor = min(max(cursor, 0), len(parts))
	parts = append(parts, "")
	copy(parts[cursor+1:], parts[cursor:])
	parts[cursor] = "▌"
	return strings.Join(parts, "")
}

func insertLineText(value string, cursor int, insertion string) (string, int) {
	parts := lineGraphemes(value)
	cursor = min(max(cursor, 0), len(parts))
	inserted := lineGraphemes(insertion)
	result := make([]string, 0, len(parts)+len(inserted))
	result = append(result, parts[:cursor]...)
	result = append(result, inserted...)
	result = append(result, parts[cursor:]...)
	return strings.Join(result, ""), cursor + len(inserted)
}

func editLine(value string, cursor int, key string) (string, int, bool) {
	text := ""
	if printableLineInput(key) {
		text = key
	}
	return editLineWithText(value, cursor, key, text)
}

func editLineWithText(value string, cursor int, key, text string) (string, int, bool) {
	parts := lineGraphemes(value)
	cursor = min(max(cursor, 0), len(parts))
	deleteRange := func(start, end int) (string, int, bool) {
		result := append([]string(nil), parts[:start]...)
		result = append(result, parts[end:]...)
		return strings.Join(result, ""), start, true
	}

	switch key {
	case "left":
		return value, max(cursor-1, 0), true
	case "right":
		return value, min(cursor+1, len(parts)), true
	case "home", "ctrl+a":
		return value, 0, true
	case "end", "ctrl+e":
		return value, len(parts), true
	case "ctrl+left", "alt+b":
		return value, previousWordBoundary(parts, cursor), true
	case "ctrl+right", "alt+f":
		return value, nextWordBoundary(parts, cursor), true
	case "backspace":
		if cursor == 0 {
			return value, cursor, true
		}
		return deleteRange(cursor-1, cursor)
	case "delete":
		if cursor == len(parts) {
			return value, cursor, true
		}
		return deleteRange(cursor, cursor+1)
	case "ctrl+backspace", "ctrl+w":
		start := previousWordBoundary(parts, cursor)
		return deleteRange(start, cursor)
	case "ctrl+delete", "alt+d":
		end := nextWordBoundary(parts, cursor)
		return deleteRange(cursor, end)
	case "ctrl+u":
		return deleteRange(0, cursor)
	case "ctrl+k":
		return deleteRange(cursor, len(parts))
	case "space":
		value, cursor = insertLineText(value, cursor, " ")
		return value, cursor, true
	}

	if text != "" && printableLineText(text) {
		value, cursor = insertLineText(value, cursor, text)
		return value, cursor, true
	}
	return value, cursor, false
}

func previousWordBoundary(parts []string, cursor int) int {
	position := min(max(cursor, 0), len(parts))
	for position > 0 && !lineWordCluster(parts[position-1]) {
		position--
	}
	for position > 0 && lineWordCluster(parts[position-1]) {
		position--
	}
	return position
}

func nextWordBoundary(parts []string, cursor int) int {
	position := min(max(cursor, 0), len(parts))
	for position < len(parts) && lineWordCluster(parts[position]) {
		position++
	}
	for position < len(parts) && !lineWordCluster(parts[position]) {
		position++
	}
	return position
}

func lineWordCluster(cluster string) bool {
	r, _ := utf8.DecodeRuneInString(cluster)
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
}

func printableLineInput(value string) bool {
	return len(lineGraphemes(value)) == 1 && printableLineText(value)
}

func printableLineText(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
