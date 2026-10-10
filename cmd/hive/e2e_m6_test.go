package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/persist"
)

// fakeResumable stands in for Claude Code and Codex: it shows how it was
// started and, like their hooks once `hive integration install` has run,
// reports its session ID (one per pane, so a test can check which
// conversation a restored agent resumed).
const fakeResumable = `#!/bin/sh
printf '\033[H\033[2J%s (fake) args:[%s]\r\n' "$(basename "$0")" "$*"
printf '{"session_id":"sess-%s"}' "$HIVE_PANE_ID" | HIVE_CLI_TEST_EXEC=1 "$HIVE_BIN" pane report-agent --hook --state idle
while :; do sleep 1; done
`

// withFakeAgents puts fake claude and codex first on the PATH of the
// daemon and its panes.
func (c *cli) withFakeAgents() {
	c.t.Helper()
	bin := filepath.Join(c.home, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		c.t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fakeResumable), 0o755); err != nil {
			c.t.Fatal(err)
		}
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (c *cli) panes() []pane.Pane {
	c.t.Helper()
	var ps []pane.Pane
	decode(c.t, c.must("--json", "pane", "list"), &ps)
	return ps
}

func (c *cli) tabs() []pane.Tab {
	c.t.Helper()
	var ts []pane.Tab
	decode(c.t, c.must("--json", "tab", "list"), &ts)
	return ts
}

