package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/vt"
)

type snapshotJSON struct {
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Lines  []string `json:"lines"`
	Cursor struct {
		X, Y int
	} `json:"cursor"`
}

func (c *cli) snapshot(id string) snapshotJSON {
	c.t.Helper()
	var s snapshotJSON
	if err := json.Unmarshal([]byte(c.must("--json", "terminal", "snapshot", id)), &s); err != nil {
		c.t.Fatal(err)
	}
	return s
}

func (c *cli) startAgent(args ...string) (id string, pid int) {
	c.t.Helper()
	var p struct {
		ID  string `json:"id"`
		PID int    `json:"pid"`
	}
	if err := json.Unmarshal([]byte(c.must(append([]string{"--json", "ps", "start"}, args...)...)), &p); err != nil {
		c.t.Fatal(err)
	}
	return p.ID, p.PID
}

func (c *cli) waitSnapshot(id, want string) snapshotJSON {
	c.t.Helper()
	var s snapshotJSON
	c.waitFor("screen of "+id+" to show "+want, func() bool {
		s = c.snapshot(id)
		return strings.Contains(strings.Join(s.Lines, "\n"), want)
	})
	return s
}

// agentScript draws a fixed, styled screen and then keeps running.
func agentScript(name string) string {
	return fmt.Sprintf(`printf '\033[2J\033[H\033[1;35m%s\033[0m\n\033[38;5;214mstatus:\033[0m thinking\n\033[4;10Hcursor-here'; while :; do sleep 1; done`, name)
}

