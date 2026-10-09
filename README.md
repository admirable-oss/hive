<p align="center">
  <img src="https://9lv0ptfqwc.ufs.sh/f/Ct83ioyjHOfENMZX8ewdYpswjne6l2VQiGOf7z53XWRtkTUu" alt="Hive" width="200px" />
</p>
<p align="center">
  <img src="https://9lv0ptfqwc.ufs.sh/f/Ct83ioyjHOfEnlhYREXGahkPT9wIcdlGmYZKCQMWiD6ESrU0" alt="Wordmark" width="100px" />
</p>
<h3 align="center">The agent infrastructure for agents that don’t clock out</h3>
<p align="center">Run them <strong style="color: #F5B942">anywhere</strong>. Leave them running.</p>

<p align="center">
  <img src="https://img.shields.io/badge/License-Apache%202.0-%23F5B942" />
  <img src="https://img.shields.io/github/last-commit/admirable-oss/hive" />
  <img src="https://img.shields.io/badge/Release-v0-%23F5B942" />
</p>

https://github.com/user-attachments/assets/bd7b9d6e-d0f5-43d7-9889-34c1a3fb0c9e

## Vision

**Start an agent and walk away.** Hive keeps its environment alive when your terminal closes, your SSH connection drops, or your laptop goes to sleep. Come back later and pick up where you left off.

- **One machine, many agents.** Each agent gets its own workspace, terminal and context, so a hundred of them can work without stepping on each other.
- **See everything working.** A terminal multiplexer built for agents: tabs of split panes with live agent screens, and an Overview of which agents are running, waiting, done or failed, so you don't have to open a dozen terminals.
- **Bring your own agent.** Claude Code, Codex, OpenCode, Cursor, your own agent, whatever comes next. Hive doesn't replace them; it gives them somewhere to live.
- **Agents that operate agents.** The same interface you use (creating workspaces, launching tasks, reading output) is available to agents themselves, so the infrastructure becomes part of the agent's toolkit.
- **Built for the background.** Long builds, large migrations, test suites, research and parallel tasks: work that takes longer than a terminal session.

## Status

Hive is **pre-release (v0)** and built in public. The wire protocol and on-disk layout may still change.

| Area | State |
|------|-------|
| Runtime daemon, unix-socket protocol (multiplexed protocol 2, plain protocol 1), CLI | ✅ working |
| Agents survive daemon crashes, restarts and upgrades (one shim per agent) | ✅ working |
| Daemon autostart, login service (launchd / systemd), single-instance lock | ✅ working |
| Config file, structured logs with rotation | ✅ working |
| Environments (persistent workspaces) | ✅ working |
| Processes: plain or PTY-backed, lifecycle, stdout/stderr logs (tail, follow), clean stop of the whole process tree | ✅ working |
| Terminal emulation: attach shows the exact screen at once, snapshots, scrollback | ✅ working |
| Terminal attach / detach, live input, resize ("last to type sets the size") | ✅ working |
| Event stream (`hive events`): processes and environments as they change | ✅ working |
| Multiplexer UI: tabs and split panes of live agents, sidebar, Overview, copy mode, mouse, themes, configurable keys | ✅ working |
| Crash recovery: stale records closed out, orphaned agents stopped (PID and start time verified) | ✅ working |
| Agent state detection (*blocked*, *waiting for input*) | 🚧 next |
| Agent-facing API (agents managing agents) | 🗺 planned |
| Reconnect from another machine | 🗺 planned |
| Workspace isolation per agent (git worktrees), log rotation | 🗺 planned |
| Windows support | 🗺 planned |

## Quick start

Requires Go 1.26+ on macOS or Linux.

```sh
make build          # or: go build -o hive ./cmd/hive

./hive demo         # starts the daemon on demand, then launches four demo agents
./hive              # open the multiplexer
./hive daemon install   # optional: start the daemon at login, restart it if it crashes
```

The multiplexer works like tmux: press the prefix, `Ctrl+B`, then a key.

| After `Ctrl+B` | |
|---|---|
| `%` · `"` | split the pane right · down |
| `←↓↑→` or `h j k l` | move between panes (`space` for sticky navigate mode, `r` for resize mode) |
| `z` · `x` | zoom the pane · close it |
| `c` · `1`–`9` · `n` `p` | new tab · go to a tab · next, previous |
| `w` | go to any agent or pane (fuzzy) |
| `O` | the Overview: every agent, with a live preview |
| `[` | copy mode: vim keys, `/` search, `v`/`V` select, `y` copy |
| `?` | every binding, with a filter |
| `d` | detach; agents keep running |

