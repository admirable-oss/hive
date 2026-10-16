//go:build !windows

package pgroup

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func started(*exec.Cmd) error { return nil }

func release(int) {}

// Kill stops the group led by pid at once (SIGKILL).
func Kill(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// TerminateAfter sends SIGTERM to the process group led by pid and schedules
// a SIGKILL for whatever is left after grace. It does not block. A group that
// is already gone is not an error.
func TerminateAfter(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already gone
		}
		return err
	}
	// POSIX does not reuse a PID while a process group with that ID exists,
	// so the delayed group kill cannot hit an unrelated process.
	time.AfterFunc(grace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	return nil
}

// Alive reports whether a process group with id pgid still has members.
// EPERM means the group exists but belongs to another user.
func Alive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
