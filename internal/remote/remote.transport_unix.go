//go:build !windows

package remote

import (
	"io"
	"net"
	"os"
	"syscall"
)

// sshTransport is the connection to a remote daemon and the end ssh gets
// as its stdin and stdout: a socket pair, so deadlines work and closing
// the connection ends ssh.
func sshTransport() (net.Conn, io.ReadWriteCloser, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	local, theirs := os.NewFile(uintptr(fds[0]), "hive-proxy"), os.NewFile(uintptr(fds[1]), "ssh")
	conn, err := net.FileConn(local)
	_ = local.Close() // FileConn dups it
	if err != nil {
		_ = theirs.Close()
		return nil, nil, err
	}
	return conn, theirs, nil
}