A split or popup starts in the directory its pane is working in (after a `cd`, too); a new tab starts at the environment's root. The mouse works too: click to focus, drag borders, select to copy, scroll into history. [docs/keybindings.md](docs/keybindings.md) lists every binding and how to change them.

### CLI

| Command | What it does |
|---------|--------------|
| `hive` / `hive ui [--env <id>]` | Open the multiplexer |
| `hive env list \| create <id> \| get <id> \| rm <id>` | Manage environments (removing one stops its agents first) |
| `hive ps start [-t] <env> [--] <cmd> [args…]` | Start a process (`-t` gives it a terminal) |
| `hive ps list [env] \| get <id> \| stop <id>` | Inspect and stop processes |
| `hive ps logs <id> [-n N] [-f] [--stderr]` | Print a process's output; `-f` follows it until the process exits |
| `hive terminal attach <id>` | Attach to an agent's terminal; **Ctrl+]** detaches and the agent keeps running |
| `hive terminal snapshot <id> [--ansi] [--scrollback N]` | Print an agent's current screen, optionally with its last N lines of history |
| `hive terminal input <id> <text>` · `resize <id> <w> <h>` | Type into / resize an agent's terminal |
| `hive events [type-prefix…]` | Stream daemon events as JSON lines |
| `hive status` · `hive ping` · `hive version` | Inspect the runtime (exit code 3 when the daemon is not running) |
| `hive stop` | Stop the runtime **and every agent it runs** |
| `hive daemon` | Run the runtime in the foreground (what service managers run) |
| `hive daemon restart` | Restart the runtime; agents keep running and are re-attached |
| `hive daemon start \| stop [--keep-agents] \| status \| logs [-f]` | Manage the background daemon |
| `hive daemon install \| uninstall` | Run the daemon as a launchd agent (macOS) or systemd user service (Linux) |
| `hive config path \| show \| default \| init \| validate \| reset-keys` | Inspect and create the configuration file; put the default key bindings back |
| `hive completion bash\|zsh\|fish\|powershell` | Shell completion, including environment and process IDs |

Every command that talks to the daemon starts it if it is not running (turn this off with `daemon.autostart = false`). Add `--json` to any command for machine-readable output. Exit codes: `0` success, `1` error, `2` bad command line, `3` daemon not running.

### Configuration

Hive reads `~/.config/hive/config.toml` (or `$XDG_CONFIG_HOME/hive/config.toml`, or `$HIVE_CONFIG`). Every key is optional; `hive config init` writes a commented file with the defaults:

```toml
[daemon]
autostart = true            # start the daemon when a command needs it
shutdown_timeout = "15s"    # how long stopping waits for agents to exit

[log]
level = "info"              # debug | info | warn | error  (HIVE_LOG overrides)
format = "text"             # text | json
max_size_mb = 10            # the daemon log rotates at this size…
max_backups = 3             # …and keeps this many old files

[process]
stop_grace = "3s"           # SIGTERM → SIGKILL delay when stopping an agent

[terminal]
default_width = 220         # size of a new agent terminal
default_height = 50
scrollback_mb = 10          # history kept per agent (lines that scrolled off its screen)

[ui]
sidebar = true              # show environments and agents beside the panes
sidebar_width = 28
mouse = true                # click, drag borders, select to copy, scroll
clipboard = "auto"          # auto | osc52 | local | off

[theme]
name = "auto"               # auto | catppuccin | catppuccin-latte | tokyo-night | gruvbox | nord | terminal | custom

[keys]
prefix_keys = ["ctrl+b"]    # bindings per mode in [keys.<mode>]; see docs/keybindings.md
```

Unknown keys and invalid values are reported as warnings and fall back to the defaults, so a typo never stops the daemon that keeps your agents alive. A file that is not valid TOML is an error, and the daemon refuses to start with it. `hive config validate` checks a file and exits non-zero on any problem.

Data lives in `~/.hive`. Set `HIVE_HOME` to use another directory, for example to run an isolated daemon for testing.

## Architecture

Hive is a single binary with three roles:

- a **shim** per agent (`hive __shim`), which owns the agent's PTY, terminal emulator and logs, and outlives everything except `hive stop`;
- a **daemon** that owns environments and supervises agents through their shims;
- **clients** (the CLI and the multiplexer UI) that talk to the daemon over a unix socket.

Closing a client never touches an agent. Stopping, crashing or upgrading the daemon doesn't either: the next daemon re-attaches to the shims ([ADR 0006](docs/adr/0006-shims-protocol-2-and-frames.md)).

