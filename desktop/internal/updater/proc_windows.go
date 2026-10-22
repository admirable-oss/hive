//go:build windows

package updater

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// detach starts cmd without a console and in a process group of its own, so
// it outlives its parent.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}

// waitExit waits until process pid has exited, or the time is up. On
// Windows a process handle can be waited on whoever started it.
func waitExit(pid int, limit, _ time.Duration) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = p.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(limit):
	}
}
