---
name: hive-best-practices
description: Use Hive well, for people and for agents. How Hive is organised (daemon, sessions, environments, worktrees, tabs, panes, agents), what survives what, how to lay out parallel agents, drive Hive from scripts or other agents, configure the multiplexer, avoid commands that stop agents, and troubleshoot. Use when helping someone set up or work in Hive, automating Hive from inside a pane, or deciding how to organise agents.
---

# Hive Best Practices

Hive runs coding agents (Claude Code, Codex, OpenCode, anything) under a background daemon, so they keep working when terminals close, SSH drops or the UI is closed. The `hive` command is both the multiplexer you work in and the CLI that scripts and agents use.

Check the installed build before advising: `hive version` and `hive <command> --help` are the authority. This guide describes v0.7 (milestone M5); [`references/commands.md`](references/commands.md) is a compact command reference.

## The model

| Thing | What it is | Notes |
|---|---|---|
| **Daemon** | The background runtime that owns everything below | Started on demand by any command; one per session |
| **Shim** | A small process per agent that owns its terminal | Why agents survive the daemon stopping, crashing or upgrading |
| **Session** | A separate daemon with its own environments | `--session <name>` or `HIVE_SESSION`; default `default` |
| **Environment** | A directory agents run in, with its own variables | `hive env create <id>` in a project; one per repository or worktree |
| **Worktree** | A git worktree with its own environment | One per agent working in parallel on the same repository |
| **Tab** | A layout of panes in an environment | Every client shows the environment's active tab |
| **Pane** | A place in a tab showing one agent's terminal | A split or popup starts in its pane's current directory |
| **Process / agent** | A program Hive supervises | Usually a pane's command; `hive ps start` runs one without a pane |

**What survives what:**

| Event | Agents keep running? |
|---|---|
| Closing the UI (`C-b d`), the terminal window, or SSH | Yes |
| `hive daemon restart`, a daemon crash, an upgrade | Yes; the next daemon re-attaches to them |
| `hive daemon stop --keep-agents` | Yes |
| `hive stop`, or `hive daemon stop` | **No**: every agent stops |
| Rebooting the machine | No (restore after reboot is planned for M6) |

## Getting started

```bash
cd ~/src/api && hive env create api     # an environment for this directory
hive                                    # open the multiplexer on it
hive demo                               # or: a tab of four demo agents to explore
hive daemon install                     # optional: start the daemon at login
hive integration install claude codex   # optional: agents report their state through hooks
```

In the multiplexer, press the prefix `ctrl+b`, then a key: `%` and `"` split, arrows or `h j k l` move, `z` zooms, `c` opens a tab, `w` finds any agent or pane, `O` opens the Overview of every agent, `a` goes to the next blocked or done agent, `[` enters copy mode, `?` lists every binding, and `d` detaches. The mouse works too. See `docs/keybindings.md`.

## Organising agents

