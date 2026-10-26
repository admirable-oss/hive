package config

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/branch"
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
	Tasks     Tasks
	Branches  Branches
	Workflows Workflows
	Insights  Insights
	Session   Session
	Retention Retention
	Remote    Remote
	Discovery Discovery
	Images    Images
	Plugins   Plugins
	Observers Observers
	Web       Web
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

// Tasks configures plans of tasks (hive task run) on this machine.
type Tasks struct {
	// Capacity caps the tasks running here at once, across plans.
	Capacity int
	// Labels are what tasks' labels select this machine by.
	Labels []string
	// KeepPlans is how many finished plans are kept.
	KeepPlans int
	// Kinds caps the tasks running at once per agent kind, across plans
	// ([tasks.kinds] claude = 4).
	Kinds map[string]int
}

// Branches configures the branches of the worktrees Hive makes: their
// names, their cleanup, the gates before they are ready, and the forges
// pull requests go to.
type Branches struct {
	// AgentPattern names an isolated agent's branch; TaskPattern an
	// isolated task's.
	AgentPattern string
	TaskPattern  string
	// Cleanup is merged (remove a merged branch's worktree once nothing
	// uncommitted or running is in it) or never.
	Cleanup string
	// DeleteMerged also deletes the local branch of such a worktree.
	DeleteMerged bool
	// CheckInterval is how often branches are compared when no change was
	// noticed.
	CheckInterval time.Duration
	// Keep is how many closed branches are remembered.
	Keep int
	// Gates run in a branch's worktree before it is ready
	// ([[branches.gate]]).
	Gates []Gate
	// Forges route hosts to forges ([[branches.forge]]).
	Forges []Forge
}

// Workflows configures workflows (hive workflow): where shared ones are
// read from, and how many run at once.
type Workflows struct {
	// Paths are more workflow files and directories ("~/" is home), for
	// shared ones such as a repository's .hive/workflows. They fire only
	// once trusted.
	Paths []string
	// MaxRuns caps the runs running at once, across workflows.
	MaxRuns int
	// GitInterval is how often watched repositories' references (and the
	// workflow files) are read again.
	GitInterval time.Duration
}

// Insights configures agent insights (hive insights): whether agents'
// time and usage are recorded, and the budgets on them.
type Insights struct {
	// Enabled records each agent's time in each state and the usage it
	// reports; off, nothing is recorded (what was can still be read).
	Enabled bool
	// Budgets limit cost, tokens or working time ([[insights.budget]]).
	Budgets []Budget
}

// Budget limits cost, tokens or agents' working time, for each key of
// what it applies to separately.
type Budget struct {
	Name string
	// Per is what it applies to: day, plan, environment or kind.
	Per string
	// Environment and Kind narrow what counts to one environment or agent
	// kind.
	Environment string
	Kind        string
	// Cost (US dollars), Tokens and Working are its limits; zero is none,
	// and at least one is set.
	Cost    float64
	Tokens  int64
	Working time.Duration
	// WarnAt is the share of a limit that raises a warning.
	WarnAt float64
	// Action is what an exceeded budget does: warn, or pause (hold new
	// tasks in its scope).
	Action string
}

// BudgetPers are what a budget can apply to, and BudgetActions what it can
// do once exceeded.
var (
	BudgetPers    = []string{"day", "plan", "environment", "kind"}
	BudgetActions = []string{"warn", "pause"}
)

// Gate is a check before a branch is ready.
type Gate struct {
	Name    string
	Command []string
	Timeout time.Duration
	// Repo limits it to repositories in this directory ("~/" is home).
	Repo string
}

// Forge routes a host's pull requests.
type Forge struct {
	Host    string
	Type    string // github | gitlab | command
	Command []string
}

