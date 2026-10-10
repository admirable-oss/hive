package runtime

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/persist"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/terminal"
)

// PersistConfig turns on session snapshots and their restore (see package
// persist).
type PersistConfig struct {
	// Keep is how many earlier snapshots are kept (default 48).
	Keep int
	// Interval is the snapshot timer besides changes (default 30s).
	Interval time.Duration
	// Restore starts panes whose processes were lost (a reboot) again
	// when the daemon starts; ResumeAgents resumes their agents'
	// conversations. Stagger separates agents' starts.
	Restore      bool
	ResumeAgents bool
	Stagger      time.Duration
	// ReplayHistory keeps panes' recent text (ReplayLines lines) and shows
	// it again in restored panes.
	ReplayHistory bool
	ReplayLines   int
}

// RetentionConfig bounds what is kept on disk.
type RetentionConfig struct {
	// ExitedProcesses is how long ended processes no pane shows are kept
	// (default 7 days).
	ExitedProcesses time.Duration
	// GCInterval is how often the daemon cleans up; zero never.
	GCInterval time.Duration
}

// EventSessionRestored reports a restore: persist.RestoreReport.
const EventSessionRestored = "session.restored"

// Variables Hive sets in every pane itself; a restored pane gets new ones.
var paneVars = []string{"HIVE_PANE_ID", "HIVE_TAB_ID"}

// keeper saves the workspace in snapshots, restores it after a reboot, and
// cleans up what is no longer needed.
type keeper struct {
	cfg       *PersistConfig // nil: snapshots are off
	retention RetentionConfig
	root      string
	store     *persist.Store
	history   *persist.History
	saver     *persist.Saver

	envs   environment.Service
	panes  *pane.Service
	procs  process.Service
	terms  terminal.Service
	agents *agent.Service
	events *event.Bus
	log    *slog.Logger
	cwd    func(pid int) (string, error)
	now    func() time.Time

	mu sync.Mutex
	// last is the newest snapshot, loaded at start and then collected: it
	// remembers what panes that are no longer running had.
	last persist.Snapshot
	// sessions holds agents' session IDs by process ID: reported ones, and
	// those read from the last snapshot or given to restored agents, which
	// have not reported theirs (again) yet.
	sessions map[string]string
	// historySums avoids rewriting unchanged pane history.
	historySums map[string]uint64
}

func newKeeper(cfg Config, envs environment.Service, panes *pane.Service, procs process.Service, terms terminal.Service, agents *agent.Service, bus *event.Bus, log *slog.Logger) *keeper {
	root := cfg.root()
	k := &keeper{
		cfg: cfg.Persist, retention: cfg.Retention, root: root,
		store:   persist.NewStore(root, 0, log),
		history: persist.NewHistory(filepath.Join(root, "history")),
		envs:    envs, panes: panes, procs: procs, terms: terms, agents: agents, events: bus,
		log: log, cwd: platform.ProcessCwd, now: time.Now,
		sessions: map[string]string{}, historySums: map[string]uint64{},
	}
	if k.retention.ExitedProcesses <= 0 {
		k.retention.ExitedProcesses = 7 * 24 * time.Hour
	}
	if k.cfg != nil {
		k.store = persist.NewStore(root, k.cfg.Keep, log)
		k.saver = persist.NewSaver(k.store, k.collect, 0, k.cfg.Interval, log)
	}
	return k
}

