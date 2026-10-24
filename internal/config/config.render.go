package config

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"
	"time"
)

// Render writes cfg as a commented TOML document that Parse reads back to
// the same Config. `hive config default` is Render(Defaults()).
func Render(cfg Config) []byte {
	var b bytes.Buffer
	if err := renderTmpl.Execute(&b, cfg); err != nil {
		panic(fmt.Sprintf("config: render template: %v", err)) // the template is static
	}
	return b.Bytes()
}

var renderTmpl = template.Must(template.New("config").Funcs(template.FuncMap{
	"dur":          func(d time.Duration) string { return tomlString(d.String()) },
	"q":            tomlString,
	"theme":        renderCustomTheme,
	"keys":         RenderKeys,
	"list":         tomlList,
	"notifyAgents": renderNotifyAgents,
	"taskKinds":    renderTaskKinds,
	"gates":        renderGates,
	"forges":       renderForges,
	"keyCommands":  renderKeyCommands,
	"status":       renderStatus,
}).Parse(`# Hive configuration. Every key is optional; omitted keys use the defaults.
# Unknown keys and invalid values are reported as warnings and ignored.

[daemon]
# Start the daemon automatically when a command needs it.
autostart = {{.Daemon.Autostart}}
# How long stopping the daemon waits for agents to exit.
shutdown_timeout = {{dur .Daemon.ShutdownTimeout}}

[log]
# debug | info | warn | error  (HIVE_LOG overrides this)
level = {{q .Log.Level}}
# text | json
format = {{q .Log.Format}}
# The daemon log rotates at this size and keeps this many old files.
max_size_mb = {{.Log.MaxSizeMB}}
max_backups = {{.Log.MaxBackups}}

[process]
# How long an agent gets to exit after SIGTERM before it is killed.
stop_grace = {{dur .Process.StopGrace}}

[terminal]
# Size of a new agent terminal when the caller does not choose one.
default_width = {{.Terminal.DefaultWidth}}
default_height = {{.Terminal.DefaultHeight}}
# Scrollback kept per agent, in MiB (lines that scrolled off its screen).
scrollback_mb = {{.Terminal.ScrollbackMB}}
# What a new pane runs when no command is given ("" means $SHELL -l).
shell = {{q .Terminal.Shell}}

[git]
# How often environments' branch and dirty state are re-read (changes to the
# repository itself, like commits and checkouts, are noticed at once).
refresh_interval = {{dur .Git.RefreshInterval}}

[session]
# When the host reboots (or an agent's shim dies), start its panes again where
# they were working, with their tabs, layout and focus.
restore = {{.Session.Restore}}
# Restored agents continue their conversations (` + "`claude --resume <id>`" + `,
# ` + "`codex resume <id>`" + `); off, they start fresh.
resume_agents_on_restore = {{.Session.ResumeAgentsOnRestore}}
# Pause between restored agents' starts.
restore_stagger = {{dur .Session.RestoreStagger}}
# The workspace is saved to session.json after every change and on this timer.
snapshot_interval = {{dur .Session.SnapshotInterval}}
# Earlier snapshots kept in snapshots/.
keep_snapshots = {{.Session.KeepSnapshots}}
# Keep each pane's recent text and show it again when the pane is restored.
# The text is whatever programs printed, secrets included; it is kept in
# history/ (private to you) only while this is on.
replay_history = {{.Session.ReplayHistory}}
# How many recent lines of each pane are kept for that.
replay_lines = {{.Session.ReplayLines}}

[retention]
# Records and output of processes that ended, and that no pane shows, are
# removed after this long (` + "`hive gc`" + ` removes them now).
exited_processes = {{dur .Retention.ExitedProcesses}}
# Each agent's output log rotates at this size in MiB, keeping one older file
# (0 lets it grow).
process_log_mb = {{.Retention.ProcessLogMB}}
# How often the daemon cleans up by itself ("0s": never).
gc_interval = {{dur .Retention.GCInterval}}
# Finished workflow runs kept for ` + "`hive workflow runs`" + ` and reports, and for how
# long; the oldest are forgotten first.
workflow_runs = {{.Retention.WorkflowRuns}}
workflow_run_age = {{dur .Retention.WorkflowRunAge}}

[update]
# The releases ` + "`hive update`" + ` installs: stable, or preview (release candidates too).
channel = {{q .Update.Channel}}

[remote]
# The ssh command that reaches other machines (` + "`hive --remote`" + `, ` + "`hive machine`" + `);
# ~/.ssh/config applies. HIVE_SSH overrides it.
ssh = {{q .Remote.SSH}}
# How much of agents' screens a remote daemon sends this client, in KiB per
# second (0: no limit). Screens update less often when it is reached.
frame_budget_kb = {{.Remote.FrameBudgetKB}}
# How fast a pasted image is sent to a remote daemon, in KiB per second (0: no
# limit). Keys typed meanwhile go between its pieces.
image_budget_kb = {{.Remote.ImageBudgetKB}}

[discovery]
# Announce this machine on local networks (multicast DNS), so
# ` + "`hive machine discover`" + ` finds it elsewhere. The announcement has a name,
# an identity, the ssh port and Hive's API level, nothing else; it opens no port
# and grants nothing. Pairing still needs ssh access and your confirmation.
advertise = {{.Discovery.Advertise}}
# The name it is announced under ("" is the host name).
name = {{q .Discovery.Name}}
# The port ssh listens on here, which the announcement gives.
ssh_port = {{.Discovery.SSHPort}}
# Network interfaces to announce and look on (empty: every one that is up and
# multicast, except VPN tunnels and loopback).
interfaces = {{list .Discovery.Interfaces}}
# Let ` + "`hive machine discover`" + ` look for runtimes. Off, saved machines and
# pairing by address keep working.
browse = {{.Discovery.Browse}}

[images]
# The largest image pasted with prefix I, in KiB. The UI reads no more from the
# clipboard, and the daemon (with its own config) accepts no more.
max_kb = {{.Images.MaxKB}}
# The most pixels a pasted image may have, in megapixels.
max_megapixels = {{.Images.MaxMegapixels}}
# Agents that take an image as a file (Claude Code, Codex) get it saved in a
# private directory and its path pasted; the daemon removes it after this long
# ("0s": never save, so such agents refuse images).
keep = {{dur .Images.Keep}}
# Keep images that agents print (Kitty graphics) in their output logs. Off
# strips them, so image data is never written to disk.
log_graphics = {{.Images.LogGraphics}}
# How images agents print are drawn: auto (the terminal's graphics protocol:
# Kitty, iTerm2 or Sixel, else coloured cells) | cells | text (a placeholder
# naming the image) | off
render = {{q .Images.Render}}

[plugins]
# Capabilities granted, without asking, to plugins that list them as optional
# (hive.read, hive.write, hive.input, hive.process, hive.admin,
# filesystem.write, network, process.exec).
allow = {{list .Plugins.Allow}}
# Capabilities no plugin gets; one that requires a denied capability does not
# run.
deny = {{list .Plugins.Deny}}
# auto (sandbox plugins that declare isolation = "strict") | standard (never)
# | strict (every plugin; needs sandbox-exec on macOS, bwrap on Linux)
isolation = {{q .Plugins.Isolation}}
# A plugin command that fails more than restart_limit times within
# restart_window is no longer restarted (` + "`hive plugin reload <id>`" + ` resets it).
restart_limit = {{.Plugins.RestartLimit}}
restart_window = {{dur .Plugins.RestartWindow}}
# Contributions to turn off: "plugin", "plugin:kind" or "plugin:kind:id"
# (` + "`hive plugin contributions`" + ` lists them).
hidden = {{list .Plugins.Hidden}}

[observers]
# Serve the observer socket: read-only access for dashboards and notifiers
# holding a credential (` + "`hive observer create`" + `). Off, nobody can observe.
enabled = {{.Observers.Enabled}}
# Events kept for observers to catch up with after a reconnect.
history_events = {{.Observers.HistoryEvents}}
# Open observer connections allowed, all credentials together.
max_connections = {{.Observers.MaxConnections}}

[web]
# The web observer (` + "`hive web serve`" + `): read-only agent status in a browser.
# Its HTTP address. Beyond this machine it needs TLS or a proxy that terminates it.
listen = {{q .Web.Listen}}
# Where browsers reach it, for share links ("" derives it from listen).
public_url = {{q .Web.PublicURL}}
# A certificate and key to serve HTTPS.
tls_cert = {{q .Web.TLSCert}}
tls_key = {{q .Web.TLSKey}}
# A reverse proxy in front terminates TLS (cookies are then Secure).
behind_proxy = {{.Web.BehindProxy}}
# Notifications on phones and browsers that turn them on (Web Push; they need
# HTTPS, or localhost).
push = {{.Web.Push}}
# How push services can reach you, if they must (mailto: or https:; "" uses
# the public URL).
push_contact = {{q .Web.PushContact}}
# Accept push services other than the browsers' own (Google, Mozilla, Apple,
# Microsoft).
push_allow_any_endpoint = {{.Web.PushAllowAnyEndpoint}}

[worktrees]
# Where ` + "`hive worktree create`" + ` puts checkouts, as <directory>/<repo>/<branch>
# ("" means ~/.hive/worktrees, or $HIVE_HOME/worktrees).
directory = {{q .Worktrees.Directory}}

[tasks]
# Tasks of plans (` + "`hive task run`" + `) running on this machine at once, across
# plans. Plans on other machines place tasks here up to it too.
capacity = {{.Tasks.Capacity}}
# Labels this machine offers: a task with labels = ["gpu"] runs only on a
# machine that has them all (saved machines get theirs with ` + "`hive machine set`" + `).
labels = {{list .Tasks.Labels}}
# Finished plans kept for ` + "`hive task list`" + ` and reports; the oldest are
# forgotten first.
keep_plans = {{.Tasks.KeepPlans}}
{{taskKinds .Tasks.Kinds}}
[branches]
# The branch of an agent started with ` + "`--isolate`" + `: {kind}, {name}, {env}, {date}
# and {id} (6 random hex digits).
agent_pattern = {{q .Branches.AgentPattern}}
# The branch of an isolated task: {plan}, {task}, {kind}, {date}, {id}.
task_pattern = {{q .Branches.TaskPattern}}
# A merged branch's worktree: "merged" removes it once nothing uncommitted
# and no running process is in it; "never" leaves it to you.
cleanup = {{q .Branches.Cleanup}}
# Also delete the local branch of a worktree removed that way, when git
# agrees it has merged.
delete_merged = {{.Branches.DeleteMerged}}
# How often branches are compared again when no change was noticed.
check_interval = {{dur .Branches.CheckInterval}}
# Cleaned-up and removed branches remembered for ` + "`hive branch list --all`" + `.
keep = {{.Branches.Keep}}
{{gates .Branches.Gates}}{{forges .Branches.Forges}}
[workflows]
# Your own workflows are the *.toml files in ~/.hive/workflows. These are
# more files and directories ("~/" is home), such as a repository's
# .hive/workflows: shared files, whose triggers fire only once you trust
# them (` + "`hive workflow trust <name>`" + `), and again after each change.
paths = {{list .Workflows.Paths}}
# Workflow runs running at once, across workflows; the rest wait.
max_runs = {{.Workflows.MaxRuns}}
# How often watched repositories (for git triggers) and workflow files are
# read again, besides changes noticed.
git_interval = {{dur .Workflows.GitInterval}}
[notify]
# When an agent wants you: blocked (waiting for your decision) or done
# (finished, and you have not looked yet). Never for the pane you are in.
on = {{list .Notify.On}}
# A toast in the UI.
toast = {{.Notify.Toast}}
# Ask your terminal to notify: auto (OSC 9 where supported) | osc9 | osc777 |
# off. This works over SSH.
terminal = {{q .Notify.Terminal}}
# The desktop's notifications (osascript, notify-send); never over SSH.
system = {{.Notify.System}}
# A sound with each notification.
sound = {{.Notify.Sound}}
{{notifyAgents .Notify.Agents}}
[ui]
# Show the sidebar (environments and agents) when ` + "`hive ui`" + ` opens; prefix b
# toggles it. Screens 64 columns wide or less always open without it.
sidebar = {{.UI.Sidebar}}
# The sidebar's width, in columns.
sidebar_width = {{.UI.SidebarWidth}}
# Let the UI use the mouse: click to focus, drag borders, select to copy, scroll
# into history. Hold shift to select with the terminal's own mouse instead.
mouse = {{.UI.Mouse}}
# Where copied text goes: auto (the terminal through OSC 52, and the system
# clipboard when not over SSH) | osc52 | local | off
clipboard = {{q .UI.Clipboard}}
{{status .UI.StatusRight}}

[theme]
# auto (hive or hive-light, from the terminal's background) | hive (amber on
# graphite, as the desktop app) | hive-light | catppuccin | catppuccin-latte |
# tokyo-night | gruvbox | nord | terminal (the terminal's own colours) | custom
name = {{q .Theme.Name}}
{{theme .Theme.Custom}}
{{keys .Keys}}{{keyCommands .Keys.Commands}}`))

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlKey writes a key bare when TOML allows it, else quoted.
func tomlKey(k string) string {
	bare := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
	}
	for _, r := range k {
		if !bare(r) {
			return tomlString(k)
		}
	}
	if k == "" {
		return `""`
	}
	return k
}

