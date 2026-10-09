<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/e1dd814c-ed76-4d2d-823b-2a3046b07a7d">
    <img alt="Hive roadmap" src="https://github.com/user-attachments/assets/708cc3c2-5d35-4dbb-b1e3-35f4735d89be" width="100%">
  </picture>
</p>


# Roadmap

> **Hive** — AI infrastructure for always-working agents.  
> Site: [https://hive.admir-saheta.com](https://hive.admir-saheta.com)

Each milestone ships a tagged release and has clear acceptance criteria, so you always know when it's truly done. Work through them in order — M2 is up next. 🚀

---

## ✅ M0 · Foundations & Debt — `v0.2` — **LIVE**

Our first stop: tidy up the foundations and pay down some debt. It's not glamorous, but future-us will be grateful.

### 1. Tooling

- `.golangci.yml`, gofumpt, `Makefile`/`justfile` (`lint test race fuzz bench`)
- GitHub Actions on **macOS and Linux**, goreleaser snapshot builds, goleak

### 2. Logging — `internal/logging`

- `slog` plus a rotating file writer
- Say goodbye to the swallowed errors in `process.service.go:108,183`

### 3. Config — `internal/config`

- TOML at `~/.config/hive/config.toml` (overridable via `HIVE_CONFIG`)
- Defaults, and validation that warns and falls back instead of failing hard
- `hive config path | show | default`

### 4. CLI

- Move the CLI to **cobra**, keeping the command names you already know; add `hive completion`

### 5. Known bug fixes

- **TUI input ordering** — per-pane writer queue, so keystrokes land in order
- **Disk I/O under the lock** in `ptySession.publish`
- **Kill orphan process groups** in `Recover` and in `Stop` on stale records
- **Serve `stderr` logs**
- **Enforce the version check**
- **Remove the frame-cap risk** on `logs` by streaming it
- **Remove the unused `client.NewModule`**

### 6. Daemon lifecycle

- `hive` auto-starts the daemon detached (`Setsid`, logs to file) plus a pidfile — it should just work
- `hive daemon install` for launchd and systemd user units

### 7. Tests

- Cover the real `ptySession`, `pgroup`, `jsonfile` (add fsync), and `envGuard`

### 8. Spikes — timeboxed to 3 days each

- **(a)** charmbracelet/x/vt against the recorded Claude Code and Codex corpus
- **(b)** Bubble Tea v2 renderer stability and performance with 9 panes
- Write up both results as ADRs so everyone learns from them

**✅ Completed:** CI is green with race and goleak, the daemon auto-starts, no input reordering under a 1k keystrokes/s test, and both ADRs are merged.

---

## ✅ M1 · Terminal Core: VT + Shim + Protocol v2 — `v0.3` — **LIVE**

Now for the heart of the system: a rock-solid terminal core that survives anything you throw at it.

1. **`protocol` v2** — hello/welcome, a mux connection, events, streams, the binary codec; `client` moves to a persistent connection.
2. **`vt`** — port, adapter, a conformance corpus and golden tests, plus a scrollback ring with a byte budget.
3. **`shim`** — server and client. Processes launch through the shim, and the daemon reconnects to shims on start — your agents are never orphaned.
4. **`event` bus** — `events.subscribe` and `events_lost`; the TUI stops polling.
5. **Rendering** — render deltas, keyframe-on-lag policy, PTY resize from clients, and multi-client size arbitration (the last client to interact wins, as in herdr).

**✅ Completed:** `kill -9` the daemon, restart it, and every agent is still running with an identical screen. Reattaching to Claude Code shows a correct screen immediately. With 50 agents producing output and a throttled client, there's no corruption.

---

## M2 · Workspace Model — `v0.4` ← **START HERE**

Let's make it feel like home: named sessions, environments, and a flexible layout that adapts to how you work.

1. **`session`** — named namespaces (`--session`, `HIVE_SESSION`), each with its own socket and state dir.
2. **`environment`** — gains `Root` (an existing dir by default via `hive env create --cwd .`), env vars and git binding; tabs and panes come from `layout`.
3. **`layout` (BSP)** — split, close, zoom, swap, resize, move a pane across tabs and envs without a restart, popups, `layout.export`, `layout.apply`.
4. **`pane` API** — `list / get / split / focus / resize / zoom / swap / move / close / rename / input / send-text / send-keys / run / read (--source visible|recent|recent-unwrapped) / wait-output --regex --timeout`.
5. **`git`** — branch, ahead/behind and dirty state on a timer plus an fs watch; `worktree list|create|open|remove` under `~/.hive/worktrees/<repo>/<branch>`.
6. **Environment injection** — inject `HIVE_PANE_ID`, `HIVE_ENV_ID`, `HIVE_SOCKET_PATH`, `HIVE_BIN`, `TERM_PROGRAM=hive` into panes; strip the outer multiplexer's variables.

---

## M3 · Multiplexer TUI — `v0.5`

The fun part: a full multiplexer UI you'll enjoy living in — soon your daily tmux replacement.

1. **Compositor** — renders panes from deltas, with borders, active highlighting, the cursor, and pane titles from OSC.
2. **Sidebar & tabs** — Environments and Agents panels (collapsible; mobile single column at ≤64 columns), a tab bar, and the existing dashboard kept as an **Overview** mode (`prefix+O`).
3. **`keymap`** — prefix key (default `ctrl+b`, configurable, multiple prefixes), modes (terminal, prefix, navigate, resize, copy), `[keys]` in config, a filterable help overlay, and `hive config reset-keys`.
4. **Mouse** — focus, drag splits and the sidebar, wheel scroll into scrollback, select-to-copy, double-click for a word, ctrl-click URLs including OSC 8.
5. **Copy mode** — vim motions, `/` and `?` search with `n`/`N`, visual and line selection, yank to OSC 52 or the local clipboard; `edit_scrollback` opens `$EDITOR`.
6. **Pickers & prompts** — fuzzy Goto picker over agents and panes with state filters, toasts, rename prompts, confirm-close.
7. **Themes** — catppuccin, catppuccin-latte, tokyo-night, gruvbox, nord, `terminal`, plus `[theme.custom]` and auto light/dark. Pick your favorite.

**✅ Acceptance:** you can use Hive daily as a tmux replacement. `teatest` goldens cover each mode. Input latency stays under 10 ms p99 with 16 visible panes.

---

## M5 · Agents Operating Agents — `v0.7`

Now it gets really cool: agents driving other agents, through a clean CLI, a Go SDK, and MCP.

1. **Agent CLI** — `agent list | get | start --kind --env --worktree | prompt --wait --until idle|done --timeout | read | wait | send-keys | rename | focus | attach`
  - `prompt` is atomic, uses bracketed paste, refuses while the agent is blocked, and detects stalls.
  - `start --worktree` creates an isolated checkout per agent (a better default than herdr's).
2. **API surface** — `hive api schema`, `hive api snapshot`, and stable `--json` output on every command.
3. **Go SDK** — `pkg/hiveapi`, with examples in `examples/` (a fan-out reviewer, a test-fixer swarm).
4. **MCP** — `hive mcp` stdio server exposing the agent, pane and env tools, so Claude Code can drive it via `claude mcp add hive -- hive mcp`.
5. **Skill** — `skills/hive/SKILL.md`, served by `hive --skill`.

**✅ Acceptance:** a Claude Code instance inside Hive can spawn 3 Codex agents in worktrees, prompt them, wait for them and collect their output using only MCP. This runs as an e2e test against a fake agent binary.

---

## M6 · Persistence & Resume — `v0.8`

Reboots happen. Hive should shrug them off and pick up right where you left off.

1. **`persist`** — `session.json` snapshots, debounced on change, keeping the last 48 rotated, with backups of corrupt files and a schema version.
2. **Restore after host reboot** — envs, tabs, panes, cwd, layout and focus; agents resume through their native IDs (`claude --resume <id>`, `codex resume <id>`) with a staggered start (`session.resume_agents_on_restore`).
3. **Pane history replay** — optional, off by default, with secret-safety docs.
4. **Shutdown safety** — Linux logind delay inhibitor; on macOS, snapshot on `NSWorkspaceWillPowerOff` via a signal path, or on a timer.
5. **Housekeeping** — log rotation, retention config, `hive gc`.
6. **Self-update** — `hive update` swaps the binary and restarts the daemon; shims keep running, giving zero-downtime upgrades.

**✅ Acceptance:** reboot the VM and all 10 Claude and Codex panes are restored and resumed. Upgrading during active output loses no bytes.

---

## M7 · Remote & Multi-Machine — `v0.9`

Go beyond one machine: SSH in and manage a whole fleet of agents from wherever you are.

1. **SSH transport** — `client.Dial` over SSH; `ssh host hive proxy --session X` acts as a stdio bridge, with ControlMaster and keepalives configured.
2. **Bootstrap** — `hive --remote host` checks the remote version and installs or updates it after confirmation (copying its own binary when the OS and architecture match).
3. **Saved machines** — `machine add | list | rename | enable | disable | remove | status | reconnect`; the sidebar merges agents from every machine, and `--machine` forwards API calls.
4. **Bandwidth** — deflate on the frame stream, a per-client frame budget, and coalescing driven by the RTT the client reports; target under 50 MB/h per busy pane.
5. **Remote clipboard** — OSC 52 over remote. The image paste bridge arrives later (post-1.0).

**✅ Acceptance:** 5 remote machines and 40 agents run for 8 hours without growth in latency or memory (soak test).

---

## M8 · Extensibility — `v0.10`

Make Hive yours: plugins, custom commands, and hooks for whatever your workflow needs.

1. **`plugin`** — a `hive-plugin.toml` manifest with id, version, `min_hive_version`, platforms, `[[startup]]`, `[[actions]]`, `[[events]]`; install from git with a trust preview, plus link, enable, disable and uninstall; `HIVE_PLUGIN_*` env and a context JSON are injected.
2. **Custom commands** — `[[keys.command]]` (popup, pane, shell, plugin\_action).
3. **Metadata** — `report-metadata` tokens and title in the sidebar, with TTL.
4. **Status bar** — right area: text, datetime, hostname, and a command on an interval.

**✅ Acceptance:** two reference plugins (a "PR status" sidebar token and "spawn reviewer on worktree.created") work end to end.

---

## M9 · Hardening → `v1.0.0`

The final stretch: freeze the APIs, polish the rough edges, and ship a 1.0 we can all be proud of. 🎉

1. **API freeze** — audit `api/v1`, `pkg/hiveapi` and the config keys; write a compatibility policy doc.
2. **Performance** — run the benchmark suite; reach 100 panes with p99 frame under 16 ms and idle CPU under 1 %; profile with pprof, available behind `hive debug pprof`.
3. **Reliability** — fuzzers run 24 h in nightly CI, a 72 h chaos soak (kill shims and the daemon, drop the network), and fd and goroutine leak checks.
4. **Security** — socket perms 0600 with peer-cred UID checks (`SO_PEERCRED`/`getpeereid`), signed manifest and update verification, a plugin trust prompt, `gosec`, and an updated `SECURITY.md`.
5. **Distribution** — goreleaser for darwin and linux on amd64 and arm64, `install.sh`, a Homebrew tap, a Nix flake, `hive update`, stable/preview channels.
6. **Docs** — quick start, concepts, keybindings, the config reference generated from `config` structs, the API reference generated from the schema, agent guides, and the herdr→Hive migration guide (including importing herdr config and keys).
7. **RC cycle** — `v1.0.0-rc.1` … `rc.N`, with 2 weeks of no P0/P1 bugs, then **v1.0.0**.

---

## Post-1.0 — Later

Once 1.0 is out the door, here's what's on the wishlist:

- **Windows** — ConPTY via `platform/windows`
- **Kitty graphics passthrough**
- **Image paste bridge**
- **Plugin panes and link handlers**
- **Plugin marketplace index**
- **Web and mobile observer UI** — built on `terminal session observe`
- **mDNS machine discovery**
- **Hosted offering**
