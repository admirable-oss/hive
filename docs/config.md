# Configuration reference

<!-- Generated from internal/config (Render); do not edit by hand. Rewrite it
with: HIVE_UPDATE_SURFACE=1 go test ./internal/config -run Reference -->

Hive reads `~/.config/hive/config.toml` (or `$XDG_CONFIG_HOME/hive/config.toml`, or
`$HIVE_CONFIG`). Every key is optional; `hive config default` prints this
whole file with its defaults, and `hive config init` writes it. Unknown keys and
invalid values are reported as warnings (`hive config validate`) and fall back to
the defaults, so a typo never stops the daemon.

## `[daemon]`

| Key | Default | Description |
|---|---|---|
| `autostart` | `true` | Start the daemon automatically when a command needs it. |
| `shutdown_timeout` | `"15s"` | How long stopping the daemon waits for agents to exit. |

## `[log]`

| Key | Default | Description |
|---|---|---|
| `level` | `"info"` | debug \| info \| warn \| error  (HIVE_LOG overrides this) |
| `format` | `"text"` | text \| json |
| `max_size_mb` | `10` | The daemon log rotates at this size and keeps this many old files. |
| `max_backups` | `3` | The daemon log rotates at this size and keeps this many old files. |

## `[process]`

| Key | Default | Description |
|---|---|---|
| `stop_grace` | `"3s"` | How long an agent gets to exit after SIGTERM before it is killed. |

## `[terminal]`

| Key | Default | Description |
|---|---|---|
| `default_width` | `220` | Size of a new agent terminal when the caller does not choose one. |
| `default_height` | `50` | Size of a new agent terminal when the caller does not choose one. |
| `scrollback_mb` | `10` | Scrollback kept per agent, in MiB (lines that scrolled off its screen). |
| `shell` | `""` | What a new pane runs when no command is given ("" means $SHELL -l). |

## `[git]`

| Key | Default | Description |
|---|---|---|
| `refresh_interval` | `"5s"` | How often environments' branch and dirty state are re-read (changes to the repository itself, like commits and checkouts, are noticed at once). |

## `[session]`

| Key | Default | Description |
|---|---|---|
| `restore` | `true` | When the host reboots (or an agent's shim dies), start its panes again where they were working, with their tabs, layout and focus. |
| `resume_agents_on_restore` | `true` | Restored agents continue their conversations (`claude --resume <id>`, `codex resume <id>`); off, they start fresh. |
| `restore_stagger` | `"1s"` | Pause between restored agents' starts. |
| `snapshot_interval` | `"30s"` | The workspace is saved to session.json after every change and on this timer. |
| `keep_snapshots` | `48` | Earlier snapshots kept in snapshots/. |
| `replay_history` | `false` | Keep each pane's recent text and show it again when the pane is restored. The text is whatever programs printed, secrets included; it is kept in history/ (private to you) only while this is on. |
| `replay_lines` | `500` | How many recent lines of each pane are kept for that. |

## `[retention]`

| Key | Default | Description |
|---|---|---|
| `exited_processes` | `"168h0m0s"` | Records and output of processes that ended, and that no pane shows, are removed after this long (`hive gc` removes them now). |
| `process_log_mb` | `50` | Each agent's output log rotates at this size in MiB, keeping one older file (0 lets it grow). |
| `gc_interval` | `"6h0m0s"` | How often the daemon cleans up by itself ("0s": never). |

## `[update]`

| Key | Default | Description |
|---|---|---|
| `channel` | `"stable"` | The releases `hive update` installs: stable, or preview (release candidates too). |

## `[remote]`

| Key | Default | Description |
|---|---|---|
| `ssh` | `"ssh"` | The ssh command that reaches other machines (`hive --remote`, `hive machine`); ~/.ssh/config applies. HIVE_SSH overrides it. |
| `frame_budget_kb` | `48` | How much of agents' screens a remote daemon sends this client, in KiB per second (0: no limit). Screens update less often when it is reached. |

## `[worktrees]`

| Key | Default | Description |
|---|---|---|
| `directory` | `""` | Where `hive worktree create` puts checkouts, as <directory>/<repo>/<branch> ("" means ~/.hive/worktrees, or $HIVE_HOME/worktrees). |

## `[notify]`

| Key | Default | Description |
|---|---|---|
| `on` | `["blocked", "done"]` | When an agent wants you: blocked (waiting for your decision) or done (finished, and you have not looked yet). Never for the pane you are in. |
| `toast` | `true` | A toast in the UI. |
| `terminal` | `"auto"` | Ask your terminal to notify: auto (OSC 9 where supported) \| osc9 \| osc777 \| off. This works over SSH. |
| `system` | `false` | The desktop's notifications (osascript, notify-send); never over SSH. |
| `sound` | `false` | A sound with each notification. |

Per agent (by manifest ID, see `hive agent manifests`), which states notify:

```toml
[notify.agents]
codex = ["blocked"]
aider = []
```

## `[ui]`

| Key | Default | Description |
|---|---|---|
| `sidebar` | `true` | Show the sidebar (environments and agents) when `hive ui` opens; prefix b toggles it. Screens 64 columns wide or less always open without it. |
| `sidebar_width` | `28` | The sidebar's width, in columns. |
| `mouse` | `true` | Let the UI use the mouse: click to focus, drag borders, select to copy, scroll into history. Hold shift to select with the terminal's own mouse instead. |
| `clipboard` | `"auto"` | Where copied text goes: auto (the terminal through OSC 52, and the system clipboard when not over SSH) \| osc52 \| local \| off |

The right of the tab bar: text, datetime (format in strftime style, %H:%M by default), hostname, or a command's first line every interval.

```toml
[[ui.status_right]]
type = "command"
command = "git -C ~/src/app branch --show-current"
interval = "30s"
[[ui.status_right]]
type = "datetime"
format = "%a %H:%M"
```

## `[theme]`

| Key | Default | Description |
|---|---|---|
| `name` | `"auto"` | auto (dark or light, from the terminal's background) \| catppuccin \| catppuccin-latte \| tokyo-night \| gruvbox \| nord \| terminal (the terminal's own colours) \| custom |

With name = "custom", [theme.custom] starts from a built-in theme ("base") and overrides colours: fg bg muted border accent selection success warning error info, each "#rrggbb", an ANSI index 0-255, or "default".

```toml
[theme.custom]
base = "nord"
accent = "#ff79c6"
```

## `[keys]`

| Key | Default | Description |
|---|---|---|
| `prefix_keys` | `["ctrl+b"]` | The prefix key(s): press one, then a key bound in prefix mode (prefix ? lists every binding). Several are allowed, e.g. ["ctrl+b", "ctrl+a"]. |

Change bindings per mode (terminal, prefix, navigate, resize, copy): an action takes a key or a list of keys, and [] unbinds it. Bindings in [keys.terminal] work without the prefix.

```toml
[keys.prefix]
split_right = ["|", "%"]
zoom = []
[keys.terminal]
focus_left = "alt+h"
```

Your own commands on keys: a popup or a new pane running a command, a command in the background (its last line shown), or a plugin's action.

```toml
[[keys.command]]
keys = "g"            # after the prefix (mode = "terminal" binds without it)
type = "popup"        # popup | pane | shell | plugin_action
command = "lazygit"
[[keys.command]]
keys = "P"
type = "plugin_action"
plugin = "pr-status"
action = "open"
```
