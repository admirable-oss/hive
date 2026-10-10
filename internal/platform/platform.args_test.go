package platform_test

import (
	"errors"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestProcessArgs(t *testing.T) {
	supported(t)
	cmd := exec.Command("/bin/sh", "-c", "read _", "name with spaces", "two")
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	args, err := platform.ProcessArgs(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/sh", "-c", "read _", "name with spaces", "two"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	if _, err := platform.ProcessArgs(0); !errors.Is(err, platform.ErrNoProcess) {
		t.Fatalf("pid 0: %v", err)
	}
}

func TestForegroundGroupFollowsTheJob(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no ForegroundGroup here")
	}
	sh := exec.Command("/bin/sh", "-i")
	sh.Env = []string{"PS1=$ ", "PATH=/bin:/usr/bin"}
	ptmx, err := pty.Start(sh)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = sh.Process.Kill(); _ = sh.Wait() })
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()

	fg := func() int {
		raw, err := ptmx.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var pgid int
		var ferr error
		_ = raw.Control(func(fd uintptr) { pgid, ferr = platform.ForegroundGroup(fd) })
		if ferr != nil {
			t.Fatal(ferr)
		}
		return pgid
	}
	waitFor := func(what string, cond func() bool) {
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out: %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	shPgid, _ := syscall.Getpgid(sh.Process.Pid)
	waitFor("the shell in the foreground", func() bool { return fg() == shPgid })

	_, _ = ptmx.Write([]byte("sleep 30\r"))
	waitFor("sleep in the foreground", func() bool {
		pg := fg()
		if pg == shPgid {
			return false
		}
		args, err := platform.ProcessArgs(pg)
		return err == nil && len(args) > 0 && args[0] == "sleep"
	})
}
