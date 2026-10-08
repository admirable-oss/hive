package platform

import (
	"errors"
	"time"
)

var (
	// ErrNoProcess means no process has the requested PID.
	ErrNoProcess = errors.New("platform: no such process")
	// ErrUnsupported means this OS has no implementation of the call.
	ErrUnsupported = errors.New("platform: not supported on this OS")
)

// ProcessInfo is what Hive needs to recognise a process it launched earlier.
type ProcessInfo struct {
	PID  int
	PGID int
	// StartTime is when the kernel created the process. Together with the
	// PID it identifies a process even across PID reuse.
	StartTime time.Time
}

// LookupProcess returns information about the live process pid.
func LookupProcess(pid int) (ProcessInfo, error) {
	if pid <= 0 {
		return ProcessInfo{}, ErrNoProcess
	}
	return lookupProcess(pid)
}
