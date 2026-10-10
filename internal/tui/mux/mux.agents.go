package mux

import (
	"cmp"
	"context"
	"encoding/json"
	"image/color"
	"os"
	"slices"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/notify"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/tui/compositor"
	"github.com/admirable-oss/hive/internal/tui/keymap"
)

// loadAgents reads every terminal's agent state. A daemon without agent
// detection (older than M4) has none, which is not an error.
func loadAgents(ctx context.Context, c client.Client) map[string]agent.Agent {
	res, err := client.NewAgents(c).List(ctx, true)
	if err != nil {
		return nil
	}
	out := make(map[string]agent.Agent, len(res.Agents))
	for _, x := range res.Agents {
		out[x.ID] = x
	}
	return out
}

// agentOf returns a recognised agent's state; ok is false for processes no
// manifest recognises, which keep their process status.
func (s *snapshot) agentOf(procID string) (agent.Agent, bool) {
	x, ok := s.agents[procID]
	return x, ok && x.Kind != ""
}

// applyAgentState records an agent.state event, marks an agent done on
// screen as seen, and notifies about the others.
func (a *App) applyAgentState(ev event.Event) {
	var x agent.Agent
	if json.Unmarshal(ev.Data, &x) != nil {
		return
	}
	s := a.ws.snap
	prev, known := s.agents[x.ID]
	if s.agents == nil {
		s.agents = map[string]agent.Agent{}
	}
	s.agents[x.ID] = x
	a.dirty = true
	if known && prev.State == x.State {
		return
	}
	if a.onScreen(x.ID) {
		if x.State == agent.StateDone {
			id, c := x.ID, a.c
			a.call("mark the agent seen", func(ctx context.Context) error { return client.NewAgents(c).Seen(ctx, id) }, nil)
		}
		return // never notify about the pane you are in
	}
	if a.opts.Notify.Wants(x.Kind, string(x.State)) {
		a.notifyAgent(x)
	}
}

// onScreen reports whether a process is in the focused pane on screen.
func (a *App) onScreen(procID string) bool {
	p := a.focusedPane()
	return p != nil && p.ProcessID == procID && a.ui.overview == nil
}

// notifyAgent tells the user an agent wants them, as configured.
func (a *App) notifyAgent(x agent.Agent) {
	cfg := a.opts.Notify
	name := x.Name
	if p := a.ws.snap.process(x.ID); p != nil {
		name = a.agentName(p)
	}
	n := notify.Note{Title: name + " · " + x.EnvironmentID, Body: "is done"}
	kind := compositor.ToastSuccess
	if x.State == agent.StateBlocked {
		n.Body, kind = "needs your decision", compositor.ToastWarning
	}
	a.ui.lastNote = x.ID
	if cfg.Toast {
		a.toast(kind, n.Title+" "+n.Body+"  ("+a.keyHint(keymap.OpenNotification)+" goes there)")
	}
	mode := notify.TerminalMode(cfg.Terminal, os.Getenv)
	// The desktop's own sound replaces the bell when it notifies too.
	system := cfg.System && !notify.OverSSH(os.Getenv)
	if seq := notify.Sequence(mode, n, cfg.Sound && !system); seq != "" {
		a.out = append(a.out, seq)
	}
	if system {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			// Not a.ctx: a notification raised just before leaving still shows.
			notify.System(context.Background(), n, cfg.Sound)
		}()
	}
}

// agentGlyph is a recognised agent's mark and colour by state.
func (a *App) agentGlyph(x agent.Agent) (string, color.Color, bool) {
	switch x.State {
	case agent.StateWorking:
		return "●", a.theme.Info, true
	case agent.StateBlocked:
		return "◆", a.theme.Warning, true
	case agent.StateDone:
		return "✓", a.theme.Success, true
	case agent.StateIdle:
		return "○", a.theme.Fg, true
	}
	return "", nil, false
}

// --- going to agents ---

// nextAgent goes to the next agent that wants the user: blocked ones
// first, then done ones, each oldest first, after the one in focus.
func (a *App) nextAgent() {
	s := a.ws.snap
	if s == nil {
		return
	}
	var want []agent.Agent
	for _, x := range s.agents {
		if x.Kind != "" && (x.State == agent.StateBlocked || x.State == agent.StateDone) {
			want = append(want, x)
		}
	}
	if len(want) == 0 {
		a.toast(compositor.ToastInfo, "no agent is blocked or done")
		return
	}
	slices.SortFunc(want, func(x, y agent.Agent) int {
		if x.State != y.State {
			return cmp.Compare(x.State.Rank(), y.State.Rank())
		}
		if c := x.Since.Compare(y.Since); c != 0 {
			return c
		}
		return cmp.Compare(x.ID, y.ID)
	})
	next := want[0]
	if p := a.focusedPane(); p != nil && a.ui.overview == nil {
		if i := slices.IndexFunc(want, func(x agent.Agent) bool { return x.ID == p.ProcessID }); i >= 0 {
			next = want[(i+1)%len(want)]
		}
	}
	a.gotoProcess(next.ID)
}

// focusAgent goes to the environment's nth agent (from 1), as the sidebar
// lists them.
func (a *App) focusAgent(n int) {
	if a.ws.snap == nil {
		return
	}
	agents := a.envAgents()
	if n < 1 || n > len(agents) {
		return
	}
	a.gotoProcess(agents[n-1].ID)
}

// openNotificationTarget goes to the agent of the latest notification.
func (a *App) openNotificationTarget() {
	if a.ws.snap == nil || a.ui.lastNote == "" || a.ws.snap.process(a.ui.lastNote) == nil {
		a.toast(compositor.ToastInfo, "no notification yet")
		return
	}
	a.gotoProcess(a.ui.lastNote)
}

// counts tallies the Overview's agents: running (working, or an active
// process no manifest recognises), awaiting (blocked), complete (done),
// idle, and ended.
type counts struct{ running, awaiting, complete, idle, ended int }

func (c *counts) add(s *snapshot, p *process.Process) {
	if !p.Active() {
		c.ended++
		return
	}
	x, ok := s.agentOf(p.ID)
	if !ok {
		c.running++
		return
	}
	switch x.State {
	case agent.StateBlocked:
		c.awaiting++
	case agent.StateDone:
		c.complete++
	case agent.StateIdle:
		c.idle++
	default:
		c.running++
	}
}
