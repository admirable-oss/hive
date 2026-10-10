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

// ProcessCwd returns the working directory of the live process pid, so a
// new pane can start where the user is working (`cd` in a shell changes
// it). It needs no special privilege for the user's own processes.
func ProcessCwd(pid int) (string, error) {
	if pid <= 0 {
		return "", ErrNoProcess
	}
	return processCwd(pid)
}

// ProcessArgs returns the argument vector of the live process pid, argv[0]
// first. Interpreted programs show their interpreter first (node, python),
// with the script after it.
func ProcessArgs(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, ErrNoProcess
	}
	return processArgs(pid)
}

// ForegroundGroup returns the process group in the foreground of the
// terminal whose file descriptor is fd (usually a PTY master): the job a
// shell is running, or the shell itself at its prompt.
func ForegroundGroup(fd uintptr) (int, error) {
	return foregroundGroup(fd)
}
