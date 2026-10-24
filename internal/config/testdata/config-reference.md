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
| `workflow_runs` | `200` | Finished workflow runs kept for `hive workflow runs` and reports, and for how long; the oldest are forgotten first. |
| `workflow_run_age` | `"720h0m0s"` | Finished workflow runs kept for `hive workflow runs` and reports, and for how long; the oldest are forgotten first. |

## `[update]`

| Key | Default | Description |
|---|---|---|
| `channel` | `"stable"` | The releases `hive update` installs: stable, or preview (release candidates too). |

## `[remote]`

| Key | Default | Description |
|---|---|---|
| `ssh` | `"ssh"` | The ssh command that reaches other machines (`hive --remote`, `hive machine`); ~/.ssh/config applies. HIVE_SSH overrides it. |
| `frame_budget_kb` | `48` | How much of agents' screens a remote daemon sends this client, in KiB per second (0: no limit). Screens update less often when it is reached. |
| `image_budget_kb` | `1024` | How fast a pasted image is sent to a remote daemon, in KiB per second (0: no limit). Keys typed meanwhile go between its pieces. |

## `[discovery]`

| Key | Default | Description |
|---|---|---|
| `advertise` | `false` | Announce this machine on local networks (multicast DNS), so `hive machine discover` finds it elsewhere. The announcement has a name, an identity, the ssh port and Hive's API level, nothing else; it opens no port and grants nothing. Pairing still needs ssh access and your confirmation. |
| `name` | `""` | The name it is announced under ("" is the host name). |
| `ssh_port` | `22` | The port ssh listens on here, which the announcement gives. |
| `interfaces` | `[]` | Network interfaces to announce and look on (empty: every one that is up and multicast, except VPN tunnels and loopback). |
| `browse` | `true` | Let `hive machine discover` look for runtimes. Off, saved machines and pairing by address keep working. |

## `[images]`

