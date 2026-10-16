//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Sockets on Windows inherit their directory's ACL; chmod would only
// toggle the read-only attribute, which a socket file may refuse.
func restrictSocket(string) error { return nil }

func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 {
		return fmt.Errorf("platform: %s must be a plain directory", dir)
	}
	return nil
}

// lockOffset is where locks are taken: far past any content, so a locked
// file (the daemon's PID file) can still be read.
const lockOffset = 1 << 30

func lockFile(f *os.File, flags uint32) error {
	ol := windows.Overlapped{OffsetHigh: lockOffset}
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ol)
}

func tryLock(f *os.File) (bool, error) {
	err := lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func lock(f *os.File) error { return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK) }

func unlock(f *os.File) error {
	ol := windows.Overlapped{OffsetHigh: lockOffset}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}

const detachFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB

func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= detachFlags
	cmd.SysProcAttr.HideWindow = true
}

func startDetached(cmd *exec.Cmd) (*exec.Cmd, error) {
	err := cmd.Start()
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB == 0 {
		return cmd, err
	}
	// The job this process is in forbids breakaway: start inside it.
	retry := exec.Command(cmd.Path, cmd.Args[1:]...) //nolint:noctx // same as cmd, which outlives this process
	retry.Dir, retry.Env, retry.Stdin, retry.Stdout, retry.Stderr = cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr
	attr := *cmd.SysProcAttr
	attr.CreationFlags &^= windows.CREATE_BREAKAWAY_FROM_JOB
	retry.SysProcAttr = &attr
	err = retry.Start()
	return retry, err
}

func openLog(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_APPEND_DATA|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func notListening(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}
