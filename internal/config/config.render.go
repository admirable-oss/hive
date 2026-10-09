package config

import (
	"bytes"
	"fmt"
	"strconv"
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
	"dur": func(d time.Duration) string { return strconv.Quote(d.String()) },
	"q":   strconv.Quote,
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
`))