// powerCut kills the daemon, every shim and every agent at once, leaving
// their records as they were: what a host reboot does.
func (c *cli) powerCut() {
	c.t.Helper()
	var pids []int
	if pid := c.pid(); pid > 0 {
		pids = append(pids, pid)
	}
	entries, _ := os.ReadDir(filepath.Join(c.home, "run"))
	for _, e := range entries {
		var st struct {
			ShimPID int `json:"shim_pid"`
			PID     int `json:"pid"`
		}
		data, err := os.ReadFile(filepath.Join(c.home, "run", e.Name(), "state.json"))
		if err != nil || json.Unmarshal(data, &st) != nil {
			continue
		}
		if st.PID > 0 {
			_ = syscall.Kill(-st.PID, syscall.SIGKILL) // the agent's process group
			pids = append(pids, st.PID)
		}
		if st.ShimPID > 0 {
			pids = append(pids, st.ShimPID)
		}
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	for _, pid := range pids {
		c.waitFor(fmt.Sprintf("pid %d to die", pid), func() bool { return syscall.Kill(pid, 0) != nil })
	}
}

// TestM6RebootRestoresAndResumesAgents is M6's acceptance: after a reboot
// (a power cut here), all 10 Claude and Codex panes come back where they
// were and resume their conversations, with tabs, layout, focus and a
// shell's working directory as they were.
func TestM6RebootRestoresAndResumesAgents(t *testing.T) {
	c := newCLI(t)
	c.withFakeAgents()
	if err := os.WriteFile(c.cfg, []byte("[terminal]\nshell = \"/bin/sh\"\n[session]\nrestore_stagger = \"50ms\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("env", "create", "one", "--managed")
	c.must("env", "create", "two", "--managed")

	// Two tabs of five agents each, Claude and Codex alternating, split
	// both ways.
	var agentPanes []string
	kinds := map[string]string{}
	for _, env := range []string{"one", "two"} {
		var created client.TabCreated
		decode(t, c.must("--json", "tab", "create", env, "--name", "agents", "--", "claude"), &created)
		agentPanes = append(agentPanes, created.Pane.ID)
		kinds[created.Pane.ID] = "claude"
		for i, kind := range []string{"codex", "claude", "codex", "claude"} {
			if env == "two" {
				kind = map[string]string{"codex": "claude", "claude": "codex"}[kind]
			}
			dir := []string{"right", "down"}[i%2]
			var p pane.Pane
			decode(t, c.must("--json", "pane", "split", agentPanes[len(agentPanes)-1], "--direction", dir, "--", kind), &p)
			agentPanes = append(agentPanes, p.ID)
			kinds[p.ID] = kind
		}
	}
	// A shell that moved to another directory, in a tab of its own.
	work := filepath.Join(c.home, "work dir")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	var shellTab client.TabCreated
	decode(t, c.must("--json", "tab", "create", "two", "--name", "shell"), &shellTab)
	shell := shellTab.Pane.ID
	c.must("pane", "run", shell, "cd '"+work+"'")
	// And a Claude typed into a shell: it comes back typed into the shell.
	var typed pane.Pane
	decode(t, c.must("--json", "pane", "split", shell), &typed)
	c.must("pane", "run", typed.ID, "claude --model x")
	kinds[typed.ID] = "claude"
	// Focus and the active tab are part of the session too.
	c.must("pane", "focus", agentPanes[2])

	for _, id := range append(agentPanes, typed.ID) {
		c.waitFor("agent "+id+" to report its session", func() bool {
			var a struct {
				Kind      string `json:"kind"`
				SessionID string `json:"session_id"`
			}
			decode(t, c.must("--json", "agent", "get", id), &a)
			return a.Kind == kinds[id] && a.SessionID == "sess-"+id
		})
	}
	c.waitFor("the shell to change directory", func() bool {
		var res persist.SaveResult
		decode(t, c.must("--json", "session", "save"), &res)
		data, _ := os.ReadFile(res.Path)
		return strings.Contains(string(data), `"cwd": "`+resolved(t, work)+`"`)
	})
	tabsBefore, panesBefore := c.tabs(), c.panes()
	if len(panesBefore) != 12 {
		t.Fatalf("%d panes, want 12", len(panesBefore))
	}

	c.powerCut()

	// The next command starts a daemon, which restores the session.
	before := map[string]string{}
	for _, p := range panesBefore {
		before[p.ID] = p.ProcessID
	}
	var after []pane.Pane
	c.waitFor("every pane to be restored", func() bool {
		after = c.panes()
		for _, p := range after {
			if p.Process == nil || !p.Process.Active() || p.ProcessID == before[p.ID] {
				return false
			}
		}
		return len(after) == len(panesBefore)
	})
	for _, p := range after {
		kind, isAgent := kinds[p.ID]
		args := p.Process.Args
		switch {
		case p.ID == typed.ID:
			if p.Process.Command != "/bin/sh" {
				t.Errorf("the shell running claude restarted as %s %v", p.Process.Command, args)
			}
		case isAgent && kind == "claude" && !slices.Equal(args, []string{"--resume", "sess-" + p.ID}):
			t.Errorf("claude pane %s restarted as %s %v", p.ID, p.Process.Command, args)
		case isAgent && kind == "codex" && !slices.Equal(args, []string{"resume", "sess-" + p.ID}):
			t.Errorf("codex pane %s restarted as %s %v", p.ID, p.Process.Command, args)
		case p.ID == shell && p.Process.WorkingDir != resolved(t, work):
			t.Errorf("the shell came back in %q, want %q", p.Process.WorkingDir, work)
		}
	}
	c.waitFor("claude to be typed into the restored shell", func() bool {
		return strings.Contains(c.must("pane", "read", typed.ID), "args:[--resume sess-"+typed.ID+"]")
	})
	for _, id := range agentPanes {
		want := fmt.Sprintf("args:[--resume sess-%s]", id)
		if kinds[id] == "codex" {
			want = fmt.Sprintf("args:[resume sess-%s]", id)
		}
		c.waitFor("agent "+id+" to show it resumed", func() bool { return strings.Contains(c.must("pane", "read", id), want) })
	}

	// Tabs, layout, focus and the active tab are as they were.
	tabsAfter := c.tabs()
	type view struct {
		ID, Focused, Zoomed, Layout string
		Active                      bool
	}
	views := func(ts []pane.Tab) []view {
		var out []view
		for _, t := range ts {
			l, _ := json.Marshal(t.Layout)
			out = append(out, view{t.ID, t.Focused, t.Zoomed, string(l), t.Active})
		}
		return out
	}
	if !slices.Equal(views(tabsBefore), views(tabsAfter)) {
		t.Fatalf("tabs changed:\nbefore %+v\nafter  %+v", views(tabsBefore), views(tabsAfter))
	}
	if n := strings.Count(c.daemonLog(), "restored pane"); n != 12 {
		t.Fatalf("daemon log shows %d restored panes, want 12", n)
	}

	// Restoring again finds nothing to do, and the agents keep running.
	var report persist.RestoreReport
	decode(t, c.must("--json", "session", "restore"), &report)
	if len(report.Panes) != 0 {
		t.Fatalf("a second restore did %+v", report)
	}
}

// TestM6UpdateDuringOutputLosesNoBytes swaps the hive binary and restarts
// the daemon while an agent prints as fast as it can: the agent keeps
// running and every byte it printed is in its log, in order.
func TestM6UpdateDuringOutputLosesNoBytes(t *testing.T) {
	c := newCLI(t)
	// The installed hive is a copy of this binary; `hive update --from`
	// replaces it with another copy.
	c.exe = filepath.Join(c.home, "bin", "hive")
	copyFile(t, os.Args[0], c.exe)
	release := filepath.Join(c.home, "release-hive")
	copyFile(t, os.Args[0], release)

	c.must("env", "create", "dev", "--managed")
	// About four seconds of output, in bursts of 100 lines.
	const lines = 40000
	id, pid := c.startAgent("-t", "dev", "--", "sh", "-c",
		fmt.Sprintf(`i=0; while [ $i -lt %d ]; do i=$((i+1)); echo "n=$i"; [ $((i %% 100)) -eq 0 ] && sleep 0.01; done; echo DONE; while :; do sleep 1; done`, lines))
	daemon := c.pid()
	logPath := filepath.Join(c.home, "environments", "dev", "processes", id, "stdout.log")
	c.waitFor("output to start", func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "n=500\r\n")
	})

	out := c.must("--json", "update", "--from", release)
	var res struct {
		Updated   bool     `json:"updated"`
		Path      string   `json:"path"`
		Restarted []string `json:"restarted"`
	}
	decode(t, out, &res)
	if !res.Updated || res.Path != resolved(t, c.exe) || !slices.Equal(res.Restarted, []string{"default"}) {
		t.Fatalf("update: %s", out)
	}
	if c.pid() == daemon {
		t.Fatal("the daemon was not restarted")
	}
	if data, _ := os.ReadFile(logPath); strings.Contains(string(data), "DONE") {
		t.Fatal("the agent finished before the update: the test proves nothing (slow machine?)")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the agent died in the update: %v", err)
	}
	var p struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}
	decode(t, c.must("--json", "ps", "get", id), &p)
	if p.Status != "running" || p.PID != pid {
		t.Fatalf("after the update the agent is %+v, want running with pid %d", p, pid)
	}

	// Slow under the race detector and a busy machine.
	c.waitForUpTo(2*time.Minute, "the agent to finish printing", func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "DONE")
	})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := 1
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "DONE" {
			break
		}
		n, err := strconv.Atoi(strings.TrimPrefix(line, "n="))
		if err != nil || n != want {
			t.Fatalf("log line %q where n=%d belongs: output was lost or reordered", line, want)
		}
		want++
	}
	if want != lines+1 {
		t.Fatalf("log ends at n=%d, want n=%d", want-1, lines)
	}
	c.waitSnapshot(id, "DONE") // the new daemon shows the screen
}

