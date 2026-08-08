//go:build windows

package installer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

func removeInstalledExecutable(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	executable, executableErr := os.Executable()
	if executableErr != nil {
		return err
	}
	same, sameErr := sameFile(executable, path)
	if sameErr != nil || !same {
		return err
	}
	script := `& { param([int]$ProcessId, [string]$Target) Wait-Process -Id $ProcessId -ErrorAction SilentlyContinue; Remove-Item -LiteralPath $Target -Force -ErrorAction SilentlyContinue }`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script, strconv.Itoa(os.Getpid()), path)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
	if startErr := command.Start(); startErr != nil {
		return fmt.Errorf("schedule executable removal: %w", startErr)
	}
	return nil
}