// tomlList writes strings as a TOML array.
func tomlList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = tomlString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func renderCustomTheme(custom map[string]string) string {
	if len(custom) == 0 {
		return `# With name = "custom", [theme.custom] starts from a built-in theme ("base")
# and overrides colours: fg bg muted border accent selection success warning
# error info, each "#rrggbb", an ANSI index 0-255, or "default".
# [theme.custom]
# base = "nord"
# accent = "#ff79c6"
`
	}
	var b strings.Builder
	b.WriteString("\n[theme.custom]\n")
	for _, k := range slices.Sorted(maps.Keys(custom)) {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlString(custom[k]))
	}
	return b.String()
}

func renderNotifyAgents(agents map[string][]string) string {
	if len(agents) == 0 {
		return `# Per agent (by manifest ID, see ` + "`hive agent manifests`" + `), which states notify:
# [notify.agents]
# codex = ["blocked"]
# aider = []
`
	}
	var b strings.Builder
	b.WriteString("\n[notify.agents]\n")
	for _, id := range slices.Sorted(maps.Keys(agents)) {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(id), tomlList(agents[id]))
	}
	return b.String()
}

func renderTaskKinds(kinds map[string]int) string {
	if len(kinds) == 0 {
		return `# Per agent kind, the most tasks running at once across plans:
# [tasks.kinds]
# claude = 4
`
	}
	var b strings.Builder
	b.WriteString("\n[tasks.kinds]\n")
	for _, id := range slices.Sorted(maps.Keys(kinds)) {
		fmt.Fprintf(&b, "%s = %d\n", tomlKey(id), kinds[id])
	}
	return b.String()
}

