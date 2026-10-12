# Coming from herdr

Hive and herdr solve the same problem: coding agents in a terminal
multiplexer that knows what they are doing. Most of what you know carries
over, and one command brings your settings and keys.

## Bring your configuration

```sh
hive config import-herdr                     # print the Hive config it would write, and a report
hive config import-herdr --herdr-keys --write  # write it, with herdr's whole keymap
```

It reads `~/.config/herdr/config.toml` (or `$HERDR_CONFIG_PATH`, or `--from`)
and sets what has a Hive equivalent:

| herdr | Hive |
|---|---|
| `keys.prefix` | `keys.prefix_keys` |
| `keys.<action> = "prefix+x"` | `[keys.prefix] <action> = "x"` |
| `keys.<action> = "ctrl+alt+n"` (no prefix) | `[keys.terminal] <action> = "ctrl+alt+n"` |
| `keys.navigate_pane_*` | `[keys.navigate] focus_*` |
| `keys.switch_tab = "prefix+1..9"` | `tab_1` … `tab_9` |
| `split_vertical` · `split_horizontal` | `split_right` · `split_down` |
| `cycle_pane_next/previous` · `swap_pane_left/right` | `focus_next/prev` · `swap_prev/next` |
| `copy_mode` · `resize_mode` · `workspace_picker`, `goto` | `mode_copy` · `mode_resize` · `goto` |
| `previous_workspace` · `next_workspace` | `prev_env` · `next_env` |
| `[[keys.command]]` (`key`, `run`/`command`, `type`, `cwd`) | `[[keys.command]]` (popup, pane or shell) |
| `theme.name`, `theme.auto_switch`, `theme.custom.*` | `theme.name` (`auto`), `[theme.custom]` |
| `ui.sidebar_width`, `ui.sidebar_start_collapsed`, `ui.mouse_capture` | `ui.sidebar_width`, `ui.sidebar`, `ui.mouse` |
| `ui.toast.delivery`, `ui.sound.enabled` | `[notify]` toast, terminal, system, sound |
| `ui.tab_bar_right` (hostname, datetime, text, command) | `[[ui.status_right]]` |
| `terminal.default_shell` | `terminal.shell` |
| `session.resume_agents_on_restore`, `session.startup_per_agent_delay_ms` | same, `session.restore_stagger` |

Everything else is listed with the reason it has no equivalent (workspace
management keys, Kitty graphics, the settings screen, …). `--herdr-keys`
starts from herdr's default keymap, then applies yours, so `prefix+v` splits
and `prefix+x` closes as you are used to. `--write` keeps your previous Hive
config beside the new one.

## Words

| herdr | Hive |
|---|---|
| workspace | environment (`hive env`) |
| tab, pane | tab, pane |
| server | daemon (`hive daemon`) |
| agent detection, hooks | the same: manifests, `hive integration install` |
| `herdr --default-config` | `hive config default` |
| remote / SSH | `hive --remote <host>`, `hive machine` |

## What is different

- **Agents outlive the daemon.** Each agent runs under its own shim, so
  restarting, upgrading or crashing the daemon never stops them, and
  `hive update` upgrades with no downtime.
- **Worktrees per agent.** `hive agent start --worktree <branch>` gives an
  agent its own checkout; `hive worktree` manages them.
- **Agents drive agents.** A stable CLI with `--json`, a Go SDK and an MCP
  server let agents start, prompt and wait for other agents.
- **Remote keeps the UI local.** The UI runs on your machine and talks to
  remote daemons over ssh, with compressed, paced screen updates (about
  14 MB/h for a busy pane).
- **Plugins** run at start, on events and on demand, and put tokens in the
  sidebar.

## Agents

Hive recognises the same agents (Claude Code, Codex, OpenCode, Gemini,
Cursor, Copilot, Amp, Aider), and Claude Code and Codex can report through
their hooks (`hive integration install claude`). Agents running in herdr
cannot move over while they run: start them again in Hive, resuming their
conversations (`claude --resume`, `codex resume --last`).