// TestM6CorruptLayoutComesBackFromTheSnapshot damages an environment's
// layout.json while no daemon runs: the next daemon moves it aside and
// rebuilds the tabs from the last snapshot.
func TestM6CorruptLayoutComesBackFromTheSnapshot(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	c.must("env", "create", "dev", "--managed")
	var created client.TabCreated
	decode(t, c.must("--json", "tab", "create", "dev", "--name", "work"), &created)
	c.must("pane", "split", created.Pane.ID)
	c.must("session", "save")
	before := c.tabs()
	c.must("daemon", "stop", "--keep-agents")

	layoutPath := filepath.Join(c.home, "environments", "dev", "layout.json")
	if err := os.WriteFile(layoutPath, []byte(`{"schema": 1, "tabs": [{"id": `), 0o600); err != nil {
		t.Fatal(err)
	}
	after := c.tabs()
	if len(after) != 1 || after[0].ID != before[0].ID || len(c.panes()) != 2 {
		t.Fatalf("tabs after recovery: %+v (before %+v)", after, before)
	}
	if backups, _ := filepath.Glob(layoutPath + ".corrupt-*"); len(backups) != 1 {
		t.Fatalf("the damaged file was not kept: %v", backups)
	}
	// gc keeps the damaged copy until it is as old as the retention says.
	var report persist.GCReport
	decode(t, c.must("--json", "gc", "--dry-run"), &report)
	if len(report.Files) != 0 {
		t.Fatalf("gc would remove %v", report.Files)
	}
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
