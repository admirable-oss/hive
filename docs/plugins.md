# Plugins, key commands, sidebar tokens and the status bar

Hive can be extended without changing it:

- **Plugins**: programs the daemon runs for you, at start, on events, and on
  demand.
- **Key commands**: your own commands bound to keys in the multiplexer.
- **Sidebar tokens**: short labels any program puts next to a pane or an
  environment ("#12 open ✓", "tests 40/40").
- **The status bar**: text, the time, the host name, or a command's output, on
  the right of the tab bar.

## Plugins

A plugin is a directory (usually a git repository) with a `hive-plugin.toml`:

```toml
id = "pr-status"                  # lower-case letters, digits and dashes
name = "PR status"
version = "0.1.0"
description = "Shows each environment's pull request in the sidebar."
min_hive_version = "0.10.0"       # optional
platforms = ["darwin", "linux"]   # optional; empty means every system

[[startup]]                       # runs while the daemon runs
command = ["./pr-status.sh", "watch"]
restart = true                    # start it again when it exits (with back-off)

[[actions]]                       # runs when asked
id = "refresh"
title = "Refresh the PR tokens now"
command = ["./pr-status.sh", "once"]
timeout = "2m"                    # default 2m, at most 1h

[[events]]                        # runs for every matching event
on = "worktree.created"           # an event type, or a prefix such as "agent."
command = ["./on-worktree.sh"]
```

Commands run in the plugin's directory. A command starting with `./` is a
file of the plugin; anything else is looked up on the `PATH`. Each command
runs in its own process group, and is stopped with everything it started
when the plugin is disabled or the daemon stops.

### What a command gets

| Variable | Value |
|---|---|
| `HIVE_PLUGIN_ID`, `HIVE_PLUGIN_VERSION` | the plugin's |
| `HIVE_PLUGIN_DIR` | where the plugin is installed |
| `HIVE_PLUGIN_DATA` | a directory the plugin may write (`~/.hive/plugins/.data/<id>`, removed on uninstall) |
| `HIVE_PLUGIN_CONTEXT` | a file holding the context JSON (below), also given on stdin |
| `HIVE_BIN`, `HIVE_SOCKET_PATH`, `HIVE_SESSION`, `HIVE_HOME` | how to call this session's Hive back |

The context says why the command runs:

```json
{
  "plugin": "spawn-reviewer",
  "trigger": "event",
  "event": {"type": "worktree.created", "data": {"path": "…", "repo": "…", "branch": "feature/login", "environment": {"id": "feature-login", …}}},
  "session": "default",
  "environment": "feature-login"
}
```

`trigger` is `startup`, `action` or `event`. Actions run from the UI or with
`hive plugin run` also carry the `environment`, `tab` and `pane` the user is
in. For events, `environment`, `tab` and `pane` are read from the event
when it names them.

Plugins drive Hive with the CLI (`"$HIVE_BIN" agent start …`,
`"$HIVE_BIN" env report-metadata …`). `--template` makes its output easy to
read from a shell: `"$HIVE_BIN" env list --template '{{range .}}{{.id}} {{.path}}{{"\n"}}{{end}}'`.

Events a plugin may handle include `worktree.created`, `worktree.removed`,
`environment.created`, `environment.removed`, `tab.*`, `pane.*`,
`process.started`, `process.exited`, `agent.state`, `metadata.changed` and
`session.restored`; `hive events` shows them as they happen. Each plugin
runs at most four event commands at once; more events wait their turn.

### Installing

```sh
hive plugin install https://github.com/you/hive-pr-status   # or any git URL or path
hive plugin install ./pr-status-repo --ref v0.1.0
hive plugin link ./my-plugin       # use a directory as it is, for developing
hive plugin list
hive plugin info pr-status
hive plugin run pr-status refresh  # exit status: the action's
hive plugin logs pr-status [-f]
hive plugin disable pr-status
hive plugin enable pr-status
hive plugin uninstall pr-status
```

**Plugins run as you.** They can read your files, run your agents and reach
the network, like any program you run. Before installing, Hive shows
everything a plugin will run and when, then asks:

