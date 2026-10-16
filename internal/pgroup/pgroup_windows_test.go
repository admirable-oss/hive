//go:build windows

package pgroup_test

import (
	"os/exec"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
)

// TestWindowsJobStopsTheWholeTree: a wrapper (cmd.exe) and the program it
// started (ping) both end when the group is terminated, as a process
// group does on Unix.
func TestWindowsJobStopsTheWholeTree(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "ping -n 60 127.0.0.1 >NUL")
	pgroup.Configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := pgroup.Started(cmd); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	var child int
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 || child == pid {
		child, _ = platform.NewestDescendant(pid)
		if time.Now().After(deadline) {
			t.Fatal("cmd never started ping")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !pgroup.Alive(pid) {
		t.Fatal("the group is not alive")
	}
	if err := pgroup.TerminateAfter(pid, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the wrapper did not end")
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		if _, err := platform.LookupProcess(child); err != nil {
			break // the child ended with its job
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child %d outlived its group", child)
		}
		time.Sleep(50 * time.Millisecond)
	}
	pgroup.Release(pid)
	if pgroup.Alive(pid) {
		t.Fatal("a released, ended group is alive")
	}
	if err := pgroup.TerminateAfter(pid, 0); err != nil {
		t.Fatalf("terminating an ended group: %v", err)
	}
}
