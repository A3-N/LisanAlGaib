package launcher

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCleanupIsSafeWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var output bytes.Buffer
	if err := Cleanup(context.Background(), CleanupOptions{Stdout: &output, Stderr: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Cleaning Lisan Docker state · skipped") || !strings.Contains(output.String(), "native extensions already stop with Lisan") {
		t.Fatalf("cleanup did not explain its no-Docker result: %q", output.String())
	}
	if strings.Contains(output.String(), "[") || strings.Contains(output.String(), "]") {
		t.Fatalf("cleanup emitted a progress bar: %q", output.String())
	}
}

func TestCleanupOwnershipRequiresLisanOrExactComposeResource(t *testing.T) {
	for _, test := range []struct {
		labels   string
		resource string
		owned    bool
	}{
		{"1||", "usul", true},
		{"|arrakis|usul", "usul", true},
		{"|arrakis|default", "default", true},
		{"|another-project|usul", "usul", false},
		{"|arrakis|other", "usul", false},
		{"malformed", "usul", false},
	} {
		if got := composeResourceOwned(test.labels, test.resource); got != test.owned {
			t.Fatalf("composeResourceOwned(%q, %q) = %v, want %v", test.labels, test.resource, got, test.owned)
		}
	}
}

func TestCleanupDeduplicatesFullAndShortDockerImageIDs(t *testing.T) {
	full := "db4af071bce335c9b601a6e15db4229c53bf19f6e7c55da113df2bbeceef9b1d"
	short := full[:12]
	for _, values := range [][]string{
		{"sha256:" + full, short},
		{short, "sha256:" + full},
	} {
		images := map[string]bool{}
		for _, value := range values {
			addDockerImageID(images, value)
		}
		if len(images) != 1 || !images[full] {
			t.Fatalf("image forms %v produced cleanup targets %#v", values, images)
		}
	}
}

func TestCleanupRejectsMalformedDockerImageIDs(t *testing.T) {
	images := map[string]bool{}
	for _, value := range []string{"", "latest", "sha256:not-hex", "../../../image"} {
		addDockerImageID(images, value)
	}
	if len(images) != 0 {
		t.Fatalf("malformed image IDs became cleanup targets: %#v", images)
	}
}

func TestParseDockerActiveExecCount(t *testing.T) {
	for input, want := range map[string]int{"0": 0, " 2\n": 2} {
		got, err := parseDockerActiveExecCount(input)
		if err != nil || got != want {
			t.Fatalf("parseDockerActiveExecCount(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1", "many"} {
		if _, err := parseDockerActiveExecCount(input); err == nil {
			t.Fatalf("parseDockerActiveExecCount(%q) accepted invalid output", input)
		}
	}
}
