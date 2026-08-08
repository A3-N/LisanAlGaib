package textsafe

import "testing"

func TestLabelsRemoveControlsAndIconsRetainNerdFontGlyphs(t *testing.T) {
	if got := Label(" safe\x1b\u202e\ue000 ", 20); got != "safe" {
		t.Fatalf("unsafe label = %q", got)
	}
	if got := Icon("󰒋\u202e", 8); got != "󰒋" {
		t.Fatalf("safe icon = %q", got)
	}
	if got := Output("one\x1b[31m\ntwo\t\u202e"); got != "one[31m\ntwo\t" {
		t.Fatalf("safe output = %q", got)
	}
}

func TestRuneLimitsDoNotSplitUnicode(t *testing.T) {
	if got := Label("sandworm", 4); got != "sand" {
		t.Fatalf("limited label = %q", got)
	}
}
