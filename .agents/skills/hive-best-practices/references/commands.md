# Hive command reference (v0.6)

Every command takes `--json` (machine-readable output) and `--session <name>`. Commands that need the daemon start it on demand. Aliases: `env` for `environment`, `ps` for `process`, `tui` for `ui`. Confirm details with `hive <command> --help`.

## Multiplexer

| Command | Does |
|---|---|
| `hive`, `hive ui [--env <id>]` | Open the multiplexer (on the environment for the current directory by default) |
| `hive demo` | Open a tab of four demo agents |

## Environments and worktrees

| Command | Does |
|---|---|
| `hive env create <id> [--cwd <dir>] [-e K=V]… [--managed]` | An environment for a directory (default: the current one), or a new empty workspace |
| `hive env list`, `hive env get <id>` | Show environments, with git state |
| `hive env set <id> K=V…`, `hive env unset <id> K…` | Variables every new process there gets |
| `hive env remove <id>` | Stop its agents and remove it (your directory is kept) |
| `hive worktree create <branch> [--base <ref>] [--id <env>] [--repo <dir>]` | Check out a branch in a new worktree, with an environment |
| `hive worktree list [--repo <dir>]`, `hive worktree open <path>` | List worktrees; make an environment for an existing one |
| `hive worktree remove …` | Stop its agents, remove its environments and the worktree |

## Tabs and panes

| Command | Does |
|---|---|
| `hive tab create <env> [--name <n>] [--pane-name <n>] [-- cmd…]` | A tab with one pane (default command: the shell) |
| `hive tab list [env]`, `focus <id>`, `rename <id> <name>`, `close <id>` | Manage tabs (`close` stops their agents) |
| `hive pane split [pane] [-d right\|left\|down\|up] [-r 0.5] [--no-focus] [--name <n>] [--cwd <dir>] [-- cmd…]` | Split; the new pane starts in the split pane's current directory |
| `hive pane popup …` | A floating pane; it closes when its command exits |
| `hive pane list`, `get`, `focus [-d <dir>]`, `zoom`, `swap`, `resize`, `move`, `rename`, `close` | Arrange panes (`close` stops its agent) |
| `hive pane run <pane> <command line>` | Type a command and press Enter |
| `hive pane send-text <pane> <text>`, `send-keys <pane> <key>…`, `input <pane>` | Type text; press named keys (`C-c`, `Enter`, `Escape`, `F5`, …); send stdin bytes |
| `hive pane read [pane] [-s visible\|recent\|recent-unwrapped\|history] [-n N] [--ansi]` | Print what a pane shows |
| `hive pane wait-output [pane] --regex <re> \| --text <s> [--anywhere] [--timeout <d>]` | Wait for a matching line (exit 4 on timeout) |
| `hive layout export [env…] > f.json`, `hive layout apply f.json` | Save and recreate workspaces |

`[pane]` defaults to `$HIVE_PANE_ID`, the pane the command runs in.

## Agents

| Command | Does |
|---|---|
| `hive agent list [--all]` | Agents and their states: working, blocked, done, idle, exited (`--all`: every terminal) |
| `hive agent get [pane]`, `hive agent explain [pane]` | One agent; `explain` shows the rule that matched, reports and the screen |
| `hive agent manifests` | The manifests that recognise agents (built in, or in `agent-detection/` beside the config) |
| `hive server reload-agent-manifests` | Read edited manifests again |
| `hive pane report-agent [pane] --state working\|blocked\|idle\|done [--ttl d] [--session-id s] [--message m]` | An agent reports its own state; `--hook` for agent hooks (never fails) |
| `hive integration install\|uninstall\|status [claude\|codex]` | Hooks in the agent's settings that report its state (reversible) |

## Processes and terminals

| Command | Does |
|---|---|
| `hive ps start [-t] <env> -- <cmd>…` | Start a process without a pane (`-t` for interactive agents) |
| `hive ps list [env]`, `hive ps get <id>`, `hive ps stop <id>` | Inspect; stop a process and everything it started |
| `hive ps logs <id> [-n N] [-f] [--stderr]` | Its output; `-f` follows it until it exits. `--stderr` is for plain processes: a terminal agent writes everything to its terminal (stdout) |
| `hive terminal attach <id>` | Full-screen attach (`ctrl+]` detaches) |
| `hive terminal snapshot <id> [--ansi] [--scrollback N]` | Its current screen, optionally with history |
| `hive terminal input <id> <text>`, `resize <id> <w> <h>` | Type into or resize an agent's terminal |
| `hive events [type-prefix…]` | Daemon events as JSON lines |

## Runtime

| Command | Does |
|---|---|
| `hive status`, `hive ping`, `hive version` | Inspect (exit 3 when the daemon is not running) |
| `hive daemon start`, `restart`, `status`, `logs [-n N] [-f]` | Manage the background daemon (`restart` keeps agents) |
| `hive daemon stop [--keep-agents]` | Stop the daemon; without the flag, its agents too |
| `hive stop` | Stop the daemon **and every agent** |
| `hive daemon install`, `uninstall` | Start the daemon at login (launchd / systemd) |
| `hive session list`, `stop <name>`, `remove <name>` | Separate daemons with their own environments |
| `hive config path`, `show`, `default`, `init`, `validate`, `reset-keys` | The configuration file |
| `hive completion bash\|zsh\|fish\|powershell` | Shell completion |

Exit codes: 0 success, 1 error, 2 bad command line, 3 daemon not running, 4 `wait-output` timed out.
