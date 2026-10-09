package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// MaxSocketPath is a conservative unix socket path limit (macOS allows 104
// bytes including the terminator, Linux 108).
const MaxSocketPath = 100

// SocketPath returns where a socket named name for directory dir lives:
// dir/name when that fits a unix socket address, else a name derived from
// dir in a private per-user directory under /tmp (the way tmux keeps its
// sockets), so deep storage paths still work. The result depends only on
// dir and name, so a later process finds the same socket.
func SocketPath(dir, name string) string {
	p := filepath.Join(dir, name)
	if len(p) <= MaxSocketPath {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(shortSocketDir(), hex.EncodeToString(sum[:12])+".sock")
}

func shortSocketDir() string {
	return "/tmp/hive-" + strconv.Itoa(os.Getuid())
}

// PrepareSocketDir makes sure the directory of socket path exists. The
// shared /tmp fallback directory is created private (0700) and refused
// when anyone else could have planted it or can write into it.
func PrepareSocketDir(path string) error {
	dir := filepath.Dir(path)
	if dir != shortSocketDir() {
		return os.MkdirAll(dir, 0o700)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("platform: %s must be a directory only you can access (mode 0700)", dir)
	}
	return nil
}
