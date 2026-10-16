//go:build !windows

package daemonctl

import (
	"errors"
	"os/exec"
	"syscall"
)

// reaped reports whether cmd's process has exited, collecting it if so,
// without waiting.
func reaped(cmd *exec.Cmd) (bool, int) {
	pid := cmd.Process.Pid
	var status syscall.WaitStatus
	for {
		got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err == nil && got == pid, status.ExitStatus()
	}
}