```
cmd/hive ──────────── CLI composition root: builds one `app` (paths, config, client); cobra commands
 │
 ├── tui ──────────── multiplexer UI (mux app, compositor, keymap, copy mode, themes); depends only on client
 ├── daemonctl ────── start the daemon detached (autostart), install launchd / systemd services
 ├── config ───────── config.toml: defaults, forgiving validation, rendering
 ├── client ───────── protocol 2 over one connection (falls back to protocol 1)
 │        ╎
 │        ╎  unix socket (0600) · newline-delimited JSON
 │        ╎
 └── runtime ──────── daemon composition root: wires modules, serves the socket, owns agent lifetimes
       ├── process ── supervises agents: start, monitor, stop, recover / adopt, logs
       │     ├── environment ── persistent workspaces
       │     └── terminal ───── sessions, viewers, size arbitration, attach / frames / snapshot
       │            └── shim ──── one process per agent: PTY + emulator; adopted after restarts
       │                   ╎
       │                   ╎  run/<id>/shim.sock · protocol 2
       ├── event ──── event bus behind `events.subscribe`
       └── vt ─────── terminal emulation (x/vt), frames codec, ANSI painter, scrollback
           protocol · jsonfile · pgroup · platform · logging · buildinfo   (leaves)
```

Dependencies only point **down**. Leaf packages know nothing about Hive's domain, and no package imports one above it. The rule is enforced by `depguard` in `make lint`.

### Following a call from `main` to the OS

Every layer is reached through an explicit call, so you can read the code top to bottom:

```
hive ps start -t dev -- claude
  main.execute                      cmd/hive/main.go      → newApp (config, paths, client), cobra root
  root PersistentPreRunE            cmd/hive/root.go      → app.ensureDaemon: ping, or autostart via daemonctl.Spawn
  newProcessStartCmd (RunE)         cmd/hive/cmd_process.go
  client.ProcessStart → call()      internal/client       → protocol.Stream.Send
  ─────────────── socket ───────────────
  runtime.Server.accept             internal/runtime      → protocol.Serve(conn) (checks the protocol version)
  protocol.Router.Handle            internal/protocol     → "process.start"
  protocol.Method[StartRequest]     decodes params once, encodes the result
  process.service.Start             internal/process      → Environments.Get (port)
  process.service.launch            → terminal.Service.Open → shim.Launcher.Open: spawn `hive __shim` (setsid)
  shim.Run (in the shim process)    internal/shim         → PTYFactory.Open → creack/pty, vt.Terminal
  process.service.monitor           Remote.Wait (shim.wait) → persist the final state → Remote.Release
```

### On disk

```
~/.hive/
  hive.sock                                  daemon socket (0600, in a 0700 directory)
  hive.pid                                   daemon PID; flock-ed while the daemon runs (one daemon per root)
  logs/daemon.log[.1…]                       structured daemon log, rotated by size
  logs/daemon.stderr                         the daemon's stderr from its last start (start-up errors)
  run/<process-id>/                          one shim per live agent (0700)
    spec.json  state.json                    what it runs; running / exited + exit code
    shim.sock  shim.log  shim.stderr         its socket (0600) and its own logs
  environments/<env>/
    environment.json
    workspace/                               where the env's agents run
    processes/<id>/
      process.json                           lifecycle record (written atomically)
      stdout.log  stderr.log                 output, private to the user (PTY output goes to stdout.log)
```

### Wire protocol

One JSON object per line. Requests carry `type`, `id`, `method` and `params`; each response carries the same `id` with either `result` or `error: {code, message}`. Error codes are `invalid_request`, `unknown_method`, `invalid_params`, `not_found`, `internal_error`, `unsupported_version`, `unsupported` and `unavailable`.

- **Protocol 1** is the default: one request, one response, in order. Hand-written clients (`nc`) use it. A request may open a *pipe* that takes over the rest of the connection.
- **Protocol 2** starts with `{"type":"hello","version":"2","params":{"client":…}}`. The server answers with a `welcome` listing its methods and capabilities. From then on, requests run concurrently and any number of pipes share the connection: the response that opens one carries a `stream` ID, and its bytes travel as `{"type":"data","stream":…,"data":<base64>}` until a `close`. The CLI and the multiplexer speak protocol 2, and fall back to protocol 1 against an older daemon.

