package platform

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
)

// RestrictSocket makes a socket file reachable only by this user. On Unix
// that is mode 0600; on Windows a socket's file lives in the user's
// profile, whose ACL already keeps other users out, and CheckPeer checks
// every caller.
func RestrictSocket(path string) error {
	err := restrictSocket(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// CheckPrivateDir returns an error unless dir is a real directory (not a
// link) that only this user can reach: owned by this user with mode 0700
// on Unix; on Windows, inside a profile whose ACL is the user's.
func CheckPrivateDir(dir string) error { return checkPrivateDir(dir) }

// TryLock takes an exclusive advisory lock on f without waiting; ok is
// false when another process holds it. The lock goes with the process:
// it is released when the process ends, however it ends.
func TryLock(f *os.File) (ok bool, err error) { return tryLock(f) }

// Lock takes an exclusive advisory lock on f, waiting for it.
func Lock(f *os.File) error { return lock(f) }

// Unlock releases a lock taken with Lock or TryLock.
func Unlock(f *os.File) error { return unlock(f) }

// Detach sets cmd up to outlive this process and its terminal or console:
// a new session on Unix; on Windows a process with no console, in its own
// process group, and out of this process's job.
func Detach(cmd *exec.Cmd) { detach(cmd) }

// StartDetached starts a command set up with Detach and returns the one
// that runs: on Windows a job that forbids breakaway (some terminals run
// their shells in one) refuses the first attempt, and a copy of cmd then
// starts inside that job.
func StartDetached(cmd *exec.Cmd) (*exec.Cmd, error) { return startDetached(cmd) }

// OpenLog opens path for appending, creating it private (0600) when it is
// new. On Windows it is opened so that it can be renamed or deleted while
// open, as on Unix: a running agent's log directory can then be moved, and
// a rotated log renamed.
func OpenLog(path string) (*os.File, error) { return openLog(path) }

// NotListening reports whether a dial error means no one listens at a
// socket path (absent, or left by a process that died): not running,
// rather than broken.
func NotListening(err error) bool { return notListening(err) }
