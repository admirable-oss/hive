package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// EventMachines carries []MachineStatus.
const EventMachines = "hive:machines"

// MachineStatus is a saved machine and how the app is connected to it.
type MachineStatus struct {
	SavedMachine
	Connection Connection     `json:"connection"`
	Counts     map[string]int `json:"counts"`
}

// Fleet is the app's view of every daemon: this computer's (always), and
// each saved machine that is enabled and trusted, through `hive machine
// proxy`. It merges their workspaces into one, qualifying a machine's IDs
// with its name ("gpu/p123"; this computer's stay as they are), and sends
// each call to the daemon its ID names.
type Fleet struct {
	cfg   Config
	local *Hub

	// listMachines and dialMachine are the CLI's (tests replace them).
	listMachines func(context.Context) ([]SavedMachine, error)
	dialMachine  func(name string) func(context.Context) (net.Conn, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	kick   chan struct{}

	mu      sync.Mutex
	remotes map[string]*Hub
	saved   []SavedMachine
	ws      map[string]Workspace
	conns   map[string]Connection
}

// machineSep separates a machine's name from an ID of its daemon.
const machineSep = "/"

func qualify(machine, id string) string {
	if machine == "" || id == "" {
		return id
	}
	return machine + machineSep + id
}

// NewFleet returns a fleet for the local daemon cfg describes; Start
// connects it.
func NewFleet(cfg Config) *Fleet {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	f := &Fleet{
		cfg: cfg, kick: make(chan struct{}, 1),
		remotes: map[string]*Hub{}, ws: map[string]Workspace{}, conns: map[string]Connection{},
	}
	local := cfg
	local.Emitter = machineEmitter{f, ""}
	local.Notify = f.notifier("")
	f.local = New(local)
	f.listMachines = f.local.SavedMachines
	f.dialMachine = f.local.machineDial
	return f
}

// Start connects to this computer's daemon and to the saved machines.
func (f *Fleet) Start() {
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.local.Start()
	f.wg.Go(f.machinesLoop)
}

// Close disconnects from every daemon; agents keep running.
func (f *Fleet) Close() {
	if f.cancel == nil {
		return
	}
	f.cancel()
	f.wg.Wait()
	f.mu.Lock()
	remotes := f.remotes
	f.remotes = map[string]*Hub{}
	f.mu.Unlock()
	for _, h := range remotes {
		h.Close()
	}
	f.local.Close()
}

// Connection is the connection to this computer's daemon.
func (f *Fleet) Connection() Connection { return f.local.Connection() }

// Settings are this computer's configuration, which the app follows.
func (f *Fleet) Settings() Settings { return f.local.Settings() }

// Local is this computer's hub.
func (f *Fleet) Local() *Hub { return f.local }

// RefreshMachines reads the saved machines again now.
func (f *Fleet) RefreshMachines() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

func (f *Fleet) machinesLoop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		f.syncMachines()
		select {
		case <-f.ctx.Done():
			return
		case <-tick.C:
		case <-f.kick:
		}
	}
}

// syncMachines connects to the machines that should be, and lets go of
// the others.
func (f *Fleet) syncMachines() {
	ms, err := f.listMachines(f.ctx)
	if err != nil {
		if f.ctx.Err() == nil {
			f.cfg.Logger.Debug("cannot list saved machines", "err", err)
		}
		return
	}
	want := map[string]bool{}
	for _, m := range ms {
		if m.connectable() {
			want[m.Name] = true
		}
	}
	f.mu.Lock()
	f.saved = ms
	var stop []*Hub
	for name, h := range f.remotes {
		if !want[name] {
			stop = append(stop, h)
			delete(f.remotes, name)
			delete(f.ws, name)
			delete(f.conns, name)
		}
	}
	for name := range want {
		if _, ok := f.remotes[name]; ok {
			continue
		}
		cfg := f.cfg
		cfg.Emitter = machineEmitter{f, name}
		cfg.Notify = f.notifier(name)
		cfg.Dial = f.dialMachine(name)
		cfg.Name = f.cfg.Name
		cfg.LoginShell = f.cfg.LoginShell
		h := New(cfg)
		h.conn.Session = name
		f.remotes[name] = h
		h.Start()
	}
	f.mu.Unlock()
	for _, h := range stop {
		h.Close()
	}
	if len(stop) > 0 {
		f.emitWorkspace()
	}
	f.emitMachines()
}

// Machines are the saved machines with their connections.
func (f *Fleet) Machines() []MachineStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]MachineStatus, 0, len(f.saved))
	for _, m := range f.saved {
		st := MachineStatus{SavedMachine: m, Counts: map[string]int{}, Connection: Connection{State: "off", Session: m.Name}}
		if c, ok := f.conns[m.Name]; ok {
			st.Connection = c
		}
		if !m.connectable() {
			st.Connection.State = "off"
		}
		for k, v := range f.ws[m.Name].Counts {
			st.Counts[k] = v
		}
		out = append(out, st)
	}
	return out
}

