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
	Remote    Remote
	Images    Images
	Plugins   Plugins
	Update    Update
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

// Update configures `hive update`.
type Update struct {
	// Channel is stable (releases) or preview (pre-releases too).
	Channel string
}

// Remote configures connections to Hive on other machines (`hive
// --remote`, `hive machine`).
type Remote struct {
	// SSH is the ssh command line, split on spaces ($HIVE_SSH overrides).
	SSH string
	// FrameBudgetKB bounds what a remote daemon sends this client of
	// agents' screens, in KiB per second (0: no limit).
	FrameBudgetKB int
	// ImageBudgetKB bounds how fast this client sends a pasted image to a
	// remote daemon, in KiB per second (0: no limit), so typing stays
	// responsive meanwhile.
	ImageBudgetKB int
}

// Images bounds pasted images (prefix I) and what is kept of them.
type Images struct {
	// MaxKB is the largest image accepted, in KiB: read from the clipboard
	// by the UI, and received by the daemon.
	MaxKB int
	// MaxMegapixels bounds an image's pixel count.
	MaxMegapixels int
	// Keep is how long the daemon keeps images it saved for agents that
	// take an image as a file path; 0 turns saving off.
	Keep time.Duration
	// LogGraphics keeps images agents print (Kitty graphics) in their
	// output logs; off strips them, so no image data is written to disk.
	LogGraphics bool
	// Render is how images agents print are drawn: auto (the terminal's
	// graphics protocol, else coloured cells) | cells | text | off.
	Render string
}

// Plugins is the user's policy for every plugin.
type Plugins struct {
	// Allow grants these capabilities to plugins that list them as
	// optional, without asking; Deny withholds them from every plugin
	// (one that requires a denied capability does not run).
	Allow []string
	Deny  []string
	// Isolation is auto (strict for plugins that support it), standard
	// or strict (every plugin, sandboxed).
	Isolation string
	// RestartLimit and RestartWindow stop restarting a plugin command
	// that fails more than RestartLimit times within RestartWindow.
	RestartLimit  int
	RestartWindow time.Duration
	// Hidden turns off contributions: "plugin", "plugin:kind" or
	// "plugin:kind:id" (`hive plugin contributions` lists them).
	Hidden []string
}

// Plugin isolation modes.
var pluginIsolations = []string{"auto", "standard", "strict"}

// Image render modes.
var imageRenderModes = []string{"auto", "cells", "text", "off"}

// SSHArgv is Remote.SSH as an argv (nil for the default).
func (c Config) SSHArgv() []string { return strings.Fields(c.Remote.SSH) }

// UI configures the multiplexer (`hive ui`).
type UI struct {
	// Sidebar shows the sidebar when the UI opens.
	Sidebar      bool
	SidebarWidth int
	// Mouse lets the UI take the mouse (focus, drag borders, select).
	Mouse bool
	// Clipboard is where copied text goes: auto | osc52 | local | off.
	Clipboard string
	// StatusRight is shown on the right of the tab bar ([[ui.status_right]]).
	StatusRight []StatusSegment
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
	// Commands are the user's own commands ([[keys.command]]).
	Commands []KeyCommand
}

// KeyCommand binds keys to a command of the user's.
type KeyCommand struct {
	Keys []string
	// Mode is where the keys work: prefix (default), terminal or navigate.
	Mode string
	// Type is what runs: popup (a command in a popup over the tab), pane
	// (a command in a new pane beside the focused one), shell (a command
	// in the background, its last line shown) or plugin_action.
	Type string
	// Command is the command line (popup, pane, shell), run by sh -c.
	Command string
	// Plugin and Action name a plugin's action (plugin_action).
	Plugin, Action string
	// Title names it in the help and in messages.
	Title string
}

// Key command types.
var KeyCommandTypes = []string{"popup", "pane", "shell", "plugin_action"}

// StatusSegment is one item on the right of the tab bar.
type StatusSegment struct {
	// Type is text, datetime, hostname or command.
	Type string
	// Text is shown as is (text).
	Text string
	// Format is the datetime's layout in strftime style (%H:%M, the
	// default).
	Format string
	// Command runs by sh -c every Interval (default 30s); its output's
	// first line is shown.
	Command  string
	Interval time.Duration
}

// StatusTypes are the kinds of status segment.
var StatusTypes = []string{"text", "datetime", "hostname", "command"}

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
		Remote:    Remote{SSH: "ssh", FrameBudgetKB: 48, ImageBudgetKB: 1024},
		Images:    Images{MaxKB: 5120, MaxMegapixels: 16, Keep: time.Hour, Render: "auto"},
		Plugins:   Plugins{Allow: []string{}, Deny: []string{}, Isolation: "auto", RestartLimit: 5, RestartWindow: 10 * time.Minute, Hidden: []string{}},
		Update:    Update{Channel: "stable"},
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
