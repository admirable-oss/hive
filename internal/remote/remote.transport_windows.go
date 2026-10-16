//go:build windows

package remote

import (
	"io"
	"net"
)

// sshTransport is the connection to a remote daemon and the end ssh gets
// as its stdin and stdout. Windows has no socket pairs: an in-memory pipe
// stands in, which os/exec copies to and from ssh's pipes. Deadlines work
// on it, and closing it ends ssh's input, so ssh exits.
func sshTransport() (net.Conn, io.ReadWriteCloser, error) {
	local, theirs := net.Pipe()
	return local, nopCloser{theirs}, nil
}

// nopCloser keeps Dial's deferred close from cutting the pipe that os/exec
// is still copying; the local end's close ends it.
type nopCloser struct{ net.Conn }

func (nopCloser) Close() error { return nil }
