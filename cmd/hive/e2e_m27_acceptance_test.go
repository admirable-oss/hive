//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/access"
	"github.com/admirable-oss/hive/internal/task"
)

const m27Plan = `name = "team"

[defaults]
kind = "claude"
environment = "dev"

[[task]]
id = "ship"
approve = true
prompt = "ship it"
`

// TestM27TeamRuntime is M27's acceptance (RFC 0007): two people work in
// one runtime, one as operator. The operator prompts an agent and approves
// a task, and is refused starting anything and typing into a shell. Both
// type into one pane at once, and only the one holding it gets through.
// Every action of the operator's is audited. Revoking her ends her live
// connection within a second, and the next one is refused.
func TestM27TeamRuntime(t *testing.T) {
	r := newTeamRuntime(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = append(r.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r.must("env", "create", "dev", "--managed")
	agentPane := strings.TrimSpace(r.must("agent", "start", "--kind", "claude", "--env", "dev", "--name", "helper"))
	shellPane := strings.Fields(r.must("tab", "create", "dev", "--name", "sh"))[1]
	alice := r.invite("alice", 1, "operator", "dev")

	// The owner plans; the operator approves the task waiting for it.
	planFile := filepath.Join(t.TempDir(), "plan.toml")
	if err := os.WriteFile(planFile, []byte(m27Plan), 0o600); err != nil {
		t.Fatal(err)
	}
	var plan task.Plan
	decode(t, r.must("--json", "task", "run", planFile), &plan)
	state := func() task.State {
		var p task.Plan
		decode(t, r.must("--json", "task", "get", plan.ID), &p)
		return p.Task("ship").State
	}
	r.waitFor("the task to wait for approval", func() bool { return state() == task.StateApproval })
	alice.must(asMember("task", "approve", plan.ID, "ship")...)
	r.waitFor("the approved task to go on", func() bool { return state() != task.StateApproval })

	// She prompts the agent, and starts nothing and types into no shell.
	alice.must(asMember("agent", "prompt", agentPane, "write the tests")...)
	alice.must(asMember("pane", "release", agentPane)...)
	for method, args := range map[string][]string{
		"agent.start":    {"agent", "start", "--kind", "claude", "--env", "dev"},
		"process.start":  {"ps", "start", "dev", "--", "true"},
		"pane.split":     {"pane", "split", agentPane},
		"pane.send_text": {"pane", "send-text", shellPane, "echo hi"},
	} {
		if _, errOut, code := alice.run(asMember(args...)...); code == 0 || !strings.Contains(errOut, "may not call "+method) {
			t.Fatalf("alice ran %s: %d %s", method, code, errOut)
		}
	}

	// Both type into the agent's pane at once: what each types either gets
	// through whole, or is refused because the other holds the pane.
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		passed  int
		refused int
	)
	for i := range 4 {
		for _, who := range []struct {
			c    *cli
			args []string
		}{
			{r.cli, []string{"pane", "send-text", agentPane, "owner's words"}},
			{alice, asMember("pane", "send-text", agentPane, "alice's words")},
		} {
			wg.Go(func() {
				_, errOut, code := who.c.run(who.args...)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case code == 0:
					passed++
				case strings.Contains(errOut, "is typing"):
					refused++
				default:
					t.Errorf("round %d: typing failed for another reason: %d %s", i, code, errOut)
				}
			})
		}
		wg.Wait()
	}
	if passed == 0 || refused == 0 {
		t.Fatalf("%d got through and %d were refused: one holds the pane, the other waits", passed, refused)
	}

	// Every one of her actions is in the audit log.
	var entries []access.AuditEntry
	decode(t, r.must("--json", "access", "audit", "--member", "alice"), &entries)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action+" "+e.Method+" "+e.Outcome] = true
	}
	for _, want := range []string{
		"action task.approve ok", "action agent.prompt ok", "action pane.release ok",
		"denied agent.start permission_denied", "denied process.start permission_denied",
		"denied pane.split permission_denied", "denied pane.send_text permission_denied",
	} {
		if !seen[want] {
			t.Errorf("alice's audit has no %q: %v", want, seen)
		}
	}

	// Revoking her ends her live connection within a second.
	follow := alice.start(asMember("events")...)
	r.waitFor("alice's connection", func() bool {
		return strings.Contains(r.must("access", "show", "alice"), "connections: 1")
	})
	ended := make(chan error, 1)
	go func() { ended <- follow.Wait() }()
	r.must("access", "revoke", "alice")
	revoked := time.Now()
	select {
	case <-ended:
		if d := time.Since(revoked); d > time.Second {
			t.Errorf("her connection ended %s after the revoke", d)
		}
	case <-time.After(10 * time.Second):
		_ = follow.Process.Kill()
		t.Fatal("her connection outlived the revoke")
	}
	if _, _, code := alice.run(asMember("env", "list")...); code == 0 {
		t.Fatal("a revoked member connects again")
	}
}

// start runs hive in the background, as run does in the foreground.
func (c *cli) start(args ...string) *exec.Cmd {
	c.t.Helper()
	exe := c.exe
	if exe == "" {
		exe = os.Args[0]
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg, "HIVE_LOG=debug", "GORACE=atexit_sleep_ms=0")
	cmd.Env = append(cmd.Env, c.env...)
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}