| Key | Default | Description |
|---|---|---|
| `max_kb` | `5120` | The largest image pasted with prefix I, in KiB. The UI reads no more from the clipboard, and the daemon (with its own config) accepts no more. |
| `max_megapixels` | `16` | The most pixels a pasted image may have, in megapixels. |
| `keep` | `"1h0m0s"` | Agents that take an image as a file (Claude Code, Codex) get it saved in a private directory and its path pasted; the daemon removes it after this long ("0s": never save, so such agents refuse images). |
| `log_graphics` | `false` | Keep images that agents print (Kitty graphics) in their output logs. Off strips them, so image data is never written to disk. |
| `render` | `"auto"` | How images agents print are drawn: auto (the terminal's graphics protocol: Kitty, iTerm2 or Sixel, else coloured cells) \| cells \| text (a placeholder naming the image) \| off |

## `[plugins]`

| Key | Default | Description |
|---|---|---|
| `allow` | `[]` | Capabilities granted, without asking, to plugins that list them as optional (hive.read, hive.write, hive.input, hive.process, hive.admin, filesystem.write, network, process.exec). |
| `deny` | `[]` | Capabilities no plugin gets; one that requires a denied capability does not run. |
| `isolation` | `"auto"` | auto (sandbox plugins that declare isolation = "strict") \| standard (never) \| strict (every plugin; needs sandbox-exec on macOS, bwrap on Linux) |
| `restart_limit` | `5` | A plugin command that fails more than restart_limit times within restart_window is no longer restarted (`hive plugin reload <id>` resets it). |
| `restart_window` | `"10m0s"` | A plugin command that fails more than restart_limit times within restart_window is no longer restarted (`hive plugin reload <id>` resets it). |
| `hidden` | `[]` | Contributions to turn off: "plugin", "plugin:kind" or "plugin:kind:id" (`hive plugin contributions` lists them). |

## `[observers]`

| Key | Default | Description |
|---|---|---|
| `enabled` | `true` | Serve the observer socket: read-only access for dashboards and notifiers holding a credential (`hive observer create`). Off, nobody can observe. |
| `history_events` | `2048` | Events kept for observers to catch up with after a reconnect. |
| `max_connections` | `32` | Open observer connections allowed, all credentials together. |

## `[web]`

| Key | Default | Description |
|---|---|---|
| `listen` | `"127.0.0.1:8484"` | The web observer (`hive web serve`): read-only agent status in a browser. Its HTTP address. Beyond this machine it needs TLS or a proxy that terminates it. |
| `public_url` | `""` | Where browsers reach it, for share links ("" derives it from listen). |
| `tls_cert` | `""` | A certificate and key to serve HTTPS. |
| `tls_key` | `""` | A certificate and key to serve HTTPS. |
| `behind_proxy` | `false` | A reverse proxy in front terminates TLS (cookies are then Secure). |
| `push` | `true` | Notifications on phones and browsers that turn them on (Web Push; they need HTTPS, or localhost). |
| `push_contact` | `""` | How push services can reach you, if they must (mailto: or https:; "" uses the public URL). |
| `push_allow_any_endpoint` | `false` | Accept push services other than the browsers' own (Google, Mozilla, Apple, Microsoft). |

## `[worktrees]`

| Key | Default | Description |
|---|---|---|
| `directory` | `""` | Where `hive worktree create` puts checkouts, as <directory>/<repo>/<branch> ("" means ~/.hive/worktrees, or $HIVE_HOME/worktrees). |

## `[tasks]`

| Key | Default | Description |
|---|---|---|
| `capacity` | `8` | Tasks of plans (`hive task run`) running on this machine at once, across plans. Plans on other machines place tasks here up to it too. |
| `labels` | `[]` | Labels this machine offers: a task with labels = ["gpu"] runs only on a machine that has them all (saved machines get theirs with `hive machine set`). |
| `keep_plans` | `100` | Finished plans kept for `hive task list` and reports; the oldest are forgotten first. |

Per agent kind, the most tasks running at once across plans:

```toml
[tasks.kinds]
claude = 4
```

## `[branches]`

| Key | Default | Description |
|---|---|---|
| `agent_pattern` | `"hive/{kind}/{id}"` | The branch of an agent started with `--isolate`: {kind}, {name}, {env}, {date} and {id} (6 random hex digits). |
| `task_pattern` | `"hive/{plan}/{task}"` | The branch of an isolated task: {plan}, {task}, {kind}, {date}, {id}. |
| `cleanup` | `"merged"` | A merged branch's worktree: "merged" removes it once nothing uncommitted and no running process is in it; "never" leaves it to you. |
| `delete_merged` | `false` | Also delete the local branch of a worktree removed that way, when git agrees it has merged. |
| `check_interval` | `"30s"` | How often branches are compared again when no change was noticed. |
| `keep` | `100` | Cleaned-up and removed branches remembered for `hive branch list --all`. |

Gates a branch passes before it is ready (`hive branch ready`), run in its worktree in order:

```toml
[[branches.gate]]
name = "test"
command = ["go", "test", "./..."]
timeout = "20m"                  # default 30m
repo = "~/src/api"               # only this repository's branches
```

Pull requests go to GitHub (through gh) or GitLab (glab) by the remote's host. Other hosts:

```toml
[[branches.forge]]
host = "git.example.com"
type = "gitlab"                  # github | gitlab | command
command = ["my-pr"]              # for type = "command": gets the request as JSON
```

## `[workflows]`

| Key | Default | Description |
|---|---|---|
| `paths` | `[]` | Your own workflows are the *.toml files in ~/.hive/workflows. These are more files and directories ("~/" is home), such as a repository's .hive/workflows: shared files, whose triggers fire only once you trust them (`hive workflow trust <name>`), and again after each change. |
| `max_runs` | `4` | Workflow runs running at once, across workflows; the rest wait. |
| `git_interval` | `"10s"` | How often watched repositories (for git triggers) and workflow files are read again, besides changes noticed. |

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
| `name` | `"auto"` | auto (hive or hive-light, from the terminal's background) \| hive (amber on graphite, as the desktop app) \| hive-light \| catppuccin \| catppuccin-latte \| tokyo-night \| gruvbox \| nord \| terminal (the terminal's own colours) \| custom |

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
