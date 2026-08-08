package settings

import (
	"path/filepath"
	"testing"

	"lisanalgaib/internal/appconfig"
)

func TestSaveAndLoad(t *testing.T) {
	t.Setenv(appconfig.EnvironmentConfig, filepath.Join(t.TempDir(), "config.json"))
	want := Settings{Theme: "Caladan"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
