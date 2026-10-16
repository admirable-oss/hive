//go:build windows

package pgroup

import (
	"errors"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobs are the job objects of the groups this process started, by leader.
var jobs = struct {
	sync.Mutex
	m        map[int]windows.Handle
	graceful map[int]func()
}{m: map[int]windows.Handle{}, graceful: map[int]func(){}}

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// NewJob creates a job object whose processes all end when its last handle
// closes (or the job is terminated).
func NewJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// Track records job as the group of the process pid. graceful, when set,
// is how TerminateAfter asks the group to stop before killing it (a
// pseudo console's close sends its programs CTRL_CLOSE_EVENT).
func Track(pid int, job windows.Handle, graceful func()) {
	jobs.Lock()
	defer jobs.Unlock()
	if old, ok := jobs.m[pid]; ok && old != job {
		_ = windows.CloseHandle(old)
	}
	jobs.m[pid] = job
	if graceful != nil {
		jobs.graceful[pid] = graceful
	}
}

func started(cmd *exec.Cmd) error {
	job, err := NewJob()
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(h)
	// A process in a job that forbids nesting cannot join ours; it is then
	// stopped by its tree (taskkill) instead.
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return nil
	}
	Track(cmd.Process.Pid, job, nil)
	return nil
}

func release(pid int) {
	jobs.Lock()
	job, ok := jobs.m[pid]
	delete(jobs.m, pid)
	delete(jobs.graceful, pid)
	jobs.Unlock()
	if ok {
		_ = windows.CloseHandle(job)
	}
}

// TerminateAfter asks the group led by pid to stop and kills what is left
// after grace. It does not block. A group that is gone is not an error.
//
// Windows has no SIGTERM: a group with a pseudo console is asked by
// closing it (CTRL_CLOSE_EVENT); others are only killed, after grace.
func TerminateAfter(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	jobs.Lock()
	job, tracked := jobs.m[pid]
	graceful := jobs.graceful[pid]
	delete(jobs.graceful, pid)
	jobs.Unlock()
	if graceful != nil {
		graceful()
	}
	if !tracked && !Alive(pid) {
		return nil
	}
	time.AfterFunc(grace, func() {
		if tracked {
			_ = windows.TerminateJobObject(job, 1)
			return
		}
		_ = killTree(pid)
	})
	return nil
}

// Kill stops the group led by pid at once.
func Kill(pid int) error {
	if pid <= 0 {
		return nil
	}
	jobs.Lock()
	job, tracked := jobs.m[pid]
	jobs.Unlock()
	if tracked {
		return windows.TerminateJobObject(job, 1)
	}
	return killTree(pid)
}

// killTree ends pid and its descendants (a process Hive started elsewhere,
// or one a job refused).
func killTree(pid int) error {
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput() //nolint:noctx // bounded by taskkill itself
	if err != nil && !Alive(pid) {
		return nil
	}
	if err != nil {
		return errors.New(string(out))
	}
	return nil
}

// Alive reports whether the group led by pgid still has a live member: any
// process in its job, or the leader itself when it has no job here.
func Alive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	jobs.Lock()
	job, tracked := jobs.m[pgid]
	jobs.Unlock()
	if tracked {
		var info jobAccounting
		if windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil) == nil {
			return info.ActiveProcesses > 0
		}
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pgid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}
