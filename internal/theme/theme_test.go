package theme

import (
	"regexp"
	"testing"
)

func TestThemesHaveUniqueNamesAndCompleteHexColours(t *testing.T) {
	hex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	seen := map[string]bool{}
	for _, current := range All {
		if current.Name == "" || seen[current.Name] {
			t.Fatalf("invalid or duplicate theme name %q", current.Name)
		}
		seen[current.Name] = true
		for field, value := range map[string]string{
			"background": current.Background, "surface": current.Surface,
			"panel": current.Panel, "border": current.Border, "text": current.Text,
			"muted": current.Muted, "primary": current.Primary, "secondary": current.Secondary,
			"success": current.Success, "warning": current.Warning, "danger": current.Danger,
			"selection": current.Selection,
		} {
			if !hex.MatchString(value) {
				t.Fatalf("theme %q %s colour = %q", current.Name, field, value)
			}
		}
	}
	if _, index := ByName("does-not-exist"); index != 0 {
		t.Fatal("unknown theme did not fall back to Arrakis")
	}
	if migrated, _ := ByName("Nord"); migrated.Name != "Caladan" {
		t.Fatalf("legacy theme name was not migrated: %#v", migrated)
	}
}
