package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	goruntime "runtime"
	"strconv"

	"github.com/admirable-oss/hive/internal/platform"
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
	ok, err := platform.TryLock(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !ok {
		_ = f.Close()
		return nil, ErrAlreadyRunning
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
// new daemon never finds our PID in a file it has just locked. Windows
// cannot remove an open file: there the file is closed (unlocking it)
// first, and removed when no new daemon has claimed it meanwhile.
func (l *instanceLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	name := l.f.Name()
	var err error
	if goruntime.GOOS == "windows" {
		err = l.f.Close()
		if rerr := os.Remove(name); rerr != nil && !errors.Is(rerr, os.ErrNotExist) && !errors.Is(rerr, fs.ErrPermission) {
			err = errors.Join(err, rerr)
		}
	} else {
		err = os.Remove(name)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		err = errors.Join(err, l.f.Close())
	}
	l.f = nil
	return err
}
