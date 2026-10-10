package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
)

// fakeClaude is a script named claude that draws Claude Code's screens,
// moving to the next one on each line of input: idle, working, a
// permission dialog, then idle again.
const fakeClaude = `#!/bin/sh
# Like Claude Code, the prompt box and status line sit at the bottom.
bottom='\033[999;1H'
printf "\033[H\033[2J ▐▛███▛█   Claude Code${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
printf "\033[H\033[2J ▐▛███▛█   Claude Code\r\n✻ Osmosing…${bottom}\033[2A❯ ${bottom}  esc to interrupt"
read x
printf '\033[H\033[2J Bash command\r\n Do you want to proceed?\r\n ❯ 1. Yes\r\n   2. No\r\n Esc to cancel · Tab to amend'
read x
printf "\033[H\033[2J ▐▛███▛█   Claude Code\r\n⏺ Done.${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
`

func (c *cli) agent(id string) agent.Agent {
	c.t.Helper()
	var a agent.Agent
	decode(c.t, c.must("agent", "get", id, "--json"), &a)
	return a
}

func (c *cli) waitAgent(id string, want agent.State) agent.Agent {
	c.t.Helper()
	var a agent.Agent
	c.waitFor("agent "+id+" "+string(want), func() bool {
		a = c.agent(id)
		return a.State == want
	})
	return a
}

func TestM4AgentStatesFromTheScreen(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	if err := os.WriteFile(script, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	c.must("env", "create", "dev", "--managed")
	var created client.TabCreated
	decode(t, c.must("--json", "tab", "create", "dev", "--", script), &created)
	p := created.Pane.ID

	a := c.waitAgent(p, agent.StateIdle)
	if a.Kind != "claude" || a.PaneID != p {
		t.Fatalf("agent = %+v", a)
	}
	var list agent.ListResult
	decode(t, c.must("agent", "list", "--json"), &list)
	if len(list.Agents) != 1 || list.Rollups.Session != agent.StateIdle {
		t.Fatalf("agent list = %+v", list)
	}

	c.must("pane", "send-keys", p, "Enter")
	c.waitAgent(p, agent.StateWorking)

	sent := time.Now()
	c.must("pane", "send-keys", p, "Enter")
	a = c.waitAgent(p, agent.StateBlocked)
	if d := a.Since.Sub(sent); d > 500*time.Millisecond {
		t.Errorf("blocked noticed %s after the dialog was drawn, want under 500ms", d)
	}
	var ex agent.Explanation
	decode(t, c.must("agent", "explain", p, "--json"), &ex)
	if ex.Manifest != "claude" || ex.Verdict.State != agent.StateBlocked || !strings.Contains(strings.Join(ex.Screen, "\n"), "Do you want to proceed?") {
		t.Fatalf("explanation = %+v", ex)
	}

	c.must("pane", "send-keys", p, "Enter")
	a = c.waitAgent(p, agent.StateDone)
	if a.CompletionSeq != 1 {
		t.Fatalf("completion seq = %d", a.CompletionSeq)
	}
	c.must("pane", "focus", p)
	c.waitAgent(p, agent.StateIdle)

	// A report beats the screen.
	c.must("pane", "report-agent", p, "--state", "blocked", "--message", "test", "--session-id", "s-1")
	a = c.agent(p)
	if a.State != agent.StateBlocked || a.Source != agent.SourceReport || a.SessionID != "s-1" {
		t.Fatalf("after report: %+v", a)
	}

	// Outside a pane, a hook report does nothing and succeeds.
	if _, errOut, code := c.run("pane", "report-agent", "--hook", "--state", "working"); code != 0 || errOut != "" {
		t.Fatalf("hook report outside a pane: exit %d, %q", code, errOut)
	}

	var reload agent.ReloadResult
	decode(t, c.must("--json", "server", "reload-agent-manifests"), &reload)
	if len(reload.Manifests) < 8 {
		t.Fatalf("reload = %+v", reload)
	}
	raw, _ := json.Marshal(reload)
	t.Logf("reloaded: %s", raw)
}
