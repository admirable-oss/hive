# Concepts

```
machine ── session ── daemon
                        ├── environment (a directory: a repo, a worktree)
                        │     └── tab ── panes (a layout of splits, popups)
                        │                  └── process ── shim ── terminal
                        │                                  └── agent (when a manifest recognises it)
                        ├── plugins
                        └── snapshots
```

**Daemon.** One background process per session runs everything: it starts
processes, keeps their terminals, reads agents' states, saves the workspace
and serves clients over a private unix socket. Every `hive` command and the
multiplexer are clients of it, and the first one starts it. `hive stop`
stops it with its agents, and `hive daemon restart` restarts it without
stopping them.

**Session.** A separate Hive with its own daemon, socket and everything
below: `--session work` or `HIVE_SESSION=work`. Most people use the default
one.

**Shim.** A small process per agent that owns the agent's terminal. Because
shims are separate from the daemon, agents survive the daemon crashing, being
restarted or being upgraded, and the next daemon re-attaches to them with the
screen intact.

**Environment.** A named directory agents work in: one you already have
(`hive env create app --cwd .`), a managed workspace, or a git worktree
(`hive worktree create fix/login`, one branch per agent). An environment
carries variables for its agents and its git state (branch, ahead/behind,
dirty).

**Tab and pane.** An environment has tabs, and a tab is a tiling layout of
panes, split right or down, with optional floating popups. A pane shows one
process's terminal. Panes can be zoomed, swapped, moved between tabs and
environments, and driven by name: `hive pane send-text`, `read` and
`wait-output`.

**Process.** What runs in a pane (or without one): a command line with a
lifecycle (starting, running, exited, failed, killed), output logs, and a PTY.

**Agent.** A process a manifest recognises as a coding agent: Claude Code,
Codex, OpenCode and the others ([agent guides](agents.md)). Hive reads its
state from its screen, or from its hooks:

```
unknown → working ⇄ blocked → done → idle
```

*Done* means it finished and you have not looked yet. Rollups summarise
states per tab, environment and session.

**Agents operating agents.** The same API that drives the UI is open to
scripts (`--json`, `--template`), the Go SDK (`pkg/hiveapi`) and agents
through MCP (`hive mcp`). An agent can start, prompt, wait for and read
other agents.

**Snapshot.** The workspace (every pane's command, directory and agent
conversation) is saved to `session.json` as it changes. After a reboot, lost
panes start again where they were ([persistence](persistence.md)).

**Machine.** Another computer running Hive, reached over your own ssh:
`--remote <host>` for one command, or a saved machine (`hive machine add`)
whose agents the sidebar lists beside yours ([remote](remote.md)).

**Plugin.** A program Hive runs for you: at start, on events (a worktree
created, an agent blocked), or on demand, often to put tokens in the
sidebar ([plugins](plugins.md)).
