//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// windowResizes signals each change of this terminal's size (SIGWINCH).
func windowResizes(context.Context, uintptr) (<-chan os.Signal, func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	return winch, func() { signal.Stop(winch) }
}