// ForgeTypes are the kinds of forge a host can be routed to.
var ForgeTypes = []string{"github", "gitlab", "command"}

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
	// WorkflowRuns and WorkflowRunAge bound the finished workflow runs
	// kept; the oldest are forgotten first.
	WorkflowRuns   int
	WorkflowRunAge time.Duration
	// InsightsDays is how many days of insights (agents' time and usage)
	// are kept.
	InsightsDays int
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

// Discovery configures finding Hive runtimes on the local network (`hive
// machine discover`, `hive machine pair`).
type Discovery struct {
	// Advertise makes the daemon announce this machine on local networks
	// (multicast DNS). Off by default.
	Advertise bool
	// Name is the name it is announced under ("" is the host name).
	Name string
	// SSHPort is the port ssh listens on here, which the announcement
	// gives.
	SSHPort int
	// Interfaces limits announcing and looking to these network
	// interfaces (empty: every one that is up and multicast, except VPN
	// tunnels and loopback).
	Interfaces []string
	// Browse lets `hive machine discover` look for runtimes. Off, saved
	// machines and pairing by address keep working.
	Browse bool
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

// Observers configures the observer socket: read-only, scoped access for
// dashboards and notifiers (`hive observer`).
type Observers struct {
	// Enabled serves the observer socket. Off, credentials are kept but
	// nobody can observe.
	Enabled bool
	// HistoryEvents is how many events observers can catch up with after
	// a reconnect.
	HistoryEvents int
	// MaxConnections bounds open observer connections.
	MaxConnections int
}

// Web configures the web observer (`hive web serve`).
type Web struct {
	// Listen is its HTTP address.
	Listen string
	// PublicURL is where browsers reach it, for share links ("" derives
	// it from Listen).
	PublicURL string
	// TLSCert and TLSKey serve HTTPS; BehindProxy says a proxy in front
	// terminates TLS. One of them is needed to listen beyond this machine.
	TLSCert     string
	TLSKey      string
	BehindProxy bool
	// Push sends notifications to phones and browsers (Web Push).
	Push bool
	// PushContact is how push services can reach whoever runs it (a
	// mailto: or https: URL; "" uses the public URL).
	PushContact string
	// PushAllowAnyEndpoint accepts push services other than the
	// browsers' own.
	PushAllowAnyEndpoint bool
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
		Tasks:    Tasks{Capacity: 8, Labels: []string{}, KeepPlans: 100},
		Branches: Branches{
			AgentPattern: branch.DefaultAgentPattern, TaskPattern: branch.DefaultTaskPattern,
			Cleanup: "merged", CheckInterval: 30 * time.Second, Keep: branch.DefaultKeep,
		},
		Session: Session{
			Restore: true, ResumeAgentsOnRestore: true, RestoreStagger: time.Second,
			SnapshotInterval: 30 * time.Second, KeepSnapshots: 48, ReplayLines: 500,
		},
		Workflows: Workflows{Paths: []string{}, MaxRuns: 4, GitInterval: 10 * time.Second},
		Insights:  Insights{Enabled: true},
		Retention: Retention{
			ExitedProcesses: 7 * 24 * time.Hour, ProcessLogMB: 50, GCInterval: 6 * time.Hour,
			WorkflowRuns: 200, WorkflowRunAge: 30 * 24 * time.Hour, InsightsDays: 90,
		},
		Remote:    Remote{SSH: "ssh", FrameBudgetKB: 48, ImageBudgetKB: 1024},
		Discovery: Discovery{SSHPort: 22, Interfaces: []string{}, Browse: true},
		Images:    Images{MaxKB: 5120, MaxMegapixels: 16, Keep: time.Hour, Render: "auto"},
		Plugins:   Plugins{Allow: []string{}, Deny: []string{}, Isolation: "auto", RestartLimit: 5, RestartWindow: 10 * time.Minute, Hidden: []string{}},
		Observers: Observers{Enabled: true, HistoryEvents: 2048, MaxConnections: 32},
		Web:       Web{Listen: "127.0.0.1:8484", Push: true},
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
