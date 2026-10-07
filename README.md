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
- **See everything working.** One dashboard shows which agents are running, waiting, done or failed, so you don't have to open a dozen terminals.
- **Bring your own agent.** Claude Code, Codex, OpenCode, Cursor, your own agent, whatever comes next. Hive doesn't replace them; it gives them somewhere to live.
- **Agents that operate agents.** The same interface you use (creating workspaces, launching tasks, reading output) is available to agents themselves, so the infrastructure becomes part of the agent's toolkit.
- **Built for the background.** Long builds, large migrations, test suites, research and parallel tasks: work that takes longer than a terminal session.

## Status

Hive is **pre-release (v0)** and built in public. The wire protocol and on-disk layout may still change.

| Area | State |
|------|-------|
| Runtime daemon, unix-socket protocol, CLI | ✅ working |
| Environments (persistent workspaces) | ✅ working |
| Processes: plain or PTY-backed, lifecycle, logs, clean stop of the whole process tree | ✅ working |
| Terminal attach / detach, live input, resize | ✅ working |
| Dashboard TUI with live logs and interactive takeover | ✅ working |
| Crash recovery (stale records closed out on restart) | ✅ working |
| Agent state detection (*blocked*, *waiting for input*) | 🚧 next |
| Agent-facing API (agents managing agents) | 🗺 planned |
| Reconnect from another machine | 🗺 planned |
| Workspace isolation per agent (git worktrees), log rotation | 🗺 planned |
| Windows support | 🗺 planned |

## Quick start

Requires Go 1.26+ on macOS or Linux.

```sh
go build -o hive ./cmd/hive

./hive daemon &     # start the runtime (keeps agents alive)
./hive demo         # launch four demo agents
./hive              # open the dashboard
```

In the dashboard: `↑↓`/`tab` select an agent, `↵` takes control of its terminal (`esc` gives it back), `a` attaches full-screen, `s` stops it, and `q` detaches. Agents keep running.

### CLI

| Command | What it does |
|---------|--------------|
| `hive` / `hive ui` | Open the dashboard |
| `hive daemon` | Run the runtime in the foreground |
| `hive status` · `hive ping` | Inspect the runtime |
| `hive stop` | Stop the runtime and every agent it runs |
| `hive env list \| create <id> \| get <id> \| rm <id>` | Manage environments (removing one stops its agents first) |
| `hive ps start <env> [-t] [--] <cmd> [args…]` | Start a process (`-t` gives it a terminal) |
| `hive ps list [env] \| get <id> \| logs <id> [n] \| stop <id>` | Inspect and stop processes |
| `hive terminal attach <id>` | Attach to an agent's terminal; **Ctrl+]** detaches |
| `hive terminal input <id> <text>` · `resize <id> <w> <h>` | Type into / resize an agent's terminal |

Data lives in `~/.hive`. Set `HIVE_HOME` to use another directory, for example to run an isolated daemon for testing.

## Architecture

Hive is a single binary with two roles: a **daemon** that owns environments and agent processes, and **clients** (the CLI and the dashboard) that talk to it over a unix socket. Closing a client never touches an agent; only the daemon does.

```
cmd/hive ──────────── CLI composition root: builds one `app` (paths + client) and dispatches commands
 │
 ├── tui ──────────── dashboard; depends only on the client.Client contract
 ├── client ───────── one generic call() per request over protocol.Stream
 │        ╎
 │        ╎  unix socket · newline-delimited JSON
 │        ╎
 └── runtime ──────── daemon composition root: wires modules, serves the socket, owns agent lifetimes
       ├── process ── supervises agents: start, monitor, stop, recover, logs
       │     ├── environment ── persistent workspaces
       │     └── terminal ───── PTY sessions, live output fan-out
       └── protocol ─ framing, router, typed handlers           (leaf)
           jsonfile · pgroup ─ atomic JSON files, process-group signals (leaves)
```

Dependencies only point **down**. Leaf packages know nothing about Hive's domain, and no package imports one above it.

### Following a call from `main` to the OS

Every layer is reached through an explicit call, so you can read the code top to bottom:

```
hive ps start dev -t claude
  main.run                          cmd/hive/main.go      → builds app (newApp)
  procStart                         cmd/hive/cmd_process.go
  client.ProcessStart → call()      internal/client       → protocol.Stream.Send
  ─────────────── socket ───────────────
  runtime.Server.accept             internal/runtime      → protocol.Serve(conn)
  protocol.Router.Handle            internal/protocol     → "process.start"
  protocol.Method[StartRequest]     decodes params once, encodes the result
  process.service.Start             internal/process      → Environments.Get (port)
  process.service.launch            → terminal.Service.Open → PTYFactory.Open → creack/pty
  process.service.monitor           waits, then persists the final state via Store
```

### On disk

```
~/.hive/
  hive.sock                                  daemon socket (0700 directory)
  environments/<env>/
    environment.json
    workspace/                               where the env's agents run
    processes/<id>/
      process.json                           lifecycle record (written atomically)
      stdout.log  stderr.log                 output (PTY output goes to stdout.log)
```

### Wire protocol

One JSON object per line. Requests carry `version`, `type`, `id`, `method` and `params`. Each response carries the same `id` with either `result` or `error: {code, message}`. Error codes are `invalid_request`, `unknown_method`, `invalid_params`, `not_found` and `internal_error`.

| Namespace | Methods |
|-----------|---------|
| `runtime` | `ping`, `status`, `shutdown` |
| `environment` | `list`, `create`, `get`, `remove` |
| `process` | `start`, `list`, `get`, `logs`, `stop` |
| `terminal` | `attach`, `input`, `resize` |

`terminal.attach` *upgrades* the connection. After its reply, the socket carries raw terminal bytes in both directions. The server's reader is kept for the whole connection, so no byte that arrives right behind the reply is lost.

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
- **Minimal dependencies.** The direct dependencies are Bubble Tea and Lip Gloss (UI), `creack/pty` (terminals) and two small `charmbracelet/x` helpers that Bubble Tea already pulls in. Everything else is the standard library. New dependencies need a strong reason.

## Contributing

```sh
go vet ./...
go test -race ./...
```

Tests sit next to the code or in a package's `tests/` directory. Fakes for `Store`, `Runner`, `Factory` and `client.Client` let most tests run without real processes. Integration tests start a real daemon on a temporary socket.

Issues and pull requests are welcome. Please keep changes small, follow the file conventions above, and include a test with each bug fix.

Found a security issue? Please follow [SECURITY.md](SECURITY.md) instead of opening a public issue.

## License

[Apache 2.0](LICENSE)
