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
)

// app is everything a command needs. execute builds it once (the CLI's
// composition root) and passes it down, so commands never reach for globals.
type app struct {
	home       string // the user's home directory
	root       string // storage root, e.g. ~/.hive
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
	root := getenv("HIVE_HOME")
	if root == "" {
		root = filepath.Join(home, ".hive")
	}
	a := &app{
		home:       home,
		root:       root,
		socket:     filepath.Join(root, "hive.sock"),
		logDir:     filepath.Join(root, "logs"),
		configPath: config.ResolvePath(getenv, home),
		getenv:     getenv,
		out:        stdout,
		errOut:     stderr,
	}
	a.client = client.NewService(client.Config{SocketPath: a.socket})

	cfg, warnings, err := config.Load(a.configPath)
	cfg, envWarnings := config.ApplyEnv(cfg, getenv)
	warnings = append(warnings, envWarnings...)
	a.cfg, a.warnings, a.cfgErr = cfg, warnings, err
	return a, nil
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
	if err == nil || !errors.Is(err, client.ErrUnavailable) {
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

// startDaemon launches `hive daemon` detached and waits until it answers.
func (a *app) startDaemon(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the hive binary: %w", err)
	}
	return daemonctl.Spawn(ctx, daemonctl.SpawnOptions{
		Executable: exe,
		Args:       []string{"daemon"},
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