// load reads the last snapshot; the daemon calls it before anything runs.
func (k *keeper) load() {
	snap, err := k.store.Load()
	if err != nil {
		if !errors.Is(err, persist.ErrNoSnapshot) {
			k.log.Warn("could not read the session snapshot", "err", err)
		}
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.last = snap
	for _, e := range snap.Environments {
		for _, p := range e.Panes {
			if p.Agent != nil && p.Agent.SessionID != "" {
				k.sessions[p.ProcessID] = p.Agent.SessionID
			}
		}
	}
}

// layoutFor rebuilds an environment's tabs and panes from the last
// snapshot, for a layout.json that could not be read.
func (k *keeper) layoutFor(envID string) (pane.State, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e := k.last.Environment(envID)
	if e == nil {
		return pane.State{}, false
	}
	st := pane.State{ActiveTab: e.ActiveTab, Tabs: slices.Clone(e.Tabs)}
	for i := range st.Tabs {
		st.Tabs[i].Active = false
	}
	for _, p := range e.Panes {
		st.Panes = append(st.Panes, pane.Pane{ID: p.ID, TabID: p.TabID, EnvironmentID: envID, ProcessID: p.ProcessID, Name: p.Name})
	}
	return st, true
}

// run takes snapshots after changes and on the timer, and one when the
// host announces a shutdown, until ctx ends.
func (k *keeper) run(ctx context.Context) {
	if k.saver == nil {
		return
	}
	if !k.cfg.ReplayHistory {
		// Text kept while it was on goes once it is turned off.
		if removed, err := k.history.Prune(nil, false); err != nil || len(removed) > 0 {
			k.log.Info("removed kept pane history (session.replay_history is off)", "files", len(removed), "err", err)
		}
	}
	sub := k.events.Subscribe(event.DefaultBuffer, "environment.", "tab.", "pane.", "layout.", "process.", agent.EventState)
	defer sub.Close()
	var helpers sync.WaitGroup
	defer helpers.Wait()
	helpers.Go(func() {
		for {
			if _, err := sub.Next(ctx); err != nil {
				return
			}
			k.saver.Changed()
		}
	})
	helpers.Go(func() {
		err := platform.WatchShutdown(ctx, func(ctx context.Context) {
			k.log.Info("the host is shutting down; saving the session")
			k.save(ctx)
		})
		if err != nil && !errors.Is(err, platform.ErrUnsupported) && ctx.Err() == nil {
			k.log.Debug("no shutdown notice from the host; snapshots rely on the timer and on SIGTERM", "err", err)
		}
	})
	k.saver.Changed() // a first snapshot of what this daemon found
	k.saver.Run(ctx)
}

// save writes a snapshot now (on shutdown).
func (k *keeper) save(ctx context.Context) {
	if k.saver == nil {
		return
	}
	if _, err := k.saver.Save(ctx); err != nil {
		k.log.Warn("session snapshot failed", "err", err)
	}
}

// collect builds a snapshot of the workspace.
func (k *keeper) collect(ctx context.Context) (persist.Snapshot, error) {
	envs, err := k.envs.List(ctx)
	if err != nil {
		return persist.Snapshot{}, err
	}
	tabs, err := k.panes.Tabs(ctx, "")
	if err != nil {
		return persist.Snapshot{}, err
	}
	panes, err := k.panes.Panes(ctx, "", "")
	if err != nil {
		return persist.Snapshot{}, err
	}
	agents, err := k.agents.List(ctx, false)
	if err != nil {
		return persist.Snapshot{}, err
	}
	byProc := map[string]agent.Agent{}
	for _, a := range agents {
		byProc[a.ID] = a
	}
	snap := persist.Snapshot{Version: buildinfo.Get().Version}
	if boot, err := platform.BootTime(); err == nil {
		snap.BootTime = boot
	}

	k.mu.Lock()
	last := k.last
	k.mu.Unlock()
	for _, env := range envs {
		e := persist.Environment{ID: env.ID, Path: env.Path, Tabs: []pane.Tab{}, Panes: []persist.Pane{}}
		for _, t := range tabs {
			if t.EnvironmentID != env.ID {
				continue
			}
			if t.Active {
				e.ActiveTab = t.ID
			}
			t.Active = false
			e.Tabs = append(e.Tabs, t)
		}
		for _, p := range panes {
			if p.EnvironmentID == env.ID {
				e.Panes = append(e.Panes, k.describe(ctx, p, byProc, last.Pane(p.ID)))
			}
		}
		snap.Environments = append(snap.Environments, e)
	}

	k.mu.Lock()
	k.last = snap
	k.mu.Unlock()
	return snap, nil
}

// describe is one pane's entry in a snapshot. prev is its entry in the
// last snapshot: a pane that no longer runs keeps what it had.
func (k *keeper) describe(ctx context.Context, p pane.Pane, agents map[string]agent.Agent, prev *persist.Pane) persist.Pane {
	out := persist.Pane{ID: p.ID, TabID: p.TabID, Name: p.Name, ProcessID: p.ProcessID}
	proc := p.Process
	if proc == nil {
		return out
	}
	argv := append([]string{proc.Command}, proc.Args...)
	out.Command, out.Env, out.Running, out.Cwd = argv, withoutVars(proc.Env, paneVars...), proc.Active(), proc.WorkingDir
	if !out.Running {
		if prev != nil && prev.ProcessID == p.ProcessID {
			out.Cwd, out.Agent = prev.Cwd, prev.Agent
		}
		return out
	}
	if proc.PID > 0 {
		if dir, err := k.cwd(proc.PID); err == nil {
			out.Cwd = dir
		}
	}
	if k.cfg != nil && k.cfg.ReplayHistory {
		k.keepHistory(ctx, p)
	}
	a, ok := agents[proc.ID]
	if !ok || a.Kind == "" || a.State == agent.StateExited {
		return out
	}
	ag := &persist.Agent{Kind: a.Kind, SessionID: a.SessionID}
	k.mu.Lock()
	if ag.SessionID != "" {
		k.sessions[proc.ID] = ag.SessionID
	} else {
		ag.SessionID = k.sessions[proc.ID]
	}
	k.mu.Unlock()
	if m := k.agents.Manifest(a.Kind); proc.Env[agent.EnvAgent] == a.Kind || (m != nil && m.Matches(argv)) {
		ag.Args = argv // the pane's own program
	} else {
		ag.InShell, ag.Args = true, k.foreground(ctx, proc.ID)
		if len(ag.Args) == 0 && prev != nil && prev.Agent != nil && prev.Agent.Kind == a.Kind {
			ag.Args = prev.Agent.Args
		}
	}
	out.Agent = ag
	return out
}

// foreground is the command line of the program in front of a terminal.
func (k *keeper) foreground(ctx context.Context, processID string) []string {
	sess, err := k.terms.Get(processID)
	if err != nil {
		return nil
	}
	fr, ok := sess.(terminal.ForegroundReader)
	if !ok {
		return nil
	}
	fg, err := fr.Foreground(ctx)
	if err != nil {
		return nil
	}
	return fg.Args
}

// keepHistory stores a pane's recent text when it changed.
func (k *keeper) keepHistory(ctx context.Context, p pane.Pane) {
	sess, err := k.terms.Get(p.ProcessID)
	if err != nil {
		return
	}
	lines, err := sess.Read(ctx, terminal.ReadRequest{Source: terminal.SourceRecent, Lines: max(k.cfg.ReplayLines, 1), ANSI: true})
	if err != nil {
		return
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	text := []byte(strings.Join(lines, "\r\n"))
	h := fnv.New64a()
	_, _ = h.Write(text)
	sum := h.Sum64()
	k.mu.Lock()
	same := k.historySums[p.ID] == sum
	k.historySums[p.ID] = sum
	k.mu.Unlock()
	if same {
		return
	}
	if err := k.history.Write(p.ID, text); err != nil {
		k.log.Debug("keep pane history", "pane", p.ID, "err", err)
	}
}

// restorePlan is how one pane is started again.
type restorePlan struct {
	persist.RestoredPane
	spec pane.Spec
}

// restore starts every pane whose process was lost (with the host or its
// shim) again, in place: plain panes at once, then agents, Stagger apart.
func (k *keeper) restore(ctx context.Context, resumeAgents bool) (persist.RestoreReport, error) {
	panes, err := k.panes.Panes(ctx, "", "")
	if err != nil {
		return persist.RestoreReport{}, err
	}
	k.mu.Lock()
	last := k.last
	k.mu.Unlock()
	var plain, agents []restorePlan
	for _, p := range panes {
		if p.Process == nil || !p.Process.Lost || p.Popup {
			continue
		}
		plan := k.plan(p, last.Pane(p.ID), resumeAgents)
		if plan.Agent != "" {
			agents = append(agents, plan)
		} else {
			plain = append(plain, plan)
		}
	}
	report := persist.RestoreReport{Panes: []persist.RestoredPane{}}
	if len(plain)+len(agents) == 0 {
		return report, nil
	}
	k.log.Info("restoring panes whose processes were lost", "panes", len(plain), "agents", len(agents))
	for i, plan := range append(plain, agents...) {
		if i > len(plain) && k.stagger() > 0 {
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(k.stagger()):
			}
		}
		report.Panes = append(report.Panes, k.start(ctx, plan))
	}
	k.events.Publish(EventSessionRestored, report)
	return report, nil
}

func (k *keeper) stagger() time.Duration {
	if k.cfg == nil {
		return 0
	}
	return k.cfg.Stagger
}

// plan works out how to start pane p again from its lost process and its
// entry in the last snapshot (prev).
func (k *keeper) plan(p pane.Pane, prev *persist.Pane, resumeAgents bool) restorePlan {
	proc := p.Process
	argv := append([]string{proc.Command}, proc.Args...)
	if prev != nil && prev.ProcessID != proc.ID {
		prev = nil // the snapshot predates this process
	}
	plan := restorePlan{RestoredPane: persist.RestoredPane{Pane: p.ID, Environment: p.EnvironmentID, Command: argv}}
	for _, dir := range []string{prevCwd(prev), proc.WorkingDir} {
		if fi, err := os.Stat(dir); dir != "" && err == nil && fi.IsDir() {
			plan.Cwd = dir
			break
		}
	}
	plan.spec = pane.Spec{Command: argv, Cwd: plan.Cwd, Env: withoutVars(proc.Env, paneVars...)}
	if k.cfg != nil && k.cfg.ReplayHistory {
		if text, err := k.history.Read(p.ID); err == nil && len(text) > 0 {
			stamp := k.now().Format("2006-01-02 15:04")
			plan.spec.Replay = slices.Concat(text, []byte("\x1b[0m\r\n\x1b[2m── restored by hive, "+stamp+" ──\x1b[0m\r\n"))
		}
	}
	if prev == nil || prev.Agent == nil {
		return plan
	}
	ag := prev.Agent
	plan.Agent = ag.Kind
	m := k.agents.Manifest(ag.Kind)
	if !resumeAgents || m == nil {
		return plan // a shell comes back as a shell; an agent pane starts it fresh
	}
	cmd, ok := m.ResumeCommand(ag.Args, ag.SessionID)
	if !ok {
		return plan
	}
	plan.Resumed = "last"
	if ag.SessionID != "" && len(m.Resume.Args) > 0 {
		plan.Resumed = "session"
	}
	if ag.InShell {
		plan.Typed = shellJoin(cmd)
	} else {
		plan.Command, plan.spec.Command = cmd, cmd
	}
	return plan
}

func prevCwd(p *persist.Pane) string {
	if p == nil {
		return ""
	}
	return p.Cwd
}

// start carries out one plan.
func (k *keeper) start(ctx context.Context, plan restorePlan) persist.RestoredPane {
	out := plan.RestoredPane
	log := k.log.With("pane", out.Pane, "env", out.Environment)
	p, err := k.panes.Respawn(ctx, out.Pane, plan.spec)
	if err != nil {
		out.Error = err.Error()
		log.Warn("could not restore pane", "err", err)
		return out
	}
	out.ProcessID = p.ProcessID
	if prev := k.previousSession(out); prev != "" {
		k.mu.Lock()
		k.sessions[p.ProcessID] = prev
		k.mu.Unlock()
	}
	if out.Typed != "" {
		k.waitForPrompt(ctx, out.Pane)
		if err := k.panes.Run(ctx, out.Pane, out.Typed); err != nil {
			out.Error = err.Error()
		}
	}
	log.Info("restored pane", "process", p.ProcessID, "command", out.Command, "cwd", out.Cwd, "agent", out.Agent, "resumed", out.Resumed, "typed", out.Typed)
	return out
}

// previousSession is the session a restored agent resumed, which it has
// not reported yet.
func (k *keeper) previousSession(r persist.RestoredPane) string {
	if r.Resumed != "session" {
		return ""
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if p := k.last.Pane(r.Pane); p != nil && p.Agent != nil {
		return p.Agent.SessionID
	}
	return ""
}

// waitForPrompt gives a restored shell a moment to show its prompt before
// a command is typed into it: shells that reset the terminal as they start
// would drop input typed earlier.
func (k *keeper) waitForPrompt(ctx context.Context, paneID string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		lines, err := k.panes.Read(ctx, paneID, terminal.ReadRequest{})
		if err == nil && slices.ContainsFunc(lines, func(l string) bool { return strings.TrimSpace(l) != "" }) {
			time.Sleep(200 * time.Millisecond) // the rest of the prompt
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// withoutVars copies env without the named variables.
func withoutVars(env map[string]string, names ...string) map[string]string {
	out := maps.Clone(env)
	for _, n := range names {
		delete(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shellJoin quotes argv for a POSIX shell (sh, bash, zsh).
func shellJoin(argv []string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r)
	}
	q := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.IndexFunc(a, func(r rune) bool { return !safe(r) }) < 0 {
			q[i] = a
			continue
		}
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// errSnapshotsOff is returned by session.save when snapshots are off.
var errSnapshotsOff = fmt.Errorf("session snapshots are off in this daemon")
