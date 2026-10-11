package mux

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"slices"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/tui/compositor"
)

// panelMachines is the sidebar panel listing other machines.
const panelMachines = "machines"

// machinePoll is how often other machines' agents are read again.
const machinePoll = 2 * time.Second

// machineCallTimeout allows for the first SSH connection to a machine.
const machineCallTimeout = 30 * time.Second

// machinesState is what the UI knows about the machines it is not showing.
type machinesState struct {
	name   string    // the machine shown ("" is this one)
	others []Machine // the rest, in the order they were configured
	agents map[string][]agent.Agent
	errs   map[string]string // machines that could not be read
	cancel context.CancelFunc
	focus  string // an agent to show once a switched-to workspace loads
	here   string // this machine's name, for the sidebar
}

func newMachinesState(o Options) machinesState {
	here, _ := os.Hostname()
	here, _, _ = strings.Cut(here, ".")
	return machinesState{
		name: o.Machine, others: slices.Clone(o.Machines),
		agents: map[string][]agent.Agent{}, errs: map[string]string{},
		here: cmpOr(here, "this machine"),
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// label names a machine for the user.
func (m *machinesState) label(name string) string {
	if name == "" {
		return m.here
	}
	return name
}

// machineAgentsMsg is one machine's agents, read by its watcher.
type machineAgentsMsg struct {
	gen     int
	machine string
	agents  []agent.Agent
	err     error
}

// watchMachines (re)starts reading every other machine's agents.
func (a *App) watchMachines() {
	if a.mach.cancel != nil {
		a.mach.cancel()
		a.mach.cancel = nil
	}
	if len(a.mach.others) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.mach.cancel = cancel
	gen := a.gen
	for _, m := range a.mach.others {
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			t := time.NewTicker(machinePoll)
			defer t.Stop()
			for {
				cctx, ccancel := context.WithTimeout(ctx, machineCallTimeout)
				res, err := client.NewAgents(m.Client).List(cctx, false)
				ccancel()
				if ctx.Err() != nil {
					return
				}
				msg := machineAgentsMsg{gen: gen, machine: m.Name, err: err}
				for _, x := range res.Agents {
					if x.State != agent.StateExited {
						msg.agents = append(msg.agents, x)
					}
				}
				a.post(msg)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
}

func (a *App) machineAgents(m machineAgentsMsg) {
	if m.gen != a.gen {
		return
	}
	if m.err != nil {
		a.mach.errs[m.machine] = errText(m.err)
		delete(a.mach.agents, m.machine)
		return
	}
	delete(a.mach.errs, m.machine)
	a.mach.agents[m.machine] = m.agents
}

// machineRows are the sidebar's Machines panel: the one shown, then each
// other machine with its agents.
func (a *App) machineRows(header func(id, title string, n int) compositor.SidebarRow) []compositor.SidebarRow {
	if len(a.mach.others) == 0 {
		return nil
	}
	muted := uv.Style{Fg: a.theme.Muted}
	rows := []compositor.SidebarRow{{}, header(panelMachines, "Machines", len(a.mach.others)+1)}
	if a.ui.collapsed[panelMachines] {
		return rows
	}
	rows = append(rows, compositor.SidebarRow{
		ID: machineRowID(a.mach.name, ""), Kind: compositor.HitMachine, Selected: true,
		Spans: []compositor.Span{{Text: "▣ ", Style: uv.Style{Fg: a.theme.Accent}}, {Text: a.mach.label(a.mach.name)}},
	})
	for _, m := range a.mach.others {
		glyph, c := a.machineGlyph(m.Name)
		spans := []compositor.Span{{Text: glyph + " ", Style: uv.Style{Fg: c}}, {Text: a.mach.label(m.Name)}}
		if _, down := a.mach.errs[m.Name]; down {
			spans = append(spans, compositor.Span{Text: " unreachable", Style: muted})
		} else {
			spans = append(spans, compositor.Span{Text: fmt.Sprintf(" %d", len(a.mach.agents[m.Name])), Style: muted})
		}
		rows = append(rows, compositor.SidebarRow{ID: machineRowID(m.Name, ""), Kind: compositor.HitMachine, Spans: spans})
		for _, x := range a.mach.agents[m.Name] {
			g, c, ok := a.agentGlyph(x)
			if !ok {
				g, c = "·", a.theme.Muted
			}
			rows = append(rows, compositor.SidebarRow{
				ID: machineRowID(m.Name, x.ID), Kind: compositor.HitMachine,
				Spans: []compositor.Span{{Text: "  " + g + " ", Style: uv.Style{Fg: c}}, {Text: x.Name}},
			})
		}
	}
	return rows
}

// machineGlyph marks a machine by its most urgent agent, or as unreachable.
func (a *App) machineGlyph(name string) (string, color.Color) {
	if _, down := a.mach.errs[name]; down {
		return "✗", a.theme.Error
	}
	var states []agent.State
	for _, x := range a.mach.agents[name] {
		states = append(states, x.State)
	}
	if g, c, ok := a.agentGlyph(agent.Agent{Status: agent.Status{State: agent.Rollup(states...)}}); ok {
		return g, c
	}
	return "○", a.theme.Muted
}

func machineRowID(machine, agentID string) string {
	id := "machine:" + machine
	if agentID != "" {
		id += "\x00" + agentID
	}
	return id
}

// pickMachine shows the machine a sidebar row names, at its agent when the
// row is one.
func (a *App) pickMachine(id string) {
	name, agentID, _ := strings.Cut(strings.TrimPrefix(id, "machine:"), "\x00")
	if name == a.mach.name {
		if agentID != "" {
			a.gotoProcess(agentID)
		}
		return
	}
	a.switchMachine(name, agentID)
}

// switchMachine shows another machine's workspace: the UI drops this
// machine's streams and state and reads that one's, as if it had been
// started there. This machine joins the others in the sidebar.
func (a *App) switchMachine(name, agentID string) {
	i := slices.IndexFunc(a.mach.others, func(m Machine) bool { return m.Name == name })
	if i < 0 {
		return
	}
	next := a.mach.others[i]
	a.mach.others[i] = Machine{Name: a.mach.name, Client: a.c}
	a.mach.name, a.mach.focus = next.Name, agentID
	delete(a.mach.agents, next.Name)
	delete(a.mach.errs, next.Name)

	a.gen++
	a.connCancel()
	a.connCtx, a.connCancel = context.WithCancel(a.ctx)
	if a.ui.copy != nil {
		a.exitCopy()
	}
	for id, v := range a.views {
		v.close()
		delete(a.views, id)
	}
	a.cache = map[string]*screenCache{}
	a.ui.overview, a.ui.overlay = nil, nil
	a.c, a.api = next.Client, client.NewWorkspace(next.Client)
	a.ws = workspaceState{
		claims: map[string]size{}, claiming: map[string]bool{},
		activity: map[string]bool{}, finals: map[string]bool{},
	}
	a.opts.Env = ""
	a.subscribe()
	a.refresh()
	a.watchMachines()
	a.toast(compositor.ToastInfo, "showing "+a.mach.label(next.Name))
	a.dirty = true
}

// focusAfterSwitch goes to the agent picked on another machine, once its
// workspace has loaded.
func (a *App) focusAfterSwitch() {
	if id := a.mach.focus; id != "" && a.ws.snap != nil {
		a.mach.focus = ""
		a.gotoProcess(id)
	}
}
