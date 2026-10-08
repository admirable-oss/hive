package platform_test

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/platform"
)

func TestLookupProcessSelf(t *testing.T) {
	info, err := platform.LookupProcess(os.Getpid())
	if errors.Is(err, platform.ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if want := syscall.Getpgrp(); info.PGID != want {
		t.Fatalf("pgid = %d, want %d", info.PGID, want)
	}
	// The test binary started moments ago; allow for coarse kernel clocks.
	if age := time.Since(info.StartTime); age < -2*time.Second || age > 10*time.Minute {
		t.Fatalf("start time %s is implausible (age %s)", info.StartTime, age)
	}
}

func TestLookupProcessChildStartTime(t *testing.T) {
	before := time.Now()
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	info, err := platform.LookupProcess(cmd.Process.Pid)
	if errors.Is(err, platform.ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.PGID != cmd.Process.Pid {
		t.Fatalf("child should lead its own group: pgid %d, pid %d", info.PGID, cmd.Process.Pid)
	}
	if d := info.StartTime.Sub(before); d < -2*time.Second || d > 5*time.Second {
		t.Fatalf("child start time off by %s", d)
	}
}

func TestLookupProcessMissing(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := platform.LookupProcess(pid); !errors.Is(err, platform.ErrNoProcess) {
			t.Errorf("LookupProcess(%d) = %v, want ErrNoProcess", pid, err)
		}
	}
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// A reaped child's PID is free; it is extremely unlikely to be reused
	// within the few microseconds before this lookup.
	if _, err := platform.LookupProcess(cmd.Process.Pid); err != nil && !errors.Is(err, platform.ErrNoProcess) && !errors.Is(err, platform.ErrUnsupported) {
		t.Fatalf("lookup of exited pid: %v", err)
	}
}