func (f *Fleet) emit(name string, data any) {
	if f.cfg.Emitter != nil {
		f.cfg.Emitter.Emit(name, data)
	}
}

func (f *Fleet) emitMachines() { f.emit(EventMachines, f.Machines()) }

func (f *Fleet) emitWorkspace() { f.emit(EventWorkspace, f.Workspace()) }

// machineEmitter takes a hub's events, for the fleet to pass on.
type machineEmitter struct {
	f       *Fleet
	machine string
}

func (e machineEmitter) Emit(name string, data any) {
	f, m := e.f, e.machine
	switch v := data.(type) {
	case Connection:
		f.mu.Lock()
		f.conns[m] = v
		f.mu.Unlock()
		if m == "" {
			f.emit(name, v)
		}
		f.emitMachines()
	case Workspace:
		f.mu.Lock()
		_, live := f.remotes[m]
		if m == "" || live {
			f.ws[m] = v
		}
		f.mu.Unlock()
		f.emitWorkspace()
	case ScreenUpdate:
		v.Pane = qualify(m, v.Pane)
		f.emit(name, v)
	case ImageUpdate:
		v.Pane = qualify(m, v.Pane)
		f.emit(name, v)
	default:
		if m == "" {
			f.emit(name, data) // settings: this computer's
		}
	}
}

// notifier qualifies a machine's notifications.
func (f *Fleet) notifier(machine string) func(Notification) {
	return func(n Notification) {
		if f.cfg.Notify == nil {
			return
		}
		if machine != "" {
			n.ID = qualify(machine, n.ID)
			n.AgentID, n.PaneID = qualify(machine, n.AgentID), qualify(machine, n.PaneID)
			n.Title = machine + " · " + n.Title
		}
		f.cfg.Notify(n)
	}
}

// Workspace is every daemon's workspace in one, this computer's first.
func (f *Fleet) Workspace() Workspace {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.ws))
	for m := range f.ws {
		if m != "" {
			names = append(names, m)
		}
	}
	slices.Sort(names)
	return mergeWorkspaces(append([]string{""}, names...), f.ws)
}

var stateRank = map[string]int{"blocked": 5, "working": 4, "done": 3, "idle": 2, "unknown": 1, "exited": 0}

func mergeWorkspaces(order []string, ws map[string]Workspace) Workspace {
	out := Workspace{Environments: []Environment{}, Tabs: []Tab{}, Panes: []Pane{}, Agents: []Agent{}, Counts: map[string]int{}, Plans: []Plan{}, Branches: []Branch{}, Workflows: []WorkflowRun{}}
	for _, m := range order {
		w := ws[m]
		for _, e := range w.Environments {
			e.ID, e.Machine = qualify(m, e.ID), m
			out.Environments = append(out.Environments, e)
		}
		for _, t := range w.Tabs {
			t.ID, t.EnvironmentID = qualify(m, t.ID), qualify(m, t.EnvironmentID)
			t.Focused, t.Zoomed = qualify(m, t.Focused), qualify(m, t.Zoomed)
			out.Tabs = append(out.Tabs, t)
		}
		for _, p := range w.Panes {
			p.ID, p.TabID, p.EnvironmentID = qualify(m, p.ID), qualify(m, p.TabID), qualify(m, p.EnvironmentID)
			p.ProcessID, p.Agent = qualify(m, p.ProcessID), qualify(m, p.Agent)
			out.Panes = append(out.Panes, p)
		}
		for _, a := range w.Agents {
			a.ID, a.EnvironmentID = qualify(m, a.ID), qualify(m, a.EnvironmentID)
			a.TabID, a.PaneID, a.Machine = qualify(m, a.TabID), qualify(m, a.PaneID), m
			out.Agents = append(out.Agents, a)
		}
		for _, p := range w.Plans {
			p.ID, p.Machine = qualify(m, p.ID), m
			waiting := make([]PlanTask, len(p.Waiting))
			for i, t := range p.Waiting {
				t.PaneID = qualify(m, t.PaneID)
				waiting[i] = t
			}
			p.Waiting = waiting
			out.Plans = append(out.Plans, p)
		}
		for _, b := range w.Branches {
			b.ID, b.EnvironmentID, b.Machine = qualify(m, b.ID), qualify(m, b.EnvironmentID), m
			out.Branches = append(out.Branches, b)
		}
		for _, r := range w.Workflows {
			r.ID, r.Machine = qualify(m, r.ID), m
			out.Workflows = append(out.Workflows, r)
		}
		for k, v := range w.Counts {
			out.Counts[k] += v
		}
		if stateRank[w.State] > stateRank[out.State] || out.State == "" {
			out.State = w.State
		}
	}
	return out
}

