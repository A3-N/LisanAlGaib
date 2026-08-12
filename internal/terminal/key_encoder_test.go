package terminal

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestEncodeCompatibleModifiedKeys(t *testing.T) {
	tests := []struct {
		name string
		key  uv.Key
		want string
	}{
		{"ctrl left", uv.Key{Code: uv.KeyLeft, Mod: uv.ModCtrl}, "\x1b[1;5D"},
		{"ctrl right", uv.Key{Code: uv.KeyRight, Mod: uv.ModCtrl}, "\x1b[1;5C"},
		{"alt up", uv.Key{Code: uv.KeyUp, Mod: uv.ModAlt}, "\x1b[1;3A"},
		{"shift ctrl down", uv.Key{Code: uv.KeyDown, Mod: uv.ModShift | uv.ModCtrl}, "\x1b[1;6B"},
		{"ctrl home", uv.Key{Code: uv.KeyHome, Mod: uv.ModCtrl}, "\x1b[1;5H"},
		{"ctrl delete", uv.Key{Code: uv.KeyDelete, Mod: uv.ModCtrl}, "\x1b[3;5~"},
		{"alt page down", uv.Key{Code: uv.KeyPgDown, Mod: uv.ModAlt}, "\x1b[6;3~"},
		{"shift f1", uv.Key{Code: uv.KeyF1, Mod: uv.ModShift}, "\x1b[1;2P"},
		{"ctrl f12", uv.Key{Code: uv.KeyF12, Mod: uv.ModCtrl}, "\x1b[24;5~"},
		{"shift tab", uv.Key{Code: uv.KeyTab, Mod: uv.ModShift}, "\x1b[Z"},
		{"ctrl backspace", uv.Key{Code: uv.KeyBackspace, Mod: uv.ModCtrl}, "\x17"},
		{"alt ctrl backspace", uv.Key{Code: uv.KeyBackspace, Mod: uv.ModAlt | uv.ModCtrl}, "\x1b\x17"},
		{"super left", uv.Key{Code: uv.KeyLeft, Mod: uv.ModSuper}, "\x1b[1;9D"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := encodeCompatibleKey(test.key)
			if !ok || got != test.want {
				t.Fatalf("encoded key = %q, %v; want %q, true", got, ok, test.want)
			}
		})
	}
}

func TestEncodeCompatibleKeyLeavesTextAndUnmodifiedKeysToEmulator(t *testing.T) {
	for _, key := range []uv.Key{
		{Code: 'a', Text: "a"},
		{Code: uv.KeyLeft},
		{Code: uv.KeyBackspace},
		{Code: 'c', Mod: uv.ModCtrl},
	} {
		if sequence, ok := encodeCompatibleKey(key); ok {
			t.Fatalf("key %#v was unexpectedly encoded as %q", key, sequence)
		}
	}
}
