package pgroup_test

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/pgroup"
)

// startGroup runs script with sh as the leader of a new process group and
// reaps it in the background so the leader does not linger as a zombie.
func startGroup(t *testing.T, script string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-reaped
	})
	return pid
}

func waitGone(t *testing.T, pgid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for pgroup.Alive(pgid) {
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still alive after %s", pgid, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminateStopsTheWholeGroup(t *testing.T) {
	// The background sleep is the "real agent" behind a wrapper shell.
	pid := startGroup(t, "sleep 30 & sleep 30")
	if !pgroup.Alive(pid) {
		t.Fatal("group should be alive right after start")
	}
	if err := pgroup.TerminateAfter(pid, time.Minute); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pid, 5*time.Second)
}

func TestTerminateEscalatesToSIGKILL(t *testing.T) {
	pid := startGroup(t, `trap "" TERM; sleep 30 & wait`)
	time.Sleep(50 * time.Millisecond) // let the trap install before signalling
	if err := pgroup.TerminateAfter(pid, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pid, 5*time.Second)
}

func TestTerminateIgnoresMissingGroups(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if err := pgroup.Terminate(pid); err != nil {
			t.Errorf("Terminate(%d) = %v, want nil", pid, err)
		}
	}
	pid := startGroup(t, "exit 0")
	waitGone(t, pid, 5*time.Second)
	if err := pgroup.Terminate(pid); err != nil {
		t.Fatalf("terminating a finished group should be a no-op, got %v", err)
	}
}

func TestAliveRejectsInvalidIDs(t *testing.T) {
	if pgroup.Alive(0) || pgroup.Alive(-5) {
		t.Fatal("non-positive group IDs are never alive")
	}
}