// route finds the hub an ID belongs to, and the ID its daemon knows.
func (f *Fleet) route(id string) (*Hub, string, error) {
	machine, raw, ok := strings.Cut(id, machineSep)
	if !ok {
		return f.local, id, nil
	}
	f.mu.Lock()
	h, live := f.remotes[machine]
	f.mu.Unlock()
	if !live {
		return nil, "", fmt.Errorf("machine %q is not connected", machine)
	}
	return h, raw, nil
}

func machineOf(id string) string {
	if m, _, ok := strings.Cut(id, machineSep); ok {
		return m
	}
	return ""
}

// hubOf is the hub of a machine ("" for this computer).
func (f *Fleet) hubOf(machine string) (*Hub, error) {
	if machine == "" {
		return f.local, nil
	}
	h, _, err := f.route(machine + machineSep)
	return h, err
}

// --- the hub's calls, routed ---

func (f *Fleet) do(id string, fn func(h *Hub, raw string) error) error {
	h, raw, err := f.route(id)
	if err != nil {
		return err
	}
	return fn(h, raw)
}

func (f *Fleet) doID(id string, fn func(h *Hub, raw string) (string, error)) (string, error) {
	h, raw, err := f.route(id)
	if err != nil {
		return "", err
	}
	out, err := fn(h, raw)
	return qualify(machineOf(id), out), err
}

// OpenView, CloseView, Refresh, Input and Paste act on a pane's view.
func (f *Fleet) OpenView(viewer, pane string, cols, rows int) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.OpenView(viewer, raw, cols, rows) })
}

func (f *Fleet) CloseView(viewer, pane string) {
	_ = f.do(pane, func(h *Hub, raw string) error { h.CloseView(viewer, raw); return nil })
}

func (f *Fleet) Refresh(pane string) {
	_ = f.do(pane, func(h *Hub, raw string) error { h.Refresh(raw); return nil })
}

func (f *Fleet) Input(pane, data string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.Input(raw, data) })
}

func (f *Fleet) Paste(pane, text string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.Paste(raw, text) })
}

func (f *Fleet) FocusPane(pane string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.FocusPane(raw) })
}

func (f *Fleet) SplitPane(pane, dir string) (string, error) {
	return f.doID(pane, func(h *Hub, raw string) (string, error) { return h.SplitPane(raw, dir) })
}

func (f *Fleet) ResizePane(pane, dir string, cells int) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.ResizePane(raw, dir, cells) })
}

func (f *Fleet) FocusNeighbour(pane, dir string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.FocusNeighbour(raw, dir) })
}

// SwapPane exchanges two panes, which must be on one machine (and in one
// tab: the daemon checks).
func (f *Fleet) SwapPane(pane, with string) error {
	if machineOf(pane) != machineOf(with) {
		return errors.New("panes on different machines cannot be swapped")
	}
	_, rawWith, err := f.route(with)
	if err != nil {
		return err
	}
	return f.do(pane, func(h *Hub, raw string) error { return h.SwapPane(raw, rawWith) })
}

func (f *Fleet) ZoomPane(pane string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.ZoomPane(raw) })
}

func (f *Fleet) ClosePane(pane string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.ClosePane(raw) })
}

func (f *Fleet) RenamePane(pane, name string) error {
	return f.do(pane, func(h *Hub, raw string) error { return h.RenamePane(raw, name) })
}

func (f *Fleet) NewTab(env string) (string, error) {
	return f.doID(env, func(h *Hub, raw string) (string, error) { return h.NewTab(raw) })
}

func (f *Fleet) FocusTab(tab string) error {
	return f.do(tab, func(h *Hub, raw string) error { return h.FocusTab(raw) })
}

func (f *Fleet) ResizeTab(tab string, cols, rows int) error {
	return f.do(tab, func(h *Hub, raw string) error { return h.ResizeTab(raw, cols, rows) })
}

func (f *Fleet) CloseTab(tab string) error {
	return f.do(tab, func(h *Hub, raw string) error { return h.CloseTab(raw) })
}

// StartAgent starts an agent in the environment's daemon.
func (f *Fleet) StartAgent(req StartAgentRequest) (string, error) {
	return f.doID(req.EnvironmentID, func(h *Hub, raw string) (string, error) {
		req.EnvironmentID = raw
		return h.StartAgent(req)
	})
}

func (f *Fleet) Prompt(agent, text string) (string, error) {
	h, raw, err := f.route(agent)
	if err != nil {
		return "", err
	}
	return h.Prompt(raw, text)
}

// History returns a pane's recent lines.
func (f *Fleet) History(pane string, lines int) ([]string, error) {
	h, raw, err := f.route(pane)
	if err != nil {
		return nil, err
	}
	return h.History(raw, lines)
}

