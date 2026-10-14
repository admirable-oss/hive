package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
)

// Context is what a plugin command learns about why it runs: on stdin and
// in the file $HIVE_PLUGIN_CONTEXT.
type Context struct {
	Plugin  string `json:"plugin"`
	Trigger string `json:"trigger"` // startup, action or event
	Action  string `json:"action,omitempty"`
	// Event is the event that triggered it: its type and data, as
	// `hive events` prints them.
	Event   *EventInfo `json:"event,omitempty"`
	Session string     `json:"session"`
	// Environment, Tab and Pane are where the user was (actions from the
	// UI), or what the event is about when it says.
	Environment string `json:"environment,omitempty"`
	Tab         string `json:"tab,omitempty"`
	Pane        string `json:"pane,omitempty"`
}

// EventInfo is an event passed to a plugin.
type EventInfo struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Result is how an action went.
type Result struct {
	ExitCode int `json:"exit_code"`
	// Output is the end of what it printed (stdout and stderr).
	Output string `json:"output"`
}

// Status is a plugin as the daemon runs it.
type Status struct {
	Plugin
	// Running counts its startup commands running now.
	Running int `json:"running"`
	// LastError is its latest failure (a command that could not start,
	// or exited with an error).
	LastError string `json:"last_error,omitempty"`
}

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
}

// Host runs the enabled plugins' commands for the daemon.
type Host struct {
	cfg HostConfig
	log *slog.Logger

	mu      sync.Mutex
	ctx     context.Context // the host's life (Run)
	plugins map[string]*loaded
	stop    context.CancelFunc // ends the current load's startup commands
	running sync.WaitGroup     // every command the host started
	loads   sync.WaitGroup     // the current load's startup loops
}

type loaded struct {
	Plugin
	log     io.Writer
	closer  io.Closer
	running int
	lastErr string
	problem string        // a load-time problem that prevents actions and events
	sem     chan struct{} // bounds its concurrent event commands
	logMu   sync.Mutex    // its commands write their output to log
}

// maxEventCommands bounds one plugin's event commands running at once;
// more events wait.
const maxEventCommands = 4

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
	return &Host{cfg: cfg, log: logging.OrDiscard(cfg.Logger), plugins: map[string]*loaded{}}
}

// Run loads the plugins and runs them until ctx ends, then stops every
// command they run.
func (h *Host) Run(ctx context.Context) {
	h.mu.Lock()
	h.ctx = ctx
	h.mu.Unlock()
	h.Reload() //nolint:contextcheck // the host keeps ctx; plugins live as long as it
	<-ctx.Done()
	h.mu.Lock()
	h.unloadLocked()
	h.mu.Unlock()
	h.loads.Wait()
	h.running.Wait()
}