func renderGates(gates []Gate) string {
	if len(gates) == 0 {
		return `# Gates a branch passes before it is ready (` + "`hive branch ready`" + `), run in
# its worktree in order:
# [[branches.gate]]
# name = "test"
# command = ["go", "test", "./..."]
# timeout = "20m"                  # default 30m
# repo = "~/src/api"               # only this repository's branches
`
	}
	var b strings.Builder
	for _, g := range gates {
		fmt.Fprintf(&b, "\n[[branches.gate]]\nname = %s\ncommand = %s\n", tomlString(g.Name), tomlList(g.Command))
		if g.Timeout > 0 {
			fmt.Fprintf(&b, "timeout = %s\n", tomlString(g.Timeout.String()))
		}
		if g.Repo != "" {
			fmt.Fprintf(&b, "repo = %s\n", tomlString(g.Repo))
		}
	}
	return b.String()
}

func renderForges(forges []Forge) string {
	if len(forges) == 0 {
		return `
# Pull requests go to GitHub (through gh) or GitLab (glab) by the remote's
# host. Other hosts:
# [[branches.forge]]
# host = "git.example.com"
# type = "gitlab"                  # github | gitlab | command
# command = ["my-pr"]              # for type = "command": gets the request as JSON
`
	}
	var b strings.Builder
	for _, f := range forges {
		fmt.Fprintf(&b, "\n[[branches.forge]]\nhost = %s\ntype = %s\n", tomlString(f.Host), tomlString(f.Type))
		if len(f.Command) > 0 {
			fmt.Fprintf(&b, "command = %s\n", tomlList(f.Command))
		}
	}
	return b.String()
}