func (f *Fleet) StopAgent(agent string) error {
	return f.do(agent, func(h *Hub, raw string) error { return h.StopAgent(raw) })
}

// ApproveTask and CancelPlan act on a plan of the machine its ID names.
func (f *Fleet) ApproveTask(plan, task string, deny bool) error {
	return f.do(plan, func(h *Hub, raw string) error { return h.ApproveTask(raw, task, deny) })
}

func (f *Fleet) CancelPlan(plan string) error {
	return f.do(plan, func(h *Hub, raw string) error { return h.CancelPlan(raw) })
}

// Insights are read from the machine named ("" for this computer).
func (f *Fleet) Insights(machine string) (Insights, error) {
	h, err := f.hubOf(machine)
	if err != nil {
		return Insights{}, err
	}
	in, err := h.Insights()
	in.Machine = machine
	return in, err
}

// Plugins lists the plugins of the machine named ("" for this computer).
func (f *Fleet) Plugins(machine string) ([]Plugin, error) {
	h, err := f.hubOf(machine)
	if err != nil {
		return nil, err
	}
	return h.Plugins()
}

// ResumeBudget resumes a budget of the machine named.
func (f *Fleet) ResumeBudget(machine, name, key string) error {
	h, err := f.hubOf(machine)
	if err != nil {
		return err
	}
	return h.ResumeBudget(name, key)
}

// Workflow runs' actions go to the machine the run's ID names.
func (f *Fleet) CancelWorkflowRun(id string) error {
	return f.do(id, func(h *Hub, raw string) error { return h.CancelWorkflowRun(raw) })
}

func (f *Fleet) RerunWorkflow(id string) (string, error) {
	h, raw, err := f.route(id)
	if err != nil {
		return "", err
	}
	run, err := h.RerunWorkflow(raw)
	if err != nil {
		return "", err
	}
	return qualify(machineOf(id), run), nil
}

// Branches' actions go to the machine the branch's ID names.
func (f *Fleet) BranchDiff(ref string) (BranchDiff, error) {
	h, raw, err := f.route(ref)
	if err != nil {
		return BranchDiff{}, err
	}
	return h.BranchDiff(raw)
}

func (f *Fleet) CommentBranch(ref, path string, line int, body string, send bool) error {
	return f.do(ref, func(h *Hub, raw string) error { return h.CommentBranch(raw, path, line, body, send) })
}

func (f *Fleet) SendComments(ref string) error {
	return f.do(ref, func(h *Hub, raw string) error { return h.SendComments(raw) })
}

func (f *Fleet) ReadyBranch(ref string) (bool, error) {
	h, raw, err := f.route(ref)
	if err != nil {
		return false, err
	}
	return h.ReadyBranch(raw)
}

func (f *Fleet) PushBranch(ref string) error {
	return f.do(ref, func(h *Hub, raw string) error { return h.PushBranch(raw) })
}

func (f *Fleet) OpenPullRequest(ref string, draft, push bool) (string, error) {
	h, raw, err := f.route(ref)
	if err != nil {
		return "", err
	}
	return h.OpenPullRequest(raw, draft, push)
}

func (f *Fleet) CleanBranch(ref string, force bool) error {
	return f.do(ref, func(h *Hub, raw string) error { return h.CleanBranch(raw, force) })
}

// Manifests lists the agents a machine's Hive can start.
func (f *Fleet) Manifests(machine string) ([]Manifest, error) {
	h, err := f.hubOf(machine)
	if err != nil {
		return []Manifest{}, err
	}
	return h.Manifests()
}

// CreateEnvironment makes an environment on a machine.
func (f *Fleet) CreateEnvironment(machine, id, dir string) error {
	h, err := f.hubOf(machine)
	if err != nil {
		return err
	}
	return h.CreateEnvironment(id, dir)
}

// Retry connects every disconnected daemon again now.
func (f *Fleet) Retry() {
	f.local.Retry()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.remotes {
		h.Retry()
	}
}

// --- machine management (through the CLI), then a fresh look ---

func (f *Fleet) Discover() (Discovery, error) { return f.local.Discover(f.ctx) }

func (f *Fleet) CheckPair(r PairRequest) (IdentityCheck, error) { return f.local.CheckPair(f.ctx, r) }

func (f *Fleet) Pair(r PairRequest, expectID string) error {
	defer f.RefreshMachines()
	return f.local.Pair(f.ctx, r, expectID)
}

func (f *Fleet) CheckTrust(name string) (IdentityCheck, error) {
	return f.local.CheckTrust(f.ctx, name)
}

func (f *Fleet) Trust(name, expectID string) error {
	defer f.RefreshMachines()
	return f.local.Trust(f.ctx, name, expectID)
}

func (f *Fleet) MachineAction(name, action string) error {
	defer f.RefreshMachines()
	return f.local.MachineAction(f.ctx, name, action)
}
