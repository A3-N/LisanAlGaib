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
	if !strings.Contains(output.String(), "native extensions already stop with Lisan") {
		t.Fatalf("cleanup did not explain its no-Docker result: %q", output.String())
	}
}
