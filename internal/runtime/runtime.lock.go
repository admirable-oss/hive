package runtime

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// pidFileName is the daemon's lock and PID file inside the storage root.
const pidFileName = "hive.pid"

// instanceLock is an exclusive flock on the PID file, held for the daemon's
// whole life. It is the authority on "one daemon per root": probing the
// socket alone races when two daemons start at once, and the loser could
// delete the winner's freshly created socket.
type instanceLock struct {
	f *os.File
}

// acquireLock takes the lock without blocking and records this PID in the
// file. The kernel drops the lock if the daemon dies, so a stale PID file
// never blocks a new daemon.
func acquireLock(path string) (*instanceLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := f.Truncate(0); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &instanceLock{f: f}, nil
}

// release removes the PID file and drops the lock. Removing first means a
// new daemon never finds our PID in a file it has just locked.
func (l *instanceLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := os.Remove(l.f.Name())
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	err = errors.Join(err, l.f.Close())
	l.f = nil
	return err
}