// RenderKeys writes the [keys] section; `hive config reset-keys` puts the
// default one back.
func RenderKeys(k Keys) string {
	var b strings.Builder
	b.WriteString(`[keys]
# The prefix key(s): press one, then a key bound in prefix mode (prefix ? lists
# every binding). Several are allowed, e.g. ["ctrl+b", "ctrl+a"].
prefix_keys = ` + tomlList(k.Prefix) + `
# Change bindings per mode (terminal, prefix, navigate, resize, copy): an
# action takes a key or a list of keys, and [] unbinds it. Bindings in
# [keys.terminal] work without the prefix.
`)
	if len(k.Modes) == 0 {
		b.WriteString(`# [keys.prefix]
# split_right = ["|", "%"]
# zoom = []
# [keys.terminal]
# focus_left = "alt+h"
`)
		return b.String()
	}
	for _, mode := range KeyModes {
		actions := k.Modes[mode]
		if len(actions) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[keys.%s]\n", mode)
		for _, a := range slices.Sorted(maps.Keys(actions)) {
			fmt.Fprintf(&b, "%s = %s\n", tomlKey(a), tomlList(actions[a]))
		}
	}
	return b.String()
}

func renderStatus(segs []StatusSegment) string {
	if len(segs) == 0 {
		return `# The right of the tab bar: text, datetime (format in strftime style, %H:%M by
# default), hostname, or a command's first line every interval.
# [[ui.status_right]]
# type = "command"
# command = "git -C ~/src/app branch --show-current"
# interval = "30s"
# [[ui.status_right]]
# type = "datetime"
# format = "%a %H:%M"
`
	}
	var b strings.Builder
	for _, s := range segs {
		fmt.Fprintf(&b, "\n[[ui.status_right]]\ntype = %s\n", tomlString(s.Type))
		if s.Text != "" {
			fmt.Fprintf(&b, "text = %s\n", tomlString(s.Text))
		}
		if s.Format != "" {
			fmt.Fprintf(&b, "format = %s\n", tomlString(s.Format))
		}
		if s.Type == "command" {
			fmt.Fprintf(&b, "command = %s\ninterval = %s\n", tomlString(s.Command), tomlString(s.Interval.String()))
		}
	}
	return b.String()
}

func renderKeyCommands(cmds []KeyCommand) string {
	if len(cmds) == 0 {
		return `
# Your own commands on keys: a popup or a new pane running a command, a
# command in the background (its last line shown), or a plugin's action.
# [[keys.command]]
# keys = "g"            # after the prefix (mode = "terminal" binds without it)
# type = "popup"        # popup | pane | shell | plugin_action
# command = "lazygit"
# [[keys.command]]
# keys = "P"
# type = "plugin_action"
# plugin = "pr-status"
# action = "open"
`
	}
	var b strings.Builder
	for _, c := range cmds {
		fmt.Fprintf(&b, "\n[[keys.command]]\nkeys = %s\nmode = %s\ntype = %s\n", tomlList(c.Keys), tomlString(c.Mode), tomlString(c.Type))
		for _, f := range []struct{ k, v string }{{"command", c.Command}, {"plugin", c.Plugin}, {"action", c.Action}, {"title", c.Title}} {
			if f.v != "" {
				fmt.Fprintf(&b, "%s = %s\n", f.k, tomlString(f.v))
			}
		}
	}
	return b.String()
}
