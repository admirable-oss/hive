package platform_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestPeerUIDIsOurs(t *testing.T) {
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := net.Dial("unix", filepath.Join(dir, "s"))
		if err == nil {
			defer c.Close()
			_, _ = c.Read(make([]byte, 1))
		}
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	uid, err := platform.PeerUID(conn)
	if err != nil || uid != os.Getuid() {
		t.Fatalf("peer uid = %d, %v (we are %d)", uid, err, os.Getuid())
	}
	if err := platform.CheckPeer(conn); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := platform.PeerUID(a); err == nil {
		t.Fatal("a pipe has no peer credentials")
	}
	if err := platform.CheckPeer(a); err != nil {
		t.Fatalf("a pipe is not checked: %v", err)
	}
}

func TestOpenFDs(t *testing.T) {
	n0, err := platform.OpenFDs()
	if err != nil || n0 < 3 {
		t.Fatalf("open fds = %d, %v", n0, err)
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	fd := f.Fd()
	n1, err := platform.OpenFDs()
	// Keep f live until after the descriptor count. The compiler may otherwise
	// make it unreachable during OpenFDs, allowing its finalizer to close it.
	runtime.KeepAlive(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n1 != n0+1 {
		t.Fatalf("one more file: %d → %d (new file descriptor %d)", n0, n1, fd)
	}
}
