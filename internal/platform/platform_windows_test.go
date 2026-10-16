//go:build windows

package platform_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestWindowsLockIsExclusiveAndKeepsTheFileReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.pid")
	a, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if ok, err := platform.TryLock(a); !ok || err != nil {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, err := a.WriteString("1234\n"); err != nil {
		t.Fatal(err)
	}
	b, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if ok, err := platform.TryLock(b); ok || err != nil {
		t.Fatalf("second lock: %v %v (want refused)", ok, err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(data)) != "1234" {
		t.Fatalf("a locked PID file must stay readable: %q %v", data, err)
	}
	if err := platform.Unlock(a); err != nil {
		t.Fatal(err)
	}
	if ok, err := platform.TryLock(b); !ok || err != nil {
		t.Fatalf("after unlock: %v %v", ok, err)
	}
}

func TestWindowsPeerCheckAcceptsThisUser(t *testing.T) {
	sock := platform.SocketPath(t.TempDir(), "t.sock")
	if err := platform.PrepareSocketDir(sock); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("AF_UNIX listen (Windows 10 1803+): %v", err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		if pid, err := platform.PeerPID(c); err == nil && pid != os.Getpid() {
			done <- err
			return
		}
		done <- platform.CheckPeer(c)
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := <-done; err != nil {
		t.Fatalf("own connection refused: %v", err)
	}
}

func TestWindowsProcessInfo(t *testing.T) {
	info, err := platform.LookupProcess(os.Getpid())
	if err != nil || info.StartTime.IsZero() || info.StartTime.After(time.Now()) {
		t.Fatalf("lookup self: %+v %v", info, err)
	}
	args, err := platform.ProcessArgs(os.Getpid())
	if err != nil || len(args) == 0 || !strings.Contains(strings.ToLower(args[0]), ".test") {
		t.Fatalf("own args: %q %v", args, err)
	}
	if boot, err := platform.BootTime(); err != nil || !boot.Before(info.StartTime) {
		t.Fatalf("boot %v (process started %v): %v", boot, info.StartTime, err)
	}
	if _, err := platform.LookupProcess(999999); err == nil {
		t.Fatal("a process that does not exist was found")
	}
}

func TestWindowsNewestDescendant(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		pid, err := platform.NewestDescendant(cmd.Process.Pid)
		if err == nil && pid != cmd.Process.Pid {
			args, _ := platform.ProcessArgs(pid)
			if len(args) > 0 && strings.Contains(strings.ToLower(args[0]), "ping") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping never showed as cmd's newest descendant (last %d, %v)", pid, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
