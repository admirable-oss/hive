// Package pgroup stops whole process groups.
//
// Agents are usually launched through wrappers (sh -c, npx, a CLI that forks
// workers). Killing only the wrapper would orphan the real agent, so Hive
// starts every agent as the leader of its own group and signals the group.
package pgroup

import (
	"errors"
	"syscall"
	"time"
)

// Grace is how long Terminate waits after SIGTERM before sending SIGKILL.
const Grace = 3 * time.Second

// Terminate is TerminateAfter with the default Grace.
func Terminate(pid int) error { return TerminateAfter(pid, Grace) }

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
