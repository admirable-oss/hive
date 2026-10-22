//go:build !windows

package updater

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// detach starts cmd in a session of its own, so it outlives its parent.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// waitExit waits until process pid has exited, or the time is up.
func waitExit(pid int, limit, poll time.Duration) {
	deadline := time.Now().Add(limit)
	for pid > 0 && time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
			return
		}
		time.Sleep(poll)
	}
}
