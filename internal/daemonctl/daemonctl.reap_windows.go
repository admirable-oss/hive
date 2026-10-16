//go:build windows

package daemonctl

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// reaped reports whether cmd's process has exited, without waiting.
func reaped(cmd *exec.Cmd) (bool, int) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		return false, 0
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code == 259 { // STILL_ACTIVE
		return false, 0
	}
	return true, int(code)
}