```
Plugin pr-status 0.1.0 (PR status)
  Shows each environment's pull request and its checks as a sidebar token (needs gh).
  from https://github.com/you/hive-pr-status @ 3f1c2a9d0b4e
  It runs as you, with your files and your Hive sessions:
    while hive runs, restarted when it stops: ./pr-status.sh watch
    action refresh (Refresh the PR tokens now): ./pr-status.sh once
    action open (Open this environment's PR in the browser): ./pr-status.sh open
hive: Install it? [y/N]
```

`--yes` answers for scripts. Without a terminal and without `--yes`,
nothing is installed. The commit installed is recorded. Install from a tag
(`--ref`) to know what you run.

Plugins live in `~/.hive/plugins/` and are shared by every session. Each
session's daemon runs the enabled ones. A plugin needing a newer hive
(`min_hive_version`) or another system (`platforms`) is listed as broken and
not run. Its output goes to `~/.hive/logs/plugins/<id>.log`, rotated.

### The reference plugins

`examples/plugins/` has two, which the end-to-end tests install and run:

- **pr-status** shows each environment's pull request (from `gh`) as a sidebar
  token, refreshed every `PR_STATUS_INTERVAL` seconds (60). Its actions are
  `refresh`, and `open` (the PR in the browser).
- **spawn-reviewer**: on `worktree.created`, it starts a reviewer agent
  (`SPAWN_REVIEWER_KIND`, default `claude`) in the new worktree and asks it to
  review the branch.

## Key commands

```toml
[[keys.command]]
keys = "g"                 # after the prefix; a list binds several keys
type = "popup"             # popup | pane | shell | plugin_action
command = "lazygit"
title = "lazygit"          # shown in the help (prefix ?)

[[keys.command]]
keys = "alt+t"
mode = "terminal"          # prefix (default) | terminal (no prefix) | navigate
type = "pane"
command = "npm test -- --watch"

[[keys.command]]
keys = "R"
type = "shell"             # in the background; its last line is shown
command = "git fetch --all --prune"

[[keys.command]]
keys = "P"
type = "plugin_action"
plugin = "pr-status"
action = "open"
```

- **popup** opens the command over the tab (it closes when the command ends).
- **pane** splits the focused pane, with the command in the new pane.
- **shell** runs the command in the background in the environment, and shows
  its last line, or its error.
- **plugin_action** runs a plugin's action, with the focused environment, tab
  and pane as its context.

Commands run with `sh -c` on the machine whose workspace is shown, with
`HIVE_PANE_ID` and `HIVE_TAB_ID` set. A command's key takes precedence over a
built-in binding, but not over one you set in `[keys.<mode>]`.
`hive config reset-keys` keeps your commands.

## Sidebar tokens and titles

Any program can put metadata on a pane or an environment:

```sh
hive pane report-metadata --token tests=40/40 --title reviewer   # this pane ($HIVE_PANE_ID)
hive env report-metadata api --token pr="#12 open ✓" --ttl 10m
hive env report-metadata api --token pr=                        # remove one token
hive pane report-metadata --clear                               # remove all
hive metadata                                                   # list everything
```

Tokens show beside the environment or the agent in the sidebar. A title
names the pane, unless you named it yourself. Everything expires after its
TTL (30 minutes by default, at most 7 days) unless reported again, so a
reporter that stops leaves nothing stale. Metadata is not kept across daemon
restarts. Limits: 8 tokens per target, keys of letters, digits, `-`, `_` and
`.`, and 48 characters of text.

## The status bar

```toml
[[ui.status_right]]
type = "command"           # its output's first line
command = "git -C ~/src/app branch --show-current"
interval = "30s"           # default 30s

[[ui.status_right]]
type = "hostname"          # the machine shown

[[ui.status_right]]
type = "datetime"
format = "%a %H:%M"        # strftime style; %H:%M by default

[[ui.status_right]]
type = "text"
text = "🐝"
```

Segments show on the right of the tab bar, before the mode indicator.
Status commands run on this machine (where the UI runs) and get 10 seconds
each.
