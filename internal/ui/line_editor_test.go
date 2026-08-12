package ui

import "testing"

func TestEditLineSupportsNormalTerminalEditing(t *testing.T) {
	value := "spice flow"
	cursor := len(lineGraphemes(value))
	value, cursor, _ = editLine(value, cursor, "ctrl+left")
	if cursor != 6 {
		t.Fatalf("Ctrl-Left cursor = %d, want 6", cursor)
	}
	value, cursor, _ = editLine(value, cursor, "ctrl+delete")
	if value != "spice " || cursor != 6 {
		t.Fatalf("Ctrl-Delete = %q at %d", value, cursor)
	}
	value, cursor, _ = editLine(value, cursor, "left")
	value, cursor, _ = editLine(value, cursor, "ctrl+backspace")
	if value != " " || cursor != 0 {
		t.Fatalf("Ctrl-Backspace = %q at %d", value, cursor)
	}
}

func TestEditLineKeepsGraphemeClustersAtomic(t *testing.T) {
	value := "a👨‍👩‍👧‍👦é"
	cursor := len(lineGraphemes(value))
	if cursor != 3 {
		t.Fatalf("grapheme count = %d, want 3", cursor)
	}
	value, cursor, _ = editLine(value, cursor, "backspace")
	if value != "a👨‍👩‍👧‍👦" || cursor != 2 {
		t.Fatalf("grapheme backspace = %q at %d", value, cursor)
	}
	value, cursor = insertLineText(value, 1, "🔥")
	if value != "a🔥👨‍👩‍👧‍👦" || cursor != 2 {
		t.Fatalf("grapheme insert = %q at %d", value, cursor)
	}
}

func TestLineWithCursorInsertsAtLogicalPosition(t *testing.T) {
	if got := lineWithCursor("abc", 1); got != "a▌bc" {
		t.Fatalf("cursor rendering = %q", got)
	}
}

func TestEditLineUsesCommittedTextInsteadOfNamedKey(t *testing.T) {
	if value, _, handled := editLineWithText("", 0, "f1", ""); handled || value != "" {
		t.Fatalf("named key was inserted: %q", value)
	}
	value, cursor, handled := editLineWithText("", 0, "unknown", "مرحبا")
	if !handled || value != "مرحبا" || cursor != 5 {
		t.Fatalf("committed text = %q at %d, handled=%v", value, cursor, handled)
	}
}
