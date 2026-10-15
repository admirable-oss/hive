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
	// A process descriptor table can change while it is being scanned (for
	// example, macOS may briefly open or close descriptors during runtime
	// activity). Sample several times and open a small batch so one transient
	// descriptor does not make this count check flaky.
	const samples = 5
	const openedFiles = 4
	baseline := int(^uint(0) >> 1)
	for range samples {
		count, err := platform.OpenFDs()
		if err != nil {
			t.Fatal(err)
		}
		if count < baseline {
			baseline = count
		}
	}
	if baseline < 3 {
		t.Fatalf("open fds = %d, want at least 3", baseline)
	}
	files := make([]*os.File, 0, openedFiles)
	t.Cleanup(func() {
		for _, f := range files {
			_ = f.Close()
		}
	})
	for range openedFiles {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	held := int(^uint(0) >> 1)
	for range samples {
		count, err := platform.OpenFDs()
		if err != nil {
			t.Fatal(err)
		}
		if count < held {
			held = count
		}
	}
	// Keep the files live through every descriptor scan. The compiler may
	// otherwise make them unreachable during OpenFDs and close them early.
	runtime.KeepAlive(files)
	if held < baseline+openedFiles-1 {
		t.Fatalf("open files: stable count %d → %d after opening %d descriptors", baseline, held, openedFiles)
	}
}
