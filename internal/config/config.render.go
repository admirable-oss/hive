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
	"dur":   func(d time.Duration) string { return tomlString(d.String()) },
	"q":     tomlString,
	"theme": renderCustomTheme,
	"keys":  RenderKeys,
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

[worktrees]
# Where ` + "`hive worktree create`" + ` puts checkouts, as <directory>/<repo>/<branch>
# ("" means ~/.hive/worktrees, or $HIVE_HOME/worktrees).
directory = {{q .Worktrees.Directory}}

[ui]
# Show the sidebar (environments and agents) when ` + "`hive ui`" + ` opens; prefix b
# toggles it. Screens 64 columns wide or less always open without it.
sidebar = {{.UI.Sidebar}}
sidebar_width = {{.UI.SidebarWidth}}
# Let the UI use the mouse: click to focus, drag borders, select to copy, scroll
# into history. Hold shift to select with the terminal's own mouse instead.
mouse = {{.UI.Mouse}}
# Where copied text goes: auto (the terminal through OSC 52, and the system
# clipboard when not over SSH) | osc52 | local | off
clipboard = {{q .UI.Clipboard}}

[theme]
# auto (dark or light, from the terminal's background) | catppuccin |
# catppuccin-latte | tokyo-night | gruvbox | nord | terminal (the terminal's own
# colours) | custom
name = {{q .Theme.Name}}
{{theme .Theme.Custom}}
{{keys .Keys}}`))

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
