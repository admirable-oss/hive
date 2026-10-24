package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Context is what a plugin command learns about why it runs: on stdin and
// in the file $HIVE_PLUGIN_CONTEXT. Its shape is part of plugin API 1:
// fields are only ever added.
type Context struct {
	// API is the plugin API version of this context.
	API     int    `json:"api"`
	Plugin  string `json:"plugin"`
	Trigger string `json:"trigger"` // startup, action, event or migration
	Action  string `json:"action,omitempty"`
	// Run identifies this action run (plugin.cancel takes it).
	Run string `json:"run,omitempty"`
	// Event is the event that triggered it: its type and data, as
	// `hive events` prints them.
	Event   *EventInfo `json:"event,omitempty"`
	Session string     `json:"session"`
	// Environment, Tab and Pane are where the user was (actions from the
	// UI), or what the event is about when it says.
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
	// Capabilities are the capabilities the plugin holds now.
	Capabilities []string `json:"capabilities"`
	// Workflow is set when a workflow's step runs the action: the run, the
	// step, what it gives the action, and the file outputs go to (also
	// $HIVE_OUTPUT).
	Workflow *WorkflowContext `json:"workflow,omitempty"`
}

// WorkflowContext is the workflow step an action runs for.
type WorkflowContext struct {
	Workflow string            `json:"workflow"`
	Run      string            `json:"run"`
	Step     string            `json:"step"`
	With     map[string]string `json:"with,omitempty"`
	Output   string            `json:"output,omitempty"`
}

// EventInfo is an event passed to a plugin.
type EventInfo struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	// Seq is the daemon's event number: events reach a plugin in Seq
	// order (with event_concurrency 1 they also finish in that order).
	Seq  uint64    `json:"seq,omitempty"`
	Time time.Time `json:"time,omitzero"`
	// Missed says events were lost before this one (a queue overflowed):
	// re-read whatever state the plugin follows.
	Missed bool `json:"missed,omitempty"`
}

// Result is how an action went.
type Result struct {
	ExitCode int `json:"exit_code"`
	// Output is the end of what it printed (stdout and stderr).
	Output string `json:"output"`
	// Run identifies the run; Async says it was started, not waited for.
	Run   string `json:"run,omitempty"`
	Async bool   `json:"async,omitempty"`
}

// Status is a plugin as the daemon runs it.
type Status struct {
	Plugin
	// Running counts its commands running now.
	Running int `json:"running"`
	// LastError is its latest failure.
	LastError string `json:"last_error,omitempty"`
	Health    Health `json:"health"`
	// Grant is what it may do; Isolation is how it runs (standard or
	// strict).
	Grant     Grant  `json:"grant"`
	Isolation string `json:"isolation"`
	API       int    `json:"api"`
	// Runs are its action runs in progress.
	Runs []RunInfo `json:"runs,omitempty"`
}

// Isolation modes ([plugins] isolation).
const (
	IsolationAuto     = "auto"     // strict for plugins that support it
	IsolationStandard = "standard" // never strict
	IsolationStrict   = "strict"   // strict for every plugin
)

// HostConfig sets up a Host.
type HostConfig struct {
	Store *Store
	// Version is this hive's, for min_hive_version.
	Version string
	// Session is the session the daemon serves.
	Session string
	// Env builds a command's environment from Hive's variables for it
	// (the daemon's launch environment: HIVE_BIN, HIVE_SOCKET_PATH, …).
	Env func(vars map[string]string) []string
	// LogDir holds each plugin's output log (<id>.log, rotated).
	LogDir string
	Logger *slog.Logger

	// Policy decides capabilities with the user's grants.
	Policy Policy
	// Isolation is auto (default), standard or strict.
	Isolation string
	// RestartLimit and RestartWindow stop restarting a startup command
	// that fails more than RestartLimit times within RestartWindow
	// (defaults 5 and 10 minutes).
	RestartLimit  int
	RestartWindow time.Duration
	// Hidden are contributions the user turned off: "plugin",
	// "plugin:kind" or "plugin:kind:id".
	Hidden []string
	// Serve opens a plugin's own socket, which accepts only the methods
	// its capabilities allow and knows the caller is that plugin. Nil
	// leaves plugins on the daemon's socket (tests).
	Serve func(id string, granted []string) (path string, stop func(), err error)
	// SocketPath is the daemon's own socket, which strict isolation hides.
	SocketPath string
	// Publish announces plugin events on the daemon's bus.
	Publish func(typ string, data any)
}

func (c HostConfig) withDefaults() HostConfig {
	if c.RestartLimit <= 0 {
		c.RestartLimit = 5
	}
	if c.RestartWindow <= 0 {
		c.RestartWindow = 10 * time.Minute
	}
	if c.Isolation == "" {
		c.Isolation = IsolationAuto
	}
	if c.Publish == nil {
		c.Publish = func(string, any) {}
	}
	return c
}

