//go:build windows

package childproc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func Configure(command *exec.Cmd) {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
			_ = exec.Command(taskkill, "/PID", fmt.Sprint(command.Process.Pid), "/T", "/F").Run()
		}
		return command.Process.Kill()
	}
	command.WaitDelay = time.Second
}