- **One environment per project.** Create it from the project directory; panes start there.
- **One worktree per parallel agent.** Agents editing the same checkout collide. `hive worktree create fix/login-redirect` checks the branch out in its own directory, with its own environment; `hive worktree remove` cleans up (and stops its agents).
- **Tabs per task, panes per role.** For example, an agent beside its test watcher: `hive tab create api --name auth -- claude`, then `hive pane split -d down -r 0.3 -- npm test --watch`.
- **Name things.** `--name` on tabs and panes (or `C-b ,` and `C-b .` in the UI). Names show in titles, the sidebar, the picker and the Overview.
- **Keep variables in the environment.** `hive env set api API_URL=http://localhost:8080` applies to every new process there, instead of per-pane exports.
- **Save a workspace you rebuild often.** `hive layout export api > api.json` writes environments, tabs, splits and commands; `hive layout apply api.json` recreates them.
- **Use sessions for hard separation** (work and personal, or a client's machine budget). Each session is its own daemon. Everyday separation is better done with environments.

## Driving Hive from scripts and agents

Every pane's process gets `HIVE_PANE_ID`, `HIVE_TAB_ID`, `HIVE_ENV_ID`, `HIVE_PROCESS_ID`, `HIVE_SESSION`, `HIVE_HOME` and `HIVE_BIN` (the hive binary that started it), plus `TERM_PROGRAM=hive`. An agent can therefore manage its own workspace.

- **Use `--json` and read IDs from the output.** Never construct IDs. `hive --json tab create api -- claude` prints the tab and its pane.
- **Pane commands default to your own pane.** Inside a pane, `hive pane split` splits the pane you are in, and `hive pane close` closes it. Pass explicit IDs whenever you mean another pane.
- **Run, then wait for a marker, then read:**
  ```bash
  hive pane run "$P" 'npm test; echo "done:$?"'
  hive pane wait-output "$P" --regex '^done:[0-9]+$' --anywhere --timeout 10m
  hive pane read "$P" --source recent-unwrapped -n 200
  ```
  `wait-output` matches only output that appears after it starts unless `--anywhere` is given, and it exits with status 4 on timeout. Print a unique marker rather than matching a prompt.
- **Send keys by name:** `hive pane send-keys "$P" C-c`, or `Escape : w q Enter`. Use `send-text` for text without Enter, and `run` for a command line plus Enter.
- **React to events instead of polling:** `hive events pane. process. agent.` streams JSON lines (`process.exited`, `pane.created`, `agent.state`, `process.output` at most once a second per agent, …).
- **Run other agents:** `hive agent start --kind codex --env "$HIVE_ENV_ID" --worktree fix/x` prints the new agent's pane; `hive agent prompt "$P" "…" --wait --read 60` gives it work and waits; `hive agent wait`, `read` and `send-keys` follow and answer it. `hive --skill` prints the full guide for agents doing this, and `hive mcp` serves the same as MCP tools.
- **Know what agents are doing:** `hive --json agent list` gives each agent's state: `working`, `blocked` (waiting for a decision), `done` (finished, not looked at), `idle` or `exited`. Wait for an agent by watching `agent.state` events, or poll `hive --json agent get <pane>`. When a state looks wrong, `hive agent explain <pane>` shows why.
- **Exit codes:** 0 success, 1 error, 2 bad command line, 3 daemon not running, 4 `wait-output` timed out.
- **Agents without a pane:** `hive ps start -t api -- codex …` runs a terminal agent you can find in the Overview, read with `hive terminal snapshot <id>`, or attach to with `hive terminal attach <id>` (`ctrl+]` detaches).

## Safety: commands that stop agents

Confirm with the user before running any of these on their behalf, and never `kill` agent processes yourself:

| Command | Stops |
|---|---|
| `hive stop`, `hive daemon stop` | the daemon and **every agent** in the session |
| `hive session stop <name>`, `hive session remove <name>` | that session's agents; `remove` also deletes its stored state |
| `hive env remove <id>`, `hive worktree remove …` | that environment's agents (`worktree remove` also deletes the checkout) |
| `hive tab close <id>`, `hive pane close <id>`, `C-b &`, `C-b x` | the agents in that tab or pane |
| `hive ps stop <id>` | that process and everything it started |

Safe ways to step away or restart: detach (`C-b d`), close the terminal, `hive daemon restart`, `hive daemon stop --keep-agents`.

Inside a pane, the environment points at the user's own runtime. A bare `hive stop` run by an agent stops the user's daemon and all their agents. To experiment, use a separate `HIVE_HOME` (see the `hive-throwaway-repro` skill).

## Configuration

`~/.config/hive/config.toml` (`hive config path`). Every key is optional, and mistakes are warnings that keep the default, never failures.

```bash
hive config init          # a commented file with every default
hive config validate      # exit 1 on any problem, including unknown key bindings or themes
hive config show          # what is in effect
hive config reset-keys    # put the default [keys] back (keeps a .bak)
```

Useful sections: `[ui]` (`sidebar`, `sidebar_width`, `mouse`, `clipboard = auto | osc52 | local | off`), `[theme]` (`name = auto | catppuccin | catppuccin-latte | tokyo-night | gruvbox | nord | terminal | custom`, plus `[theme.custom]`), `[keys]` (`prefix_keys`, plus `[keys.<mode>]` tables where `[]` unbinds), `[terminal]` (`shell`, `scrollback_mb`), `[daemon] autostart`. `HIVE_LOG=debug` raises the daemon's log level.

## Upgrades and versions

The daemon reports its API level (`hive status --json`, `api_level`). A newer `hive` that finds an older daemon replaces it on its next command when no agent can be lost (agents run under shims, or none run); otherwise it explains and leaves it alone. After installing a new version, `hive daemon restart` does the same explicitly. Running from source with `go run ./cmd/hive` works: the daemon is started from a copy in `<HIVE_HOME>/bin`.

## Troubleshooting

| Symptom | Check |
|---|---|
| "Cannot reach the hive daemon" | `hive status` (exit 3 means not running); `hive daemon logs -n 100`; with `autostart = false`, `hive daemon start` |
| "…is older than this hive" | The running daemon predates this build and has agents a restart would stop; stop them, then `hive daemon restart` |
| Config warnings on every command | `hive config validate`, then fix or remove the named keys |
| A pane ignores input | It may be in a full-screen program or waiting: `hive pane read <id>`, then `hive pane send-keys <id> C-c` |
| "reconnecting…" while typing | The daemon restarted; the UI reconnects within about half a second |
| An agent's output vanished after it exited | Exited panes keep their last screen; `hive ps logs <id>` has the full output |
| Keys do nothing in the UI | `C-b ?` lists every binding; with no pane open, `ctrl+c` or `C-b d` quits |

## Not there yet

Planned, not shipped (see `ROADMAP.md`): restore after reboot (M6), SSH and remote machines (M7), plugins (M8). Do not promise these; suggest the shipped equivalent instead (panes, `pane wait-output`, `events`, layouts).
