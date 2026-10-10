package config

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/notify"
)

// Config is the effective configuration. Zero values are never meaningful;
// always start from Defaults.
type Config struct {
	Daemon    Daemon
	Log       Log
	Process   Process
	Terminal  Terminal
	Git       Git
	Worktrees Worktrees
	Session   Session
	Retention Retention
	Notify    notify.Config
	UI        UI
	Theme     Theme
	Keys      Keys
}

type Daemon struct {
	// Autostart lets client commands start the daemon when it is not running.
	Autostart bool
	// ShutdownTimeout bounds how long a stop waits for agents to exit.
	ShutdownTimeout time.Duration
}

type Log struct {
	Level      string // debug | info | warn | error
	Format     string // text | json
	MaxSizeMB  int
	MaxBackups int
}

type Process struct {
	// StopGrace is the SIGTERM → SIGKILL delay when stopping an agent.
	StopGrace time.Duration
}

type Terminal struct {
	// DefaultWidth and DefaultHeight size a new agent terminal when the
	// caller does not.
	DefaultWidth  int
	DefaultHeight int
	// ScrollbackMB bounds each agent's scrollback (lines that scrolled off
	// its screen). 0 keeps none.
	ScrollbackMB int
	// Shell is the command line a new pane runs when none is given, split
	// on spaces. Empty means $SHELL as a login shell.
	Shell string
}

type Git struct {
	// RefreshInterval is how often environments' git status is re-read,
	// besides changes noticed in the repository itself.
	RefreshInterval time.Duration
}

type Worktrees struct {
	// Directory holds worktrees created by `hive worktree create`, as
	// <directory>/<repo>/<branch>. Empty means <HIVE_HOME>/worktrees.
	Directory string
}

// Session configures snapshots of the workspace and its restore after a
// reboot (see package persist).
type Session struct {
	// Restore starts panes again, where they were working, when the daemon
	// finds their processes gone with the host or their shims.
	Restore bool
	// ResumeAgentsOnRestore resumes restored agents' conversations (their
	// manifests' [resume]); without it they start fresh.
	ResumeAgentsOnRestore bool
	// RestoreStagger separates restored agents' starts.
	RestoreStagger time.Duration
	// SnapshotInterval is how often a snapshot is taken besides after
	// changes (for directories changed by `cd`).
	SnapshotInterval time.Duration
	// KeepSnapshots is how many earlier snapshots are kept.
	KeepSnapshots int
	// ReplayHistory keeps each pane's recent text and shows it again in a
	// restored pane. Off by default: the text may hold secrets.
	ReplayHistory bool
	// ReplayLines bounds the text kept per pane.
	ReplayLines int
}

// Retention bounds what Hive keeps on disk (see `hive gc`).
type Retention struct {
	// ExitedProcesses is how long the records and output of processes that
	// ended (and no pane shows) are kept.
	ExitedProcesses time.Duration
	// ProcessLogMB rotates each agent's output log at this size, keeping
	// one older file; 0 lets logs grow.
	ProcessLogMB int
	// GCInterval is how often the daemon cleans up by itself; 0 never.
	GCInterval time.Duration
}

// UI configures the multiplexer (`hive ui`).
type UI struct {
	// Sidebar shows the sidebar when the UI opens.
	Sidebar      bool
	SidebarWidth int
	// Mouse lets the UI take the mouse (focus, drag borders, select).
	Mouse bool
	// Clipboard is where copied text goes: auto | osc52 | local | off.
	Clipboard string
}

// Clipboard modes.
var clipboardModes = []string{"auto", "osc52", "local", "off"}

// Theme picks the UI's colours.
type Theme struct {
	// Name is auto, a built-in theme, or custom. The UI checks it.
	Name string
	// Custom is the [theme.custom] table: base and colour overrides.
	Custom map[string]string
}

// Keys are the UI's key bindings. Mode and action names are checked by the
// UI, which owns them; this package only reads the shapes.
type Keys struct {
	// Prefix lists the prefix keys.
	Prefix []string
	// Modes holds [keys.<mode>] tables: action → keys. An empty list
	// unbinds the action.
	Modes map[string]map[string][]string
}

// KeyModes are the input modes a [keys.<mode>] table may name.
var KeyModes = []string{"terminal", "prefix", "navigate", "resize", "copy"}

// DefaultPrefix is the prefix key unless configured.
const DefaultPrefix = "ctrl+b"

// Defaults returns the built-in configuration.
func Defaults() Config {
	return Config{
		Daemon:   Daemon{Autostart: true, ShutdownTimeout: 15 * time.Second},
		Log:      Log{Level: "info", Format: "text", MaxSizeMB: 10, MaxBackups: 3},
		Process:  Process{StopGrace: 3 * time.Second},
		Terminal: Terminal{DefaultWidth: 220, DefaultHeight: 50, ScrollbackMB: 10},
		Git:      Git{RefreshInterval: 5 * time.Second},
		Session: Session{
			Restore: true, ResumeAgentsOnRestore: true, RestoreStagger: time.Second,
			SnapshotInterval: 30 * time.Second, KeepSnapshots: 48, ReplayLines: 500,
		},
		Retention: Retention{ExitedProcesses: 7 * 24 * time.Hour, ProcessLogMB: 50, GCInterval: 6 * time.Hour},
		Notify:    notify.Defaults(),
		UI:        UI{Sidebar: true, SidebarWidth: 28, Mouse: true, Clipboard: "auto"},
		Theme:     Theme{Name: "auto"},
		Keys:      Keys{Prefix: []string{DefaultPrefix}},
	}
}

// ShellArgv is Terminal.Shell as an argv (nil for the default).
func (c Config) ShellArgv() []string {
	if c.Terminal.Shell == "" {
		return nil
	}
	return strings.Fields(c.Terminal.Shell)
}

// WorktreeDir resolves Worktrees.Directory: "~/" is the home directory,
// empty is base/worktrees.
func (c Config) WorktreeDir(home, base string) string {
	d := c.Worktrees.Directory
	switch {
	case d == "":
		return filepath.Join(base, "worktrees")
	case d == "~":
		return home
	case strings.HasPrefix(d, "~/"):
		return filepath.Join(home, d[2:])
	}
	return d
}

// LoggingConfig converts the [log] section for package logging. The level
// and format are validated during loading, so parsing cannot fail here.
func (c Config) LoggingConfig(path string, stderr bool) logging.Config {
	level, _ := logging.ParseLevel(c.Log.Level)
	format, _ := logging.ParseFormat(c.Log.Format)
	return logging.Config{
		Level:        level,
		Format:       format,
		Path:         path,
		MaxSizeBytes: int64(c.Log.MaxSizeMB) << 20,
		MaxBackups:   c.Log.MaxBackups,
		Stderr:       stderr,
	}
}
