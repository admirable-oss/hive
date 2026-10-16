//go:build windows

package platform

import (
	"errors"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID: the process ID at the
// other end of an AF_UNIX connection (Windows 10 1803 and later).
const sioAFUnixGetPeerPID = 0x58000100

// PeerUID has no meaning on Windows, where users are SIDs; CheckPeer
// compares the peer process's user with this one's instead.
func PeerUID(net.Conn) (int, error) { return -1, ErrUnsupported }

// PeerPID returns the process ID at the other end of a unix socket
// connection, as the kernel reports it.
func PeerPID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("platform: not a unix socket (%T)", conn)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid uint32
	var opErr error
	if err := raw.Control(func(fd uintptr) {
		var n uint32
		opErr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0, (*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0) //nolint:gosec // WSAIoctl writes a DWORD into pid
	}); err != nil {
		return 0, err
	}
	if opErr != nil {
		return 0, fmt.Errorf("%w: %w", ErrUnsupported, opErr)
	}
	return int(pid), nil
}

// CheckPeer accepts a connection only from a process running as this
// process's user (the same SID).
func CheckPeer(conn net.Conn) error {
	if _, ok := conn.(*net.UnixConn); !ok {
		return nil // not a socket another user could reach (a test's pipe)
	}
	pid, err := PeerPID(conn)
	if errors.Is(err, ErrUnsupported) {
		return nil // an older Windows: the socket directory's ACL still holds
	}
	if err != nil {
		return fmt.Errorf("read the peer's process: %w", err)
	}
	same, err := sameUser(uint32(pid))
	if err != nil {
		return fmt.Errorf("read the peer's user: %w", err)
	}
	if !same {
		return fmt.Errorf("%w (pid %d)", ErrForeignPeer, pid)
	}
	return nil
}

// sameUser reports whether process pid runs as this process's user.
func sameUser(pid uint32) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return false, err
	}
	defer tok.Close()
	theirs, err := tok.GetTokenUser()
	if err != nil {
		return false, err
	}
	ours, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	return windows.EqualSid(theirs.User.Sid, ours.User.Sid), nil
}
