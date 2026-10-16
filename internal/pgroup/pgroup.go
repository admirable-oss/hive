// Package pgroup stops whole process trees.
//
// Agents are usually launched through wrappers (sh -c, npx, a CLI that forks
// workers). Killing only the wrapper would orphan the real agent, so Hive
// starts every agent as the leader of its own group and stops the group.
// On Unix that is a process group, signalled as a whole. Windows has no
// process groups: there each started process is put in a job object, which
// is terminated as a whole, and which ends with its last handle, so a
// stopped or crashed owner leaves no orphans behind.
package pgroup

import (
	"os/exec"
	"time"
)

// Grace is how long Terminate waits after asking the group to stop before
// killing what is left.
const Grace = 3 * time.Second

// Terminate is TerminateAfter with the default Grace.
func Terminate(pid int) error { return TerminateAfter(pid, Grace) }

// Configure sets cmd up to lead its own group. Call it before cmd.Start,
// and Started after.
func Configure(cmd *exec.Cmd) { configure(cmd) }

// Started records the group of a command set up with Configure, once it
// has started. On Windows it creates the job the process and its
// descendants run in.
func Started(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return started(cmd)
}

// Release forgets the group led by pid once its leader has been waited
// for. On Windows this closes its job, ending any descendants it left.
func Release(pid int) { release(pid) }