| Namespace | Methods |
|-----------|---------|
| `runtime` | `ping`, `status`, `shutdown` (`stop_agents`, default true) |
| `environment` | `list`, `create`, `get`, `remove` |
| `process` | `start`, `list`, `get`, `logs`, `logs.stream` (pipe), `stop` |
| `terminal` | `attach` (pipe: screen painted as ANSI, keystrokes back), `frames` (pipe: binary screen frames), `snapshot`, `input`, `resize` |
| `events` | `subscribe` (pipe: one JSON event per line; `events_lost` means resync) |

Screens never travel as raw output. Each agent's terminal is emulated in its shim, and viewers get frames computed from the current screen when they are ready for one. A slow viewer gets fewer frames, never a corrupted screen, and a late one starts with an exact keyframe. `process.logs` returns a size-capped tail in one reply (`truncated: true` when cut); `process.logs.stream` sends logs of any size.

## Design principles

### Protocol-Oriented Programming (POP)

Hive is designed **contracts first**. Each layer depends on a small interface (a *protocol*, in the Swift sense), and concrete types are plugged in at a single composition root.

- **The consumer owns the interface.** `process` doesn't import the whole environment service. It declares `Environments{ Get }` and `Terminals{ Open }` and accepts anything that fits. The runtime declares `Supervisor{ Recover, StopAll }` for the same reason.
- **Small surfaces.** `protocol.Handler` has one method, and `Store`, `Runner`, `Factory` and `ListenerFactory` have a handful each. Small contracts are cheap to fake, so most tests need no network or processes.
- **Composition instead of inheritance.** Go has no class inheritance. Where you might reach for it, Hive **embeds** an interface and overrides one method. `runtime.envGuard` embeds `environment.Service` and overrides only `Delete`, which stops the environment's agents first. `process.sessionHandle` embeds a `terminal.Session` and adapts it into a `Handle`.

### Patterns you'll find

| Pattern | Where | Why |
|---------|-------|-----|
| Composition root and constructor injection | `runtime.NewModule`, `cmd/hive.newApp` | Everything is wired in one place; there are no globals, no `init()` and no setter injection |
| Module per package | `*.module.go` | `NewModule(cfg, deps…)` hides each package's internal wiring |
| Ports and adapters | `Store`/`FilesystemStore`, `Runner`/`execRunner`, `Factory`/`PTYFactory`, `ListenerFactory` | The domain doesn't know about disks, `os/exec` or PTYs |
| Repository | `environment.Store`, `process.Store` | Persistence and on-disk layout are owned by one type |
| Adapter | `protocol.Method[P, R]`, `sessionHandle` | Typed Go functions become wire handlers; PTY sessions become process handles |
| Decorator via embedding | `runtime.envGuard` | A cross-domain rule without coupling the domains |
| Observer (pub/sub) | `terminal.ptySession.Subscribe` | One PTY fans out to log file, history and any number of attached clients |
| Connection upgrade | `protocol.Response.Hijack` | Request/response and raw streaming share one socket |

### Conventions

- **One file per role:** `pkg.model.go` (types), `pkg.service.go` (contract and implementation), `pkg.store.go` (port), `pkg.filesystem_store.go` (adapter), `pkg.protocol.go` (wire handlers), `pkg.module.go` (wiring and package doc), `pkg.error.go`.
- **Comments explain why, not what.** Each package starts with a short doc comment, and non-obvious decisions are commented where they happen.
- **Minimal dependencies.** Bubble Tea and Lip Gloss (UI), `creack/pty` (terminals), cobra (CLI), go-toml (config), `x/sys`, and two small `charmbracelet/x` helpers. Everything else is the standard library. A new dependency needs an [ADR](docs/adr/).

## Contributing

```sh
make check      # vet, lint (pinned golangci-lint), race tests with goleak, go.mod tidiness
make fuzz       # run the fuzz targets (protocol framing, config parser)
make snapshot   # build release archives into ./dist
```

Tests sit next to the code or in a package's `tests/` directory. Fakes for `Store`, `Runner`, `Factory` and `client.Client` let most tests run without real processes. Integration tests start a real daemon on a temporary socket, and the CLI's end-to-end tests run the real binary path, autostarting a daemon in a scratch `HIVE_HOME`. Every package checks for leaked goroutines.

See [CONTRIBUTING.md](CONTRIBUTING.md) for coding standards and [docs/adr](docs/adr/) for design decisions. Issues and pull requests are welcome. Please keep changes small, follow the file conventions above, and include a test with each bug fix.

Found a security issue? Please follow [SECURITY.md](SECURITY.md) instead of opening a public issue.

## License

[Apache 2.0](LICENSE)
