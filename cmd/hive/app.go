package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/config"
	"github.com/admirable-oss/hive/internal/daemonctl"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/session"
)

// app is everything a command needs. execute builds it once (the CLI's
// composition root) and passes it down, so commands never reach for globals.
type app struct {
	home       string // the user's home directory
	base       string // every session's data: $HIVE_HOME or ~/.hive
	session    string // the session commands talk to
	root       string // the session's storage root (base for the default session)
	socket     string // daemon socket inside root
	logDir     string // daemon logs
	configPath string

	cfg      config.Config
	warnings []string // config problems, reported once per command
	cfgErr   error    // a config file that could not be parsed at all

	getenv func(string) string
	client client.Client
	out    io.Writer
	errOut io.Writer
	json   bool // --json: machine-readable output
}

func newApp(getenv func(string) string, stdout, stderr io.Writer) (*app, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate home directory: %w", err)
	}
	base := getenv("HIVE_HOME")
	if base == "" {
		base = filepath.Join(home, ".hive")
	}
	a := &app{
		home:       home,
		base:       base,
		configPath: config.ResolvePath(getenv, home),
		getenv:     getenv,
		out:        stdout,
		errOut:     stderr,
	}
	if err := a.useSession(getenv(session.EnvVar)); err != nil {
		return nil, fmt.Errorf("%s: %w", session.EnvVar, err)
	}

	cfg, warnings, err := config.Load(a.configPath)
	cfg, envWarnings := config.ApplyEnv(cfg, getenv)
	warnings = append(warnings, envWarnings...)
	a.cfg, a.warnings, a.cfgErr = cfg, warnings, err
	return a, nil
}

// useSession points the app at session name ("" is the default session).
func (a *app) useSession(name string) error {
	root, err := session.Root(a.base, name)
	if err != nil {
		return err
	}
	if a.client != nil {
		_ = a.client.Close()
	}
	a.session = session.Normalize(name)
	a.root = root
	a.socket = platform.SocketPath(root, "hive.sock")
	a.logDir = filepath.Join(root, "logs")
	a.client = client.NewService(client.Config{SocketPath: a.socket})
	return nil
}

// sessionArgs is the --session flag that selects a.session in a child hive.
func (a *app) sessionArgs() []string {
	if a.session == session.Default {
		return nil
	}
	return []string{"--session", a.session}
}

func (a *app) daemonLog() string    { return filepath.Join(a.logDir, "daemon.log") }
func (a *app) daemonStderr() string { return filepath.Join(a.logDir, "daemon.stderr") }

// reportConfig prints config warnings to stderr. A config that cannot be
// parsed is only fatal for the daemon; clients carry on with defaults.
func (a *app) reportConfig() {
	if a.cfgErr != nil {
		fmt.Fprintf(a.errOut, "hive: warning: %v (using defaults)\n", a.cfgErr)
	}
	for _, w := range a.warnings {
		fmt.Fprintf(a.errOut, "hive: warning: config: %s\n", w)
	}
}

// ensureDaemon makes sure a daemon answers, starting one when autostart is
// enabled. Commands that only make sense against a running daemon use it.
func (a *app) ensureDaemon(ctx context.Context) error {
	err := a.client.Ping(ctx)
	if err == nil {
		return a.ensureCurrentDaemon(ctx)
	}
	if !errors.Is(err, client.ErrUnavailable) {
		return err
	}
	if !a.cfg.Daemon.Autostart {
		return fmt.Errorf("the hive daemon is not running; start it with `hive daemon start` (autostart is off in %s)", a.configPath)
	}
	if err := a.startDaemon(ctx); err != nil {
		return err
	}
	fmt.Fprintf(a.errOut, "hive: started the daemon (logs: %s)\n", a.daemonLog())
	return nil
}

// ensureCurrentDaemon replaces a running daemon older than this build (one
// started before an upgrade, or by an older checkout), which would answer
// requests it no longer understands. It does so only when no agent can be
// lost: when its agents outlive restarts (shims) or it runs none.
// Otherwise it explains and leaves the daemon alone.
func (a *app) ensureCurrentDaemon(ctx context.Context) error {
	st, err := a.client.Status(ctx)
	if err != nil || st.APILevel >= protocol.APILevel {
		return nil // current, or it cannot say: let the command try
	}
	who := fmt.Sprintf("the running hive daemon (pid %d, %s)", st.PID, st.Version)
	if !a.cfg.Daemon.Autostart {
		return fmt.Errorf("%s is older than this hive; restart it with `hive daemon restart`", who)
	}
	if !st.AgentsSurviveRestart {
		n, err := a.runningAgents(ctx)
		switch {
		case err != nil:
			return fmt.Errorf("%s is older than this hive, and its agents would not survive a restart; stop them, then run `hive daemon restart`", who)
		case n > 0:
			return fmt.Errorf("%s is older than this hive, and restarting it would stop its %d running agent(s), which it does not keep alive across restarts; stop them, then run `hive daemon restart`", who, n)
		}
	}
	fmt.Fprintf(a.errOut, "hive: %s is older than this hive; replacing it (agents keep running)\n", who)
	if err := a.shutdownDaemon(ctx, false); err != nil {
		return fmt.Errorf("stop %s: %w", who, err)
	}
	return a.startDaemon(ctx)
}

// runningAgents counts the daemon's running agents, through calls every
// daemon version answers.
func (a *app) runningAgents(ctx context.Context) (int, error) {
	envs, err := a.client.EnvironmentList(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range envs {
		procs, err := a.client.ProcessList(ctx, e.ID)
		if err != nil {
			return 0, err
		}
		for _, p := range procs {
			if p.Active() {
				n++
			}
		}
	}
	return n, nil
}

// startDaemon launches `hive daemon` detached and waits until it answers.
func (a *app) startDaemon(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the hive binary: %w", err)
	}
	if exe, err = a.stableExecutable(exe); err != nil {
		return err
	}
	return daemonctl.Spawn(ctx, daemonctl.SpawnOptions{
		Executable: exe,
		Args:       append(a.sessionArgs(), "daemon"),
		StderrPath: a.daemonStderr(),
		LogPath:    a.daemonLog(),
		Ready:      a.client.Ping,
		Timeout:    10 * time.Second,
	})
}

// daemonRunning reports whether a daemon answers right now, without starting one.
func (a *app) daemonRunning(ctx context.Context) (client.Status, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st, err := a.client.Status(ctx)
	return st, err == nil
}

func (a *app) table() *tabwriter.Writer {
	return tabwriter.NewWriter(a.out, 0, 0, 3, ' ', 0)
}

// printJSON writes v as indented JSON (the --json output format).
func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// emit prints v as JSON under --json, and calls human otherwise.
func (a *app) emit(v any, human func() error) error {
	if a.json {
		return a.printJSON(v)
	}
	return human()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