// Reload reads the installed plugins again and restarts what changed: every
// startup command stops, and the enabled plugins' start again.
func (h *Host) Reload() []Status {
	h.mu.Lock()
	ctx := h.ctx
	if ctx == nil || ctx.Err() != nil {
		h.mu.Unlock()
		return nil
	}
	h.unloadLocked()
	h.mu.Unlock()
	h.loads.Wait() // the previous load's commands have stopped

	list, err := h.cfg.Store.List(ctx, h.cfg.Version)
	if err != nil {
		h.log.Error("read installed plugins", "err", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	load, stop := context.WithCancel(ctx)
	h.stop = stop
	for _, p := range list {
		switch {
		case !p.Enabled:
			continue
		case p.Problem != "":
			h.log.Warn("plugin not loaded", "plugin", p.ID, "problem", p.Problem)
			continue
		}
		l := &loaded{Plugin: p, log: io.Discard, sem: make(chan struct{}, maxEventCommands)}
		if err := h.prepareConfiguration(load, l); err != nil {
			h.log.Error("plugin configuration is not ready; plugin not loaded", "plugin", p.ID, "err", err)
			l.lastErr = "configuration: " + err.Error()
			l.problem = l.lastErr
			h.plugins[p.ID] = l
			continue
		}
		if h.cfg.LogDir != "" {
			if f, err := logging.OpenRotating(filepath.Join(h.cfg.LogDir, p.ID+".log"), 1<<20, 2); err == nil {
				l.log, l.closer = f, f
			}
		}
		h.plugins[p.ID] = l
		h.log.Info("plugin loaded", "plugin", p.ID, "version", p.Manifest.Version, "startup", len(p.Manifest.Startup), "events", len(p.Manifest.Events), "actions", len(p.Manifest.Actions))
		for _, s := range p.Manifest.Startup {
			h.loads.Go(func() { h.runStartup(load, l, s) })
		}
	}
	return h.statusLocked()
}

// unloadLocked stops the current load's startup commands (they exit in
// the background) and forgets its plugins.
func (h *Host) unloadLocked() {
	if h.stop != nil {
		h.stop()
		h.stop = nil
	}
	for id, l := range h.plugins {
		if l.closer != nil {
			l.logMu.Lock()
			_ = l.closer.Close()
			l.log = io.Discard
			l.logMu.Unlock()
		}
		delete(h.plugins, id)
	}
}

// Status lists the plugins the host runs.
func (h *Host) Status() []Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked()
}

func (h *Host) statusLocked() []Status {
	out := []Status{}
	for _, l := range h.plugins {
		out = append(out, Status{Plugin: l.Plugin, Running: l.running, LastError: l.lastErr})
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// restartPause bounds how often a startup command that keeps exiting is
// started again.
const (
	restartFirst = 2 * time.Second
	restartMax   = time.Minute
)

func (h *Host) runStartup(ctx context.Context, l *loaded, s Startup) {
	pause := restartFirst
	for ctx.Err() == nil {
		h.mu.Lock()
		l.running++
		h.mu.Unlock()
		start := time.Now()
		_, err := h.run(ctx, l, s.Command, Context{Trigger: "startup"}, nil)
		h.mu.Lock()
		l.running--
		if err != nil && ctx.Err() == nil {
			l.lastErr = fmt.Sprintf("startup %s: %v", strings.Join(s.Command, " "), err)
		}
		h.mu.Unlock()
		if ctx.Err() != nil || !s.Restart {
			return
		}
		if time.Since(start) > restartMax {
			pause = restartFirst // it ran a good while: start over quickly
		}
		h.log.Warn("plugin command stopped; starting it again", "plugin", l.ID, "command", s.Command, "err", err, "in", pause)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		pause = min(pause*2, restartMax)
	}
}

// Event runs every enabled plugin's handlers for an event, in the
// background.
func (h *Host) Event(typ string, data json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop == nil {
		return
	}
	for _, l := range h.plugins {
		if l.problem != "" {
			continue
		}
		for _, e := range l.Manifest.Events {
			if !e.Matches(typ) {
				continue
			}
			c := Context{Trigger: "event", Event: &EventInfo{Type: typ, Data: data}}
			c.Environment, c.Tab, c.Pane = about(data)
			h.running.Go(func() {
				l.sem <- struct{}{}
				defer func() { <-l.sem }()
				ctx, cancel := context.WithTimeout(h.ctx, e.Timeout())
				defer cancel()
				if _, err := h.run(ctx, l, e.Command, c, nil); err != nil {
					h.mu.Lock()
					l.lastErr = fmt.Sprintf("on %s: %v", typ, err)
					h.mu.Unlock()
					h.log.Warn("plugin event command failed", "plugin", l.ID, "event", typ, "err", err)
				}
			})
		}
	}
}

// about reads which environment, tab and pane an event's data names.
func about(data json.RawMessage) (env, tab, pane string) {
	var d struct {
		ID            string `json:"id"`
		EnvironmentID string `json:"environment_id"`
		TabID         string `json:"tab_id"`
		PaneID        string `json:"pane_id"`
		Environment   *struct {
			ID string `json:"id"`
		} `json:"environment"`
	}
	if json.Unmarshal(data, &d) != nil {
		return "", "", ""
	}
	env, tab, pane = d.EnvironmentID, d.TabID, d.PaneID
	if d.Environment != nil {
		env = d.Environment.ID
	}
	return env, tab, pane
}

// ErrNoAction is returned for an action a plugin does not have.
var ErrNoAction = errors.New("no such plugin action")

// Action runs a plugin's action and returns how it went.
func (h *Host) Action(ctx context.Context, id, action string, c Context) (Result, error) {
	h.mu.Lock()
	l := h.plugins[id]
	h.mu.Unlock()
	if l == nil {
		return Result{}, fmt.Errorf("%w %q (or it is disabled; see `hive plugin list`)", ErrNotFound, id)
	}
	if l.problem != "" {
		return Result{}, fmt.Errorf("plugin %s is not ready: %s", id, l.lastErr)
	}
	a, ok := l.Manifest.Action(action)
	if !ok {
		var ids []string
		for _, x := range l.Manifest.Actions {
			ids = append(ids, x.ID)
		}
		return Result{}, fmt.Errorf("%w %q in %s (have %s)", ErrNoAction, action, id, strings.Join(ids, ", "))
	}
	ctx, cancel := context.WithTimeout(ctx, a.Timeout())
	defer cancel()
	c.Trigger, c.Action = "action", action
	var out tailBuffer
	code, err := h.run(ctx, l, a.Command, c, &out)
	res := Result{ExitCode: code, Output: out.String()}
	if err != nil && code == 0 {
		return res, err // it did not run at all
	}
	return res, nil
}

// run runs one plugin command to its end and returns its exit code. Its
// output goes to the plugin's log (and to also, when set).
func (h *Host) run(ctx context.Context, l *loaded, argv []string, c Context, also io.Writer) (int, error) {
	return h.runWithEnvironment(ctx, l, argv, c, also, nil)
}

func (h *Host) runWithEnvironment(ctx context.Context, l *loaded, argv []string, c Context, also io.Writer, extra map[string]string) (int, error) {
	c.Plugin, c.Session = l.ID, h.cfg.Session
	data := h.cfg.Store.DataDir(l.ID)
	if l.Manifest.DataDirectory != "" && l.Manifest.DataDirectory != "." {
		data = filepath.Join(data, filepath.FromSlash(l.Manifest.DataDirectory))
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return 0, err
	}
	configDir := h.cfg.Store.ConfigDir(l.ID)
	if l.Manifest.Configuration.Directory != "" && l.Manifest.Configuration.Directory != "." {
		configDir = filepath.Join(configDir, filepath.FromSlash(l.Manifest.Configuration.Directory))
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return 0, err
	}
	ctxJSON, _ := json.Marshal(c)
	ctxFile := filepath.Join(data, ".context-"+randomID()+".json")
	if err := os.WriteFile(ctxFile, ctxJSON, 0o600); err != nil {
		return 0, err
	}
	defer os.Remove(ctxFile)

	path := argv[0]
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		path = filepath.Join(l.Dir, path)
	}
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Dir = l.Dir
	vars := map[string]string{
		"HIVE_PLUGIN_ID":         l.ID,
		"HIVE_PLUGIN_DIR":        l.Dir,
		"HIVE_PLUGIN_DATA":       data,
		"HIVE_PLUGIN_CONFIG_DIR": configDir,
		"HIVE_PLUGIN_CONFIG":     filepath.Join(configDir, pluginConfigFile),
		"HIVE_PLUGIN_VERSION":    l.Manifest.Version,
		"HIVE_PLUGIN_CONTEXT":    ctxFile,
	}
	for key, value := range extra {
		vars[key] = value
	}
	cmd.Env = h.cfg.Env(vars)
	cmd.Stdin = bytes.NewReader(ctxJSON)
	var w io.Writer = pluginLog{l}
	if also != nil {
		w = io.MultiWriter(w, also)
	}
	cmd.Stdout, cmd.Stderr = w, w
	// Its own process group, so stopping it stops what it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	h.running.Add(1)
	defer h.running.Done()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	err := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // anything it left running
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), err
	}
	return 0, err
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// pluginLog writes a command's output to its plugin's log, one write at a
// time. It never fails: an unloaded plugin's log is gone, and its commands
// are being stopped anyway.
type pluginLog struct{ l *loaded }

func (w pluginLog) Write(p []byte) (int, error) {
	w.l.logMu.Lock()
	defer w.l.logMu.Unlock()
	_, _ = w.l.log.Write(p)
	return len(p), nil
}

// tailBuffer keeps the last 64 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailKeep = 64 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailKeep {
		t.buf = t.buf[len(t.buf)-tailKeep:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
