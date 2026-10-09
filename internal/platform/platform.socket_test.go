package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestSocketPath(t *testing.T) {
	short := "/tmp/x"
	if got := platform.SocketPath(short, "a.sock"); got != "/tmp/x/a.sock" {
		t.Fatalf("short path moved: %q", got)
	}
	long := "/" + strings.Repeat("d", 120)
	got := platform.SocketPath(long, "a.sock")
	if len(got) > platform.MaxSocketPath || !strings.HasPrefix(got, "/tmp/hive-") {
		t.Fatalf("long path fallback = %q", got)
	}
	if again := platform.SocketPath(long, "a.sock"); again != got {
		t.Fatal("the fallback must be stable")
	}
	if other := platform.SocketPath(long+"x", "a.sock"); other == got {
		t.Fatal("different directories must not share a socket")
	}
	if err := platform.PrepareSocketDir(got); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Dir(got))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("fallback directory: %v %v", fi.Mode(), err)
	}
}
