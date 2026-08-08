//go:build !windows

package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// NotifyContext covers keyboard interrupts, service-manager termination, and
// terminal/SSH closure on Unix hosts.
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
}
