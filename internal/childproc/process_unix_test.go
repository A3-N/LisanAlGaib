//go:build !windows

package childproc

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestConfigureCancelsProcessGroupPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 30")
	Configure(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	cancel()
	if err := command.Wait(); err == nil {
		t.Fatal("cancelled command exited successfully")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process group cancellation took %s", elapsed)
	}
}