// Host runs the enabled plugins for the daemon. Each plugin has its own
// lifetime: a crash, a hang or a full queue in one never holds up another
// or the daemon, and one can be reloaded alone.
type Host struct {
	cfg HostConfig
	log *slog.Logger

	mu      sync.Mutex
	ctx     context.Context // the host's life (Run)
	plugins map[string]*loaded
	missed  bool // the daemon lost events since the last delivery
	running sync.WaitGroup
	runSeq  uint64
	content contentStore
}

type loaded struct {
	Plugin
	grant   Grant
	strict  bool
	problem string // a load-time problem: it runs nothing

	ctx    context.Context // its life: until unloaded
	cancel context.CancelFunc
	wg     sync.WaitGroup // its startup loops and event workers

	log    io.Writer
	closer io.Closer
	logMu  sync.Mutex

	socket     string
	stopSocket func()

	queue  chan queuedEvent
	missed bool // it dropped an event since its last delivery

	running  int
	lastErr  string
	health   Health
	crashes  []time.Time
	startups int // startup commands configured
	runs     map[string]*actionRun
}

// NewHost returns a host; Run starts it.
func NewHost(cfg HostConfig) *Host {
	if cfg.Env == nil {
		cfg.Env = func(vars map[string]string) []string {
			env := os.Environ()
			for k, v := range vars {
				env = append(env, k+"="+v)
			}
			return env
		}
	}
	cfg = cfg.withDefaults()
	return &Host{cfg: cfg, log: logging.OrDiscard(cfg.Logger), plugins: map[string]*loaded{}}
}

// Run loads the plugins and runs them until ctx ends, then stops every
// command they run.
func (h *Host) Run(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
	h.Reload()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			ids := slices.Collect(maps.Keys(h.plugins))
			h.mu.Unlock()
			for _, id := range ids {
				h.unload(id)
			}
			h.running.Wait()
			return
		case <-t.C:
			h.tick()
		}
	}
}

// tick expires contributions and refreshes health states that change
// with time (degraded fades, readiness by uptime).
func (h *Host) tick() {
	expired := h.content.expire(time.Now())
	h.mu.Lock()
	var changed []Status
	for _, l := range h.plugins {
		if h.refreshLocked(l) {
			changed = append(changed, h.statusOfLocked(l))
		}
	}
	h.mu.Unlock()
	for _, id := range expired {
		h.cfg.Publish(EventContributions, map[string]string{"plugin": id})
	}
	for _, st := range changed {
		h.cfg.Publish(EventChanged, st)
	}
}

// Reload reads the installed plugins again and restarts every one: its
// commands stop, and the enabled plugins start again.
func (h *Host) Reload() []Status {
	h.mu.Lock()
	ctx := h.ctx
	ids := slices.Collect(maps.Keys(h.plugins))
	h.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return nil
	}
	for _, id := range ids {
		h.unload(id)
	}
	list, err := h.cfg.Store.List(ctx, h.cfg.Version)
	if err != nil {
		h.log.Error("read installed plugins", "err", err)
	}
	for _, p := range list {
		h.load(ctx, p)
	}
	return h.Status()
}

// ReloadPlugin restarts one plugin: its commands stop, its manifest,
// grants and settings are read again, and it starts afresh (its crash
// count too). A plugin that was disabled or uninstalled just stops.
func (h *Host) ReloadPlugin(id string) (Status, error) {
	h.mu.Lock()
	ctx := h.ctx
	h.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return Status{}, fmt.Errorf("plugin host is not running")
	}
	h.unload(id)
	p, err := h.cfg.Store.Get(ctx, id, h.cfg.Version)
	if err != nil {
		return Status{}, err
	}
	h.load(ctx, p)
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.plugins[id]
	if l == nil {
		return Status{Plugin: p, Health: Health{State: StateDisabled}}, nil
	}
	return h.statusOfLocked(l), nil
}

