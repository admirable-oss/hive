package mux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/pane"
)

// fakeClaude draws Claude Code's screens, one per line of input: idle,
// working, a permission dialog, idle.
const fakeClaude = `#!/bin/sh
bottom='\033[999;1H'
printf "\033[H\033[2J Claude Code${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
printf "\033[H\033[2J Claude Code\r\n✻ Osmosing…${bottom}\033[2A❯ ${bottom}  esc to interrupt"
read x
printf '\033[H\033[2J Bash command\r\n Do you want to proceed?\r\n ❯ 1. Yes\r\n Esc to cancel · Tab to amend'
read x
printf "\033[H\033[2J Claude Code\r\n⏺ Done.${bottom}\033[2A❯ ${bottom}  ? for shortcuts"
read x
`

func (h *harness) agentState(procID string) agent.State {
	if x, ok := h.a.ws.snap.agentOf(procID); ok {
		return x.State
	}
	return ""
}

func TestAgentStatesShowAndNotify(t *testing.T) {
	h := newHarness(t, 100, 20, Options{})
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	home := h.focused()
	no := false
	p, err := h.api.PaneSplit(ctx, pane.SplitRequest{Pane: home, Spec: pane.Spec{Name: "claude", Command: []string{script}}, Focus: &no})
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("claude idle", func() bool { return h.agentState(p.ProcessID) == agent.StateIdle })
	enter := func() {
		if err := h.api.PaneSendKeys(ctx, p.ID, []string{"Enter"}); err != nil {
			t.Fatal(err)
		}
	}

	enter()
	h.waitFor("claude working", func() bool { return h.agentState(p.ProcessID) == agent.StateWorking })
	enter()
	h.waitFor("claude blocked", func() bool { return h.agentState(p.ProcessID) == agent.StateBlocked })
	h.waitText("needs your decision") // a toast: it is not the pane in focus
	if !strings.Contains(h.screen(), "◆ claude") {
		t.Fatalf("the sidebar does not mark claude blocked:\n%s", h.screen())
	}
	if h.a.ui.lastNote != p.ProcessID {
		t.Fatalf("last notification = %q, want %q", h.a.ui.lastNote, p.ProcessID)
	}

	h.press("ctrl+b", "a") // next agent: the blocked one
	h.waitFor("claude's pane in focus", func() bool { return h.focused() == p.ID })

	// Done in the pane in focus: seen at once, and no notification.
	h.a.ui.lastNote = ""
	enter()
	h.waitFor("claude idle again", func() bool { return h.agentState(p.ProcessID) == agent.StateIdle })
	if h.a.ui.lastNote != "" {
		t.Fatal("notified about the pane in focus")
	}

	h.press("ctrl+b", "alt+1") // the first agent in the sidebar: the shell
	h.waitFor("the first pane in focus", func() bool { return h.focused() == home })
}