// TestE2E_AgentsSurviveADaemonCrash is M1's acceptance test: kill -9 the
// daemon, start it again, and every agent is still running with an
// identical screen.
func TestE2E_AgentsSurviveADaemonCrash(t *testing.T) {
	c := newCLI(t)
	c.must("env", "create", "dev")
	type agent struct {
		id     string
		pid    int
		screen snapshotJSON
	}
	var agents []agent
	for _, name := range []string{"alpha", "beta", "gamma"} {
		id, pid := c.startAgent("-t", "dev", "--", "sh", "-c", agentScript(name))
		agents = append(agents, agent{id: id, pid: pid, screen: c.waitSnapshot(id, "cursor-here")})
	}
	plainID, plainPID := c.startAgent("dev", "--", "sh", "-c", "while :; do sleep 1; done")

	daemon := c.pid()
	if err := syscall.Kill(daemon, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	c.waitFor("the daemon to die", func() bool { return syscall.Kill(daemon, 0) != nil })
	for _, a := range append(agents, agent{id: plainID, pid: plainPID}) {
		if err := syscall.Kill(a.pid, 0); err != nil {
			t.Fatalf("agent %s (pid %d) died with the daemon: %v", a.id, a.pid, err)
		}
	}

	// The next command starts a daemon, which adopts every shim.
	for _, a := range agents {
		after := c.snapshot(a.id)
		if strings.Join(after.Lines, "\n") != strings.Join(a.screen.Lines, "\n") || after.Cursor != a.screen.Cursor {
			t.Fatalf("screen of %s changed across the crash:\n%s\nwant:\n%s", a.id, strings.Join(after.Lines, "\n"), strings.Join(a.screen.Lines, "\n"))
		}
		var p struct {
			Status string `json:"status"`
			PID    int    `json:"pid"`
		}
		if err := json.Unmarshal([]byte(c.must("--json", "ps", "get", a.id)), &p); err != nil || p.Status != "running" || p.PID != a.pid {
			t.Fatalf("after recovery %s is %+v, want running with pid %d", a.id, p, a.pid)
		}
	}
	if got := c.must("ps", "get", plainID); !strings.Contains(got, "running") {
		t.Fatalf("plain agent after recovery:\n%s", got)
	}
	if n := strings.Count(c.daemonLog(), "re-attached to a process that outlived the previous daemon"); n != 4 {
		t.Fatalf("daemon log shows %d re-attachments, want 4:\n%s", n, c.daemonLog())
	}

	// History survives too: the scrolled-off lines come from the shim. Nine
	// lines and a newline on a 5-row screen leave lines 1-5 in history.
	histID, _ := c.startAgent("-t", "--height", "5", "dev", "--", "sh", "-c", `for i in 1 2 3 4 5 6 7 8 9; do echo "old line $i"; done; while :; do sleep 1; done`)
	c.waitSnapshot(histID, "old line 9")
	if out := c.must("terminal", "snapshot", "--scrollback", "3", histID); !strings.HasPrefix(out, "old line 3\nold line 4\nold line 5\nold line 6\n") {
		t.Fatalf("snapshot with scrollback:\n%s", out)
	}

	// Input still reaches an adopted agent.
	c.must("terminal", "input", agents[0].id, "x")

	// A graceful restart keeps them too.
	c.must("daemon", "restart")
	for _, a := range agents {
		if err := syscall.Kill(a.pid, 0); err != nil {
			t.Fatalf("agent %s died in a graceful restart", a.id)
		}
		c.waitSnapshot(a.id, "cursor-here")
	}

	// hive stop is the explicit "stop everything".
	c.must("stop")
	for _, a := range append(agents, agent{id: plainID, pid: plainPID}) {
		c.waitFor("agent "+a.id+" to stop", func() bool { return syscall.Kill(a.pid, 0) != nil })
	}
	if entries, _ := os.ReadDir(filepath.Join(c.home, "run")); len(entries) != 0 {
		t.Fatalf("shim directories left after stop: %d", len(entries))
	}
}

// inkLikeAgent imitates Claude Code's renderer: a bordered prompt box,
// colours, and Ink-style redraws (erase lines, move up, draw again).
const inkLikeAgent = `printf '\033[?25l'
printf '\033[38;5;174m╭──────────────────────────────╮\033[0m\n'
printf '\033[38;5;174m│\033[0m \033[1m✻ Welcome to Claude Code!\033[0m    \033[38;5;174m│\033[0m\n'
printf '\033[38;5;174m╰──────────────────────────────╯\033[0m\n'
for i in 1 2 3; do
  printf '\033[2K\033[1A\033[2K\033[G> working %s\n\033[2m(esc to interrupt)\033[0m' "$i"
  sleep 0.1
done
printf '\033[2K\033[1A\033[2K\033[G\033[32m✔ done\033[0m\n> \033[?25h'
while :; do sleep 1; done`

// TestE2E_AttachShowsTheScreenAtOnce attaches to a running Ink-style agent
// and checks the very first paint reproduces its screen exactly.
func TestE2E_AttachShowsTheScreenAtOnce(t *testing.T) {
	c := newCLI(t)
	c.must("env", "create", "dev")
	id, _ := c.startAgent("-t", "--width", "60", "--height", "12", "dev", "--", "sh", "-c", inkLikeAgent)
	want := c.waitSnapshot(id, "✔ done")

	cl := client.NewService(client.Config{SocketPath: filepath.Join(c.home, "hive.sock"), Name: "test"})
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	att, err := cl.TerminalAttach(ctx, client.ViewRequest{ProcessID: id, Width: 60, Height: 12})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()

	// Paint what arrives into an emulated "real" terminal.
	outer := vt.New(60, 12, vt.Options{})
	defer outer.Close()
	buf := make([]byte, 64<<10)
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, err := att.Read(buf)
		_, _ = outer.Write(buf[:n])
		got := outer.Snapshot()
		var lines []string
		for y := range got.Rows {
			lines = append(lines, got.LineText(y))
		}
		if strings.Join(lines, "\n") == strings.Join(want.Lines, "\n") {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("attached screen:\n%s\nwant:\n%s (err %v)", strings.Join(lines, "\n"), strings.Join(want.Lines, "\n"), err)
		}
	}
	c.must("stop")
}

var _ = io.EOF
