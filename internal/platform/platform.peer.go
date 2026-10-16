//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// PeerUID returns the user ID of the process at the other end of a unix
// socket connection, as the kernel vouches for it (SO_PEERCRED on Linux,
// LOCAL_PEERCRED on macOS).
func PeerUID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("platform: not a unix socket (%T)", conn)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	uid := -1
	var opErr error
	if err := raw.Control(func(fd uintptr) { uid, opErr = peerUID(int(fd)) }); err != nil {
		return 0, err
	}
	return uid, opErr
}

// CheckPeer accepts a connection only from this process's own user. The
// socket's 0600 mode already keeps others out; this holds even when a
// socket file is made reachable some other way (a shared directory, a
// permissive umask on a copied path).
func CheckPeer(conn net.Conn) error {
	if _, ok := conn.(*net.UnixConn); !ok {
		return nil // not a socket another user could reach (a test's pipe)
	}
	uid, err := PeerUID(conn)
	if errors.Is(err, ErrUnsupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the peer's credentials: %w", err)
	}
	if uid != os.Getuid() {
		return fmt.Errorf("%w (uid %d)", ErrForeignPeer, uid)
	}
	return nil
}