// load starts p when it is enabled. A plugin that cannot run is still
// recorded, failed, so its health says why.
func (h *Host) load(ctx context.Context, p Plugin) {
	if !p.Enabled {
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	l := &loaded{Plugin: p, ctx: lctx, cancel: cancel, log: io.Discard, runs: map[string]*actionRun{}}
	l.health = Health{State: StateStarting, Since: time.Now(), LoadedAt: time.Now()}
	fail := func(problem string) {
		l.problem, l.lastErr = problem, problem
		l.health.LastError, l.health.LastErrorAt = problem, time.Now()
		h.mu.Lock()
		h.plugins[p.ID] = l
		h.refreshLocked(l)
		st := h.statusOfLocked(l)
		h.mu.Unlock()
		h.log.Warn("plugin not loaded", "plugin", p.ID, "problem", problem)
		h.cfg.Publish(EventChanged, st)
	}
	if p.Problem != "" || p.Manifest == nil {
		fail(cmpStr(p.Problem, "no manifest"))
		return
	}
	m := p.Manifest
	l.grant = Decide(m, p.Granted, h.cfg.Policy)
	if problem := l.grant.Problem(p.ID); problem != "" {
		fail(problem)
		return
	}
	// auto sandboxes a plugin that can run sandboxed, where a sandbox
	// exists; strict, chosen by the user, never runs a plugin without one.
	l.strict = h.cfg.Isolation == IsolationStrict || (h.cfg.Isolation == IsolationAuto && m.Isolation == "strict" && SandboxAvailable())
	if l.strict && !SandboxAvailable() {
		fail("[plugins] isolation is strict, which needs " + sandboxTool() + ", not available here")
		return
	}
	if m.Legacy() {
		l.health.Warnings = append(l.health.Warnings, "declares no plugin API or permissions, so it holds every capability; its author can add api = 1 and [permissions]")
	}
	for _, e := range m.Events {
		if !KnownEvent(e.On) {
			l.health.Warnings = append(l.health.Warnings, fmt.Sprintf("handles %q, which plugin API %d never publishes", e.On, APIVersion))
		}
	}
	warnings, err := h.prepareConfiguration(lctx, l)
	if err != nil {
		fail("configuration: " + err.Error() + " (see `hive plugin config " + p.ID + "`)")
		return
	}
	l.health.Warnings = append(l.health.Warnings, warnings...)
	if h.cfg.LogDir != "" {
		if f, err := logging.OpenRotating(filepath.Join(h.cfg.LogDir, p.ID+".log"), 1<<20, 2); err == nil {
			l.log, l.closer = f, f
		}
	}
	if h.cfg.Serve != nil {
		path, stop, err := h.cfg.Serve(p.ID, l.grant.Effective)
		if err != nil {
			fail("open its socket: " + err.Error())
			return
		}
		l.socket, l.stopSocket = path, stop
	}
	workers := m.EventConcurrency
	if workers <= 0 {
		workers = defaultEventConcurrency
	}
	l.queue = make(chan queuedEvent, eventQueue)
	l.startups = len(m.Startup)
	h.mu.Lock()
	h.plugins[p.ID] = l
	if l.startups == 0 {
		h.markReadyLocked(l)
	}
	for range workers {
		l.wg.Go(func() { h.deliver(l) }) //nolint:contextcheck // workers live as long as the plugin (l.ctx), not this load call
	}
	for _, s := range m.Startup {
		l.wg.Go(func() { h.runStartup(l, s) })
	}
	st := h.statusOfLocked(l)
	h.mu.Unlock()
	h.log.Info("plugin loaded", "plugin", p.ID, "version", m.Version, "api", m.EffectiveAPI(), "strict", l.strict, "capabilities", l.grant.Effective)
	h.cfg.Publish(EventChanged, st)
}

// unload stops a plugin: its context ends, its commands are stopped, and
// its socket and log close. It waits for them.
func (h *Host) unload(id string) {
	h.mu.Lock()
	l := h.plugins[id]
	delete(h.plugins, id)
	h.mu.Unlock()
	if l == nil {
		return
	}
	l.cancel()
	l.wg.Wait()
	if l.stopSocket != nil {
		l.stopSocket()
	}
	if l.closer != nil {
		l.logMu.Lock()
		_ = l.closer.Close()
		l.log = io.Discard
		l.logMu.Unlock()
	}
	if h.content.forget(id) {
		h.cfg.Publish(EventContributions, map[string]string{"plugin": id})
	}
}

// Status lists the plugins the host runs.
func (h *Host) Status() []Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Status{}
	for _, l := range h.plugins {
		h.refreshLocked(l)
		out = append(out, h.statusOfLocked(l))
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (h *Host) statusOfLocked(l *loaded) Status {
	st := Status{Plugin: l.Plugin, Running: l.running, LastError: l.lastErr, Health: l.health, Grant: l.grant, Isolation: IsolationStandard, API: APIVersion}
	if l.Manifest != nil {
		st.API = l.Manifest.EffectiveAPI()
	}
	if l.strict {
		st.Isolation = IsolationStrict
	}
	st.Health.Warnings = slices.Clone(l.health.Warnings)
	st.Health.Requests = slices.Clone(l.health.Requests)
	for _, r := range l.runs {
		st.Runs = append(st.Runs, r.info)
	}
	slices.SortFunc(st.Runs, func(a, b RunInfo) int { return a.Started.Compare(b.Started) })
	return st
}

// Granted returns what a loaded plugin holds now (nil when it is not
// loaded): its socket checks calls against it.
func (h *Host) Granted(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.plugins[id]; l != nil && l.problem == "" {
		return slices.Clone(l.grant.Effective)
	}
	return nil
}

// Discovery describes this hive to plugins; with id, also what that
// plugin holds and may call.
func (h *Host) Discovery(id string) Discovery {
	d := Discovery{
		API: APIVersion, SupportedAPIs: slices.Clone(SupportedAPIs), HiveVersion: h.cfg.Version,
		Capabilities: slices.Clone(CapabilityList), Events: slices.Clone(Events),
		Contributions: slices.Clone(ContributionKinds), Isolation: []string{IsolationStandard},
	}
	if SandboxAvailable() {
		d.Isolation = append(d.Isolation, IsolationStrict)
	}
	if id != "" {
		d.Plugin = id
		d.Granted = h.Granted(id)
		for _, m := range ClassifiedMethods() {
			if Allowed(m, d.Granted) {
				d.Methods = append(d.Methods, m)
			}
		}
	}
	return d
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
