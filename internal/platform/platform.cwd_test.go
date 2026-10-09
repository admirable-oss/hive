package platform_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
)

func supported(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no ProcessCwd on " + runtime.GOOS)
	}
}

// resolved resolves symlinks (macOS's /tmp is /private/tmp), as the kernel does.
func resolved(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProcessCwd(t *testing.T) {
	supported(t)
	wd, _ := os.Getwd()
	if got, err := platform.ProcessCwd(os.Getpid()); err != nil || got != resolved(t, wd) {
		t.Fatalf("own working directory = %q, %v; want %q", got, err, resolved(t, wd))
	}

	// A shell that moves after it started, into a deep directory: the
	// answer follows `cd`, and long paths come back whole.
	start := t.TempDir()
	deep := start
	for len(deep) < 600 {
		deep = filepath.Join(deep, "a-rather-long-directory-name")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `read _; cd "$1" && echo moved && read _`, "sh", deep)
	cmd.Dir = start
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })

	if got, err := platform.ProcessCwd(cmd.Process.Pid); err != nil || got != resolved(t, start) {
		t.Fatalf("child's working directory = %q, %v; want %q", got, err, resolved(t, start))
	}
	_, _ = stdin.Write([]byte("\n"))
	buf := make([]byte, 6)
	if _, err := stdout.Read(buf); err != nil || !strings.HasPrefix(string(buf), "moved") {
		t.Fatalf("the shell did not move: %q %v", buf, err)
	}
	if got, err := platform.ProcessCwd(cmd.Process.Pid); err != nil || got != resolved(t, deep) {
		t.Fatalf("after cd: %q, %v; want %q (%d bytes)", got, err, resolved(t, deep), len(deep))
	}
}

func TestProcessCwdOfAGoneProcess(t *testing.T) {
	supported(t)
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := platform.ProcessCwd(cmd.Process.Pid)
		if errors.Is(err, platform.ErrNoProcess) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("an exited (reaped) process: %v, want ErrNoProcess", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessCwdRejectsBadPIDs(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := platform.ProcessCwd(pid); !errors.Is(err, platform.ErrNoProcess) {
			t.Errorf("pid %d: %v", pid, err)
		}
	}
}
