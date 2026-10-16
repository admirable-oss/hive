//go:build darwin

package platform

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

func lookupProcess(pid int) (ProcessInfo, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// x/sys reports the kernel's empty answer for an unknown PID as EIO.
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) {
			return ProcessInfo{}, ErrNoProcess
		}
		return ProcessInfo{}, err
	}
	// The kernel answers an unknown PID with an empty record.
	if int(kp.Proc.P_pid) != pid {
		return ProcessInfo{}, ErrNoProcess
	}
	tv := kp.Proc.P_starttime
	return ProcessInfo{
		PID:       pid,
		PGID:      int(kp.Eproc.Pgid),
		StartTime: time.Unix(tv.Sec, int64(tv.Usec)*int64(time.Microsecond)),
	}, nil
}

func bootTime() (time.Time, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*int64(time.Microsecond)), nil
}

// NewestDescendant is Windows' stand-in for a terminal's foreground group;
// Unix asks the terminal (ForegroundGroup).
func NewestDescendant(int) (int, error) { return 0, ErrUnsupported }
