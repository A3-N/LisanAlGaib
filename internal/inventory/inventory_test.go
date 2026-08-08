package inventory

import (
	"context"
	"strings"
	"testing"

	"lisanalgaib/internal/appconfig"
)

func TestSafeVersionReadsFirstLine(t *testing.T) {
	got := safeVersion(context.Background(), "/bin/echo", []string{"version one\x1b\nversion two"})
	if got != "version one" {
		t.Fatalf("got %q", got)
	}
}

func TestInventoryCoversEverySelectableTool(t *testing.T) {
	seen := map[string]bool{"nvchad": true}
	for _, candidate := range specs {
		seen[candidate.ID] = true
	}
	for _, option := range appconfig.Options {
		if option.Category == appconfig.Tools && !seen[option.ID] {
			t.Fatalf("selectable tool %q has no inventory probe", option.ID)
		}
	}
}

func TestSpecsHaveUniqueIDsAndKnownCommands(t *testing.T) {
	seen := make(map[string]bool)
	for _, candidate := range specs {
		if candidate.ID == "" || candidate.Command == "" {
			t.Fatalf("invalid tool spec: %#v", candidate)
		}
		if seen[candidate.ID] {
			t.Fatalf("duplicate tool id %q", candidate.ID)
		}
		seen[candidate.ID] = true
		if strings.ContainsAny(candidate.Command, " \t\n") {
			t.Fatalf("command must be an executable name, not a shell string: %q", candidate.Command)
		}
	}
}

func TestSelectedScanOnlyInspectsRequestedTools(t *testing.T) {
	snapshot := ScanSelected(context.Background(), Selection{
		IDs: map[string]bool{"git": true},
	})
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].ID != "git" {
		t.Fatalf("selected scan leaked unconfigured tools: %#v", snapshot.Tools)
	}
	if snapshot.APTManual != nil {
		t.Fatalf("selected scan unexpectedly checked apt: %#v", snapshot.APTManual)
	}
}
