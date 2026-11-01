<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/e1dd814c-ed76-4d2d-823b-2a3046b07a7d">
    <img alt="Hive roadmap" src="https://github.com/user-attachments/assets/708cc3c2-5d35-4dbb-b1e3-35f4735d89be" width="100%">
  </picture>
</p>


# Roadmap

> **Hive** — AI infrastructure for always-working agents.  
> Site: [https://hive.admir-saheta.com](https://hive.admir-saheta.com)

Each milestone ships a tagged release and has clear acceptance criteria, so you always know when it's truly done. Hive 1.0 is in release candidates (`v1.0.0-rc.1`); the 1.0 train (M10–M19) is on `main` and ships as `v1.0.10`. M20–M30 are the proposed 1.x train, one minor release each, open for comment in [RFC 0001](rfcs/0001-after-1.0.10.md). 🚀

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

## M2 · Workspace Model — `v0.4`  — **LIVE**

Let's make it feel like home: named sessions, environments, and a flexible layout that adapts to how you work.

1. **`session`** — named namespaces (`--session`, `HIVE_SESSION`), each with its own socket and state dir.
2. **`environment`** — gains `Root` (an existing dir by default via `hive env create --cwd .`), env vars and git binding; tabs and panes come from `layout`.
3. **`layout` (BSP)** — split, close, zoom, swap, resize, move a pane across tabs and envs without a restart, popups, `layout.export`, `layout.apply`.
4. **`pane` API** — `list / get / split / focus / resize / zoom / swap / move / close / rename / input / send-text / send-keys / run / read (--source visible|recent|recent-unwrapped) / wait-output --regex --timeout`.
5. **`git`** — branch, ahead/behind and dirty state on a timer plus an fs watch; `worktree list|create|open|remove` under `~/.hive/worktrees/<repo>/<branch>`.
6. **Environment injection** — inject `HIVE_PANE_ID`, `HIVE_ENV_ID`, `HIVE_SOCKET_PATH`, `HIVE_BIN`, `TERM_PROGRAM=hive` into panes; strip the outer multiplexer's variables.

---

## M3 · Multiplexer TUI — `v0.5`  — **LIVE**

The fun part: a full multiplexer UI you'll enjoy living in — soon your daily tmux replacement.

1. **Compositor** — renders panes from deltas, with borders, active highlighting, the cursor, and pane titles from OSC.
2. **Sidebar & tabs** — Environments and Agents panels (collapsible; mobile single column at ≤64 columns), a tab bar, and the existing dashboard kept as an **Overview** mode (`prefix+O`).
3. **`keymap`** — prefix key (default `ctrl+b`, configurable, multiple prefixes), modes (terminal, prefix, navigate, resize, copy), `[keys]` in config, a filterable help overlay, and `hive config reset-keys`.
4. **Mouse** — focus, drag splits and the sidebar, wheel scroll into scrollback, select-to-copy, double-click for a word, ctrl-click URLs including OSC 8.
5. **Copy mode** — vim motions, `/` and `?` search with `n`/`N`, visual and line selection, yank to OSC 52 or the local clipboard; `edit_scrollback` opens `$EDITOR`.
6. **Pickers & prompts** — fuzzy Goto picker over agents and panes with state filters, toasts, rename prompts, confirm-close.
7. **Themes** — catppuccin, catppuccin-latte, tokyo-night, gruvbox, nord, `terminal`, plus `[theme.custom]` and auto light/dark. Pick your favorite.

**✅ Acceptance:** you can use Hive daily as a tmux replacement. Harness goldens (`internal/tui/mux/testdata`) cover each mode. Input latency stays under 10 ms p99 with 16 visible panes.

---

## M4 · Agent Intelligence — `v0.6` - **LIVE**

Hive learns what your agents are doing, so you only look when one needs you.

1. **`agent` engine** — process detection, the manifest engine (regions and matchers), report overrides with TTL, the state machine (working ⇄ blocked → done → idle), rollups and seq counters.
2. **Manifests** — Claude Code, Codex, OpenCode, Gemini, Cursor Agent, Copilot CLI, Amp and Aider, each with recorded-screen fixtures in tests.
3. **Tooling** — `hive agent list | get | explain | manifests`, `hive server reload-agent-manifests`, user manifests in `~/.config/hive/agent-detection/`.
4. **Integrations** — `hive integration install | uninstall | status <agent>`: Claude `settings.json` and Codex `hooks.json` hooks call `hive pane report-agent`. Writes are idempotent, back up the original file, and are fully reversible.
5. **`notify`** — toasts, terminal notifications (OSC 9/777, which work over SSH), system notifications (osascript or notify-send) and sounds for done and blocked, configurable per agent and never for the focused pane.
6. **UI** — sidebar state icons, the Overview's running/awaiting/complete counts from real states, and `next-agent`, `focus-agent-N`, `open-notification-target`.

**✅ Acceptance:** on the fixture corpus, classification is at least 98 % correct for the top 4 agents. A blocked prompt raises a notification within 500 ms. 100 agents at idle use under 1 % CPU.

---

## M5 · Agents Operating Agents — `v0.7` - **LIVE**

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

## M6 · Persistence & Resume — `v0.8`  — **LIVE**

Reboots happen. Hive should shrug them off and pick up right where you left off.

1. **`persist`** — `session.json` snapshots, debounced on change, keeping the last 48 rotated, with backups of corrupt files and a schema version.
2. **Restore after host reboot** — envs, tabs, panes, cwd, layout and focus; agents resume through their native IDs (`claude --resume <id>`, `codex resume <id>`) with a staggered start (`session.resume_agents_on_restore`).
3. **Pane history replay** — optional, off by default, with secret-safety docs.
4. **Shutdown safety** — Linux logind delay inhibitor; on macOS, launchd's SIGTERM at shutdown, and a snapshot every 30 s.
5. **Housekeeping** — log rotation, retention config, `hive gc`.
6. **Self-update** — `hive update` swaps the binary and restarts the daemon; shims keep running, giving zero-downtime upgrades.

**✅ Acceptance:** reboot the VM and all 10 Claude and Codex panes are restored and resumed. Upgrading during active output loses no bytes.

---

## M7 · Remote & Multi-Machine — `v0.9` - **LIVE**

Go beyond one machine: SSH in and manage a whole fleet of agents from wherever you are.

1. **SSH transport** — `client.Dial` over SSH; `ssh host hive proxy --session X` acts as a stdio bridge, with ControlMaster and keepalives configured.
2. **Bootstrap** — `hive --remote host` checks the remote version and installs or updates it after confirmation (copying its own binary when the OS and architecture match).
3. **Saved machines** — `machine add | list | rename | enable | disable | remove | status | reconnect`; the sidebar merges agents from every machine, and `--machine` forwards API calls.
4. **Bandwidth** — deflate on the frame stream, a per-client frame budget, and coalescing driven by the RTT the client reports; target under 50 MB/h per busy pane.
5. **Remote clipboard** — OSC 52 over remote. The image paste bridge arrives later (post-1.0).

**✅ Acceptance:** 5 remote machines and 40 agents run for 8 hours without growth in latency or memory (soak test).

---

## M8 · Extensibility — `v0.10` - **LIVE**

Make Hive yours: plugins, custom commands, and hooks for whatever your workflow needs.

1. **`plugin`** — a `hive-plugin.toml` manifest with id, version, `min_hive_version`, platforms, `[[startup]]`, `[[actions]]`, `[[events]]`; install from git with a trust preview, plus link, enable, disable and uninstall; `HIVE_PLUGIN_*` env and a context JSON are injected.
2. **Custom commands** — `[[keys.command]]` (popup, pane, shell, plugin\_action).
3. **Metadata** — `report-metadata` tokens and title in the sidebar, with TTL.
4. **Status bar** — right area: text, datetime, hostname, and a command on an interval.

**✅ Acceptance:** two reference plugins (a "PR status" sidebar token and "spawn reviewer on worktree.created") work end to end.

---

## M9 · Hardening → `v1.0.0` — **LIVE**

The final stretch: freeze the APIs, polish the rough edges, and ship a 1.0 we can all be proud of. 🎉

1. **API freeze** — audit `api/v1`, `pkg/hiveapi` and the config keys; write a compatibility policy doc.
2. **Performance** — run the benchmark suite; reach 100 panes with p99 frame under 16 ms and idle CPU under 1 %; profile with pprof, available behind `hive debug pprof`.
3. **Reliability** — every fuzzer runs 5 h in nightly CI (the hosted runners' limit), a chaos soak (5 h nightly; the 72 h run is dispatched by hand on a self-hosted runner) (kill shims and the daemon, drop the network), and fd and goroutine leak checks.
4. **Security** — socket perms 0600 with peer-cred UID checks (`SO_PEERCRED`/`getpeereid`), signed manifest and update verification, a plugin trust prompt, `gosec`, and an updated `SECURITY.md`.
5. **Distribution** — goreleaser for darwin and linux on amd64 and arm64, `install.sh`, a Homebrew tap, a Nix flake, `hive update`, stable/preview channels.
6. **Docs** — quick start, concepts, keybindings, the config reference generated from `config` structs, the API reference generated from the schema, agent guides, and the herdr→Hive migration guide (including importing herdr config and keys).
7. **RC cycle** — `v1.0.0-rc.1` … `rc.N`, with 2 weeks of no P0/P1 bugs, then **v1.0.0**.

---

## M10 · Plugin App Store — `v1.0.1` - **LIVE**

**Status:** the CLI side is done; publishing the examples and the
storefront's checks remain (below).

### Extend Hive. Make it yours.

The Plugin App Store is the first post-1.0 feature. It builds on Hive’s existing
Git-based plugin system: the CLI remains able to install and link plugins from
repositories, while the store provides a trustworthy place to discover,
evaluate, install, update, and remove them.

1. **Store experience**
   - Build a searchable plugin catalog with categories, featured collections,
     official plugins, and recently updated releases.
   - Provide dedicated plugin detail pages with descriptions, screenshots,
     installation instructions, configuration examples, and changelogs.
   - Add filters for agent integrations, workflow automation, developer tools,
     notifications, and terminal enhancements.
   - Show compatibility badges for Hive versions, supported operating systems,
     and plugin API versions.
   - Clearly identify verified maintainers, official plugins, deprecated
     releases, and archived projects.
   - Link directly to source repositories, documentation, issue trackers,
     licenses, and release histories.
   - Integrate the responsive discovery experience into the Hive website.

2. **Registry and plugin records**
   - Define a versioned registry schema for plugin metadata, releases,
     maintainers, and compatibility.
   - Surface plugin ID, version, author or organization, license, repository,
     description, and supported platforms.
   - Record minimum and maximum supported Hive versions where applicable.
   - Keep release metadata, source revisions, checksums, and publication
     timestamps immutable.
   - Support multiple maintainers and explicit ownership-transfer workflows.
   - Preserve historical release metadata when new versions are published.
   - Validate registry records before publication and reject malformed or
     conflicting entries.

3. **Hive CLI integration**
   - Add `hive plugin search`, `hive plugin info`, and `hive plugin install <id>`.
   - Support `hive plugin update [id]`, `hive plugin remove <id>`, and
     `hive plugin list`.
   - Keep `hive plugin install <git-url>` and `hive plugin link` available for
     private and local development.
   - Resolve plugin IDs to registry releases without confusing registry
     packages with arbitrary Git repositories.
   - Display installation progress, compatibility warnings, and actionable
     errors.
   - Support non-interactive installation for scripts, with explicit security
     policies.
   - Keep the existing CLI usable when the registry is unavailable.

4. **Package and manifest contract**
   - Finalize the versioned `hive-plugin.toml` manifest and package format.
   - Define immutable release references, checksums, supported platforms, and
     minimum Hive versions.
   - Validate declared commands, actions, events, startup hooks, and required
     capabilities.
   - Define compatibility and migration rules for manifest changes.
   - Reject releases whose manifest ID does not match the registry listing.
   - Specify package size limits, download limits, and extraction safeguards.
   - Document how plugins declare configuration, data directories, and runtime
     requirements.

5. **Installation, updates, and rollback**
   - Preview plugin identity, source, version, requested capabilities, and
     installation actions before execution.
   - Verify package integrity before installing or updating a plugin.
   - Install atomically so an interrupted operation cannot leave a partially
     active version.
   - Preserve the last known-good version during upgrades.
   - Support rollback after a failed upgrade or explicit user request.
   - Never delete plugin data without explicit confirmation.
   - Detect interrupted downloads, corrupted archives, missing dependencies,
     and incompatible versions.
   - Maintain a local installation record with version, source, checksum, and
     installation time.

6. **Open-source submissions and governance**
   - Establish a documented submission process for public plugin repositories.
   - Require valid manifests, license information, maintainer metadata,
     documentation, and reproducible release references.
   - Introduce submission states: draft, submitted, changes requested, approved,
     published, deprecated, and removed.
   - Maintain an auditable review history for listing changes and release
     approvals.
   - Automate manifest validation, compatibility checks, license checks,
     checksum verification, and available security scanning.
   - Require human review for official or verified listings.
   - Publish moderation policies, reporting procedures, and an appeals process.
   - Allow maintainers to withdraw or archive plugins without silently rewriting
     historical release records.

7. **Initial catalog and developer experience**
   - Publish official example plugins covering PR status, reviewer orchestration,
     CI status, and issue tracking.
   - Introduce `hive plugin init` to scaffold a valid plugin.
   - Document local linking, development mode, test fixtures, event and action
     contracts, and release publishing.
   - Provide a sample repository and automated validation workflow.
   - Document ownership transfer, maintainer retirement, security disclosures,
     and version deprecation.
   - Add a registry contribution guide for community maintainers.

**Exit criteria:** a user can discover a plugin on the Hive website, inspect its
source and requirements, install it by ID, update or roll it back safely, and
remove it. An external maintainer can submit an open-source plugin through a
documented, auditable publishing process. Registry outages do not break locally
installed plugins or Git-based installation.

### Performance: Zig over cgo for heavy-duty work

Catalog hot paths (release checksum streaming, archive digests, large
registry JSON) stay pure Go unless profiling proves a bottleneck. When one
is proven, implement it as a small Zig module exposed over the C ABI and
linked into the Go binary with cgo to optimize performance: one narrow
package owns every `import "C"`, the cgo build stays opt-in
(`CGO_ENABLED=1`), and the default pure-Go build keeps cross-compiling to a
single static binary, so the goreleaser matrix, installers, and the
checksum-verified plugin transport are unaffected. Zig cross-compiles every
target from one machine, which keeps the release flow reproducible.

### M10 remaining work (implementation audit, 2026-10-14)

- [x] Add registry-backed `hive plugin update [id]` and explicit rollback while
  retaining the last known-good release and plugin data.
- [x] Make interrupted updates recoverable and verify installed release
  metadata/checksums against the registry.
- [x] Define and implement maintainer ownership transfer, submission states,
  review history, moderation, reporting, appeals, and withdrawal workflows.
- [x] Add publication-side registry validation and an automated submission
  workflow; require human approval for official and verified listings.
- [x] Complete manifest configuration/data-directory declarations and
  compatibility/migration rules; document download and extraction limits.
  Settings use a versioned JSON file, sequential staged migrations, separate
  persistent config/data paths, and declared Hive version bounds.
- [x] Add `hive plugin validate [dir]` as a local manifest/runtime validation
  entry point for contributors and CI.
- [x] Add `hive plugin init`, expand validation fixtures, and finish
  release/submission documentation and automation.
- [x] Align storefront validation and registry records with the CLI contract.
- [ ] Publish and seed official catalog releases. auto-tab-title and
  hive-explorer are listed (`registry/official-releases.json`, tags
  `v0.1.0` and `v0.2.0`); the examples (`pr-status`, `spawn-reviewer`) are
  not, and CI-status and issue-tracking examples do not exist yet.
- [x] The plugin TUI extension claim: contributed commands, views and
  keybindings are wired through the core since M12 (`hive plugin views`,
  pane views, `docs/plugins/api.md`).
- [x] Non-interactive install policy, progress/errors and registry outage
  behaviour in the CLI (`--yes`, policy denial, `TestM10*` outage cases).
- [ ] The same checks in the storefront (hive.admir-saheta.com, not in
  this repository).

---

## M11 · Visual Terminal Workflows — `v1.0.3` — **LIVE**

Give agents the context they need.

Design, resource bounds, terminal compatibility and measurements:
[docs/plugins/image-graphics.md](docs/plugins/image-graphics.md).

Coding agents increasingly work with screenshots, diagrams, browser captures,
and other visual inputs. Hive should make these workflows possible without
compromising terminal rendering, transport reliability, or resource limits.

1. **Image and graphics support**
   - Evaluate Kitty graphics protocol support against the existing terminal
     rendering architecture.
   - Add graphics passthrough where supported by the terminal and agent
     integration.
   - Implement an image paste bridge for supported local workflows.
   - Preserve ordinary terminal behavior when graphics capabilities are
     unavailable.
   - Define how graphical sequences interact with scrollback, pane resizing,
     and screen restoration.
   - Add explicit feature detection for terminals that support different
     graphics protocols.

2. **Image transfer and remote sessions**
   - Support bounded image transfer across remote sessions where the transport
     permits it.
   - Avoid forwarding large image payloads through control messages intended
     for ordinary terminal events.
   - Introduce configurable image-size and payload limits.
   - Handle interrupted transfers and temporary transport failures.
   - Ensure remote image workflows respect the capabilities of the destination
     terminal and agent.
   - Document limitations involving SSH, clipboard permissions, and terminal
     emulators.

3. **Resource and security safeguards**
   - Validate incoming image metadata and payload sizes.
   - Reject malformed graphics sequences safely.
   - Bound memory usage for image buffers and cached assets.
   - Prevent image transfer from starving ordinary terminal input and control
     traffic.
   - Avoid persisting image data in session history unless explicitly
     configured.
   - Document sensitive image and clipboard data handling.

4. **Testing and compatibility**
   - Add fixtures for image paste, graphics sequences, malformed payloads, and
     interrupted transfers.
   - Test local and remote workflows independently.
   - Measure transfer overhead and rendering responsiveness.
   - Provide text-only fallbacks for unsupported terminals.
   - Document supported terminal emulators and protocol limitations.

**✅ Acceptance:** a supported local or remote workflow can deliver an image to
a compatible agent without corrupting terminal state, exceeding configured
resource limits, or disrupting unrelated panes. Unsupported terminals continue
to work normally.

---

## M12 · Plugin Runtime Surfaces — `v1.0.2` — **LIVE**

Plugins become first-class citizens.

Plugin API 1 reference (versions, capabilities and the security boundary,
contributions, lifecycle, configuration): [docs/plugins/api.md](docs/plugins/api.md).

Hive’s initial plugin system provides commands, events, startup actions, and
status-bar metadata. This milestone expands the extension model so plugins can
contribute meaningful workflows without coupling themselves to internal runtime
implementation details.

1. **Stable extension API**
   - Publish a versioned plugin API with explicit compatibility guarantees.
   - Define stable contracts for lifecycle events, environment changes, pane
     operations, agent state transitions, and workspace changes.
   - Introduce capability discovery so plugins can determine which APIs are
     available.
   - Define synchronous and asynchronous execution semantics for plugin actions.
   - Establish event ordering, delivery guarantees, timeouts, and failure
     behavior.
   - Support graceful handling of unknown events and unsupported API versions.
   - Add contract tests that run against every supported plugin API version.

2. **Plugin-owned interface contributions**
   - Support structured sidebar cards and additional status indicators.
   - Allow plugins to register contextual actions for agents, panes, sessions,
     and workspaces.
   - Add plugin-provided links and command-palette entries.
   - Support configuration forms generated from declared plugin settings.
   - Define a restricted interface contribution format that does not require
     arbitrary modifications to the core TUI.
   - Ensure terminal resizing and narrow layouts remain usable when plugins
     contribute UI elements.
   - Make plugin contributions discoverable and individually configurable.

3. **Capability and permission model**
   - Define explicit capabilities for filesystem access, process execution,
     network access, and Hive API operations.
   - Separate capabilities required at installation from optional capabilities
     requested during execution.
   - Display permission changes before plugin installation and upgrades.
   - Provide configurable allowlists and deny rules.
   - Avoid implying that manifest declarations alone provide operating-system-
     level isolation.
   - Document the security boundary for in-process and external-process plugins.
   - Provide stricter execution options for plugins that can run as isolated
     subprocesses.

4. **Lifecycle and health management**
   - Track plugin startup, readiness, errors, and shutdown.
   - Add per-plugin logs and diagnostics.
   - Detect repeated crashes and apply configurable restart limits.
   - Prevent failed plugins from blocking the core runtime.
   - Add timeouts and cancellation for plugin actions.
   - Provide `hive plugin doctor` for common installation and compatibility
     issues.
   - Support controlled plugin reloads where the implementation permits safe
     reinitialization.

5. **Configuration and persistent state**
   - Introduce documented configuration schemas and validation.
   - Separate plugin configuration, cache, and persistent data directories.
   - Support configuration migration between plugin versions.
   - Define behavior for missing, invalid, and obsolete configuration.
   - Preserve plugin data when uninstalling unless the user explicitly requests
     deletion.
   - Provide export and backup options for plugin configuration.

**✅ Acceptance:** reference plugins can contribute sidebar metadata,
contextual actions, and workflow integrations using documented interfaces. A
failed plugin cannot crash the Hive daemon, and users can inspect plugin health,
configuration, and declared capabilities.

---

## M13 · Windows Foundation — `v1.0.4` — **LIVE**

Bring Hive to Windows.

Support matrix, Windows installation, how Windows differs, agent and shell
expectations, and tracked limitations: [docs/platforms.md](docs/platforms.md).
CI runs every change on `windows-latest`.

Windows support should extend Hive’s existing runtime model rather than
introduce a separate, incompatible implementation.

1. **Platform architecture**
   - Introduce Windows implementations (`*_windows.go` in `internal/platform` and `internal/pgroup`) for process management,
     terminal sessions, sockets, and platform-specific lifecycle operations.
   - Integrate ConPTY for pseudoterminal support.
   - Define Windows-specific runtime directories, configuration paths, logging,
     and process ownership.
   - Preserve shared protocol, workspace, agent, and plugin contracts wherever
     practical.
   - Document platform-specific differences instead of hiding incompatible
     behavior.

2. **Runtime and terminal support**
   - Support terminal creation, input, output, resize, and cleanup through
     ConPTY.
   - Handle child-process lifecycle and process-tree termination correctly.
   - Implement appropriate interprocess communication for the Windows runtime.
   - Support daemon or background-runtime operation through a suitable Windows
     lifecycle model.
   - Define restart and session-restoration behavior.
   - Handle Windows paths, environment variables, executable discovery, and
     command-line quoting.
   - Validate terminal resizing, Unicode, and control-sequence handling.

3. **Distribution and updates**
   - Add Windows amd64 CI coverage and establish an explicit architecture
     support policy.
   - Produce signed or verifiable release artifacts where distribution
     infrastructure permits.
   - Implement installation, upgrades, and removal procedures.
   - Integrate Windows-compatible self-update and rollback behavior.
   - Document installation prerequisites and common troubleshooting steps.

4. **Agent compatibility**
   - Test supported coding agents and shells on Windows.
   - Define compatibility expectations for PowerShell, Command Prompt, and
     supported terminal environments.
   - Document limitations for Unix-only agent dependencies and shell scripts.
   - Ensure plugin platform constraints are enforced during installation.

5. **Release quality**
   - Add automated runtime, protocol, session, and terminal tests on Windows.
   - Test shutdown, restart, orphan-process cleanup, and interrupted upgrades.
   - Establish a platform support matrix for macOS, Linux, and Windows.
   - Track Windows-specific limitations independently from shared runtime
     defects.

**✅ Acceptance:** the documented core Hive workflow—runtime lifecycle,
terminal multiplexing, supported agents, session persistence, plugin
management, and upgrades—works on supported Windows versions with automated
CI coverage.

---

## M14 · Observer API — `v1.0.5` — **LIVE**

Observe without taking control.

Credentials, the observer socket, methods, events and resynchronisation,
limits, remote observers and the security model:
[docs/observers.md](docs/observers.md). The API is frozen in
`api/v1/observer.txt`; examples are in `examples/observer-dashboard` and
`examples/observer-notifier`.

Introduce a read-only interface for applications that need to inspect Hive
sessions without gaining the ability to type into agents or change runtime
state.

1. **Read-only API design**
   - Define an explicit observer API for runtime health, sessions, workspaces,
     agents, and pane metadata.
   - Provide screen snapshots and bounded terminal output where permitted.
   - Expose structured events for agent-state changes, notifications, and
     session lifecycle.
   - Support event subscriptions, sequence numbers, and detection of lost
     events.
   - Provide snapshot recovery after client reconnection.
   - Define pagination and retention limits for historical events.
   - Publish an API schema and compatibility policy.

2. **Authentication and authorization**
   - Introduce scoped observer credentials with explicit expiry and revocation.
   - Separate observation permissions from interactive control permissions.
   - Allow access to specific sessions, workspaces, or panes.
   - Avoid exposing unrelated terminal contents, environment variables, or
     secret-bearing metadata.
   - Apply authentication consistently across local and remote observer
     connections.
   - Audit observer credential creation, revocation, and use where applicable.

3. **Transport and streaming**
   - Support incremental updates without requiring clients to poll every pane
     continuously.
   - Reuse existing protocol mechanisms where suitable.
   - Apply bounded queues, backpressure, and snapshot resynchronization.
   - Define reconnect behavior and stale-client cleanup.
   - Protect the runtime from slow or disconnected observers.
   - Ensure observers cannot monopolize resources used by interactive clients.

4. **API tooling**
   - Add `hive observer` commands for creating, inspecting, revoking, and
     validating observer access.
   - Provide example clients and integration tests.
   - Document screen snapshot formats and event semantics.
   - Include examples for dashboards and notification services.
   - Add negative tests proving that read-only credentials cannot invoke
     control operations.

5. **Security verification**
   - Test unauthorized session access and expired credentials.
   - Test revoked credentials against existing connections.
   - Verify that observer scopes cannot be widened by clients.
   - Ensure snapshots and events respect session boundaries.
   - Document the difference between read-only API access and operating-system
     process isolation.

**✅ Acceptance:** an authenticated observer can follow explicitly authorized
sessions and receive screen and event updates, but cannot send terminal input,
alter layouts, launch processes, or inspect unrelated sessions.

---

## M15 · Web Observer — `v1.0.6` — **LIVE**

Check on your agents from anywhere.

Setup, sharing and access, the app, privacy, deployment (TLS, reverse
proxies, health checks), compatibility and limits:
[docs/web.md](docs/web.md). Built in Go with
[gomponents](https://www.gomponents.com/); browser tests drive headless
Chrome in CI.

Build a browser-based companion around the Observer API. The initial experience
is intentionally read-only: users can inspect agent progress without turning
Hive’s remote API into an unrestricted remote terminal.

1. **Web application**
   - Provide a responsive dashboard for authorized Hive runtimes.
   - Display machines, workspaces, sessions, agents, and pane status.
   - Show selected terminal snapshots and bounded output history.
   - Surface working, blocked, completed, and idle states where the runtime can
     identify them reliably.
   - Add filtering and search across authorized sessions.
   - Provide session detail pages with activity and relevant metadata.
   - Support dark and light themes with accessible contrast and keyboard
     navigation.

2. **Live updates**
   - Stream observer events into the browser without continuous full-screen
     polling.
   - Recover from dropped connections using fresh snapshots.
   - Display connection state, stale data, and last successful synchronization.
   - Prevent old events from overwriting newer state.
   - Bound client-side buffers and stop unnecessary subscriptions when views are
     inactive.
   - Support reconnecting after browser suspension or network changes.

3. **Sharing and access**
   - Require explicit user authorization before exposing a runtime to a browser
     client.
   - Provide scoped sharing links where supported by the authentication model.
   - Support expiry and revocation.
   - Avoid embedding long-lived credentials in URLs.
   - Add access and revocation audit events.
   - Provide a clear indicator when a session is being observed.
   - Keep control capabilities unavailable in the observer interface.

4. **Deployment**
   - Support self-hosted deployment with documented configuration.
   - Define TLS and authentication requirements.
   - Keep the observer frontend separate from privileged runtime operations.
   - Document reverse-proxy and network deployment patterns.
   - Provide health checks and deployment troubleshooting.
   - Establish supported browser and runtime API compatibility.

5. **Privacy and performance**
   - Avoid collecting terminal contents or usage telemetry beyond configured
     requirements.
   - Make session history access explicit.
   - Avoid persisting sensitive terminal output in browser storage by default.
   - Add browser tests for session switching, revocation, reconnects, and
     restricted access.
   - Measure responsiveness with multiple active sessions and slow connections.

**✅ Acceptance:** a user can open Hive in a browser, authenticate, inspect
authorized agent activity, and follow live updates. Revoking access prevents
further observation, and the web client has no ability to control sessions
through the read-only API.

---

## M16 · Mobile Observer — `v1.0.7` — **LIVE**

Your agents don’t stop when you leave your desk.

An installable web app (PWA) on the web observer, with Web Push notifications
sent directly and end-to-end encrypted, deep links, and phone layouts tested
in emulated iPhone and Pixel browsers; why not native apps (yet), delivery
limits and the device checklist: [docs/mobile.md](docs/mobile.md).

Build on the Web Observer to make Hive’s read-only monitoring experience useful
on phones and tablets. Prioritize actionable information over attempting to
reproduce a full terminal interface on a small screen.

1. **Mobile-first experience**
   - Introduce compact machine, workspace, and agent views.
   - Optimize session cards for working, blocked, completed, and idle states.
   - Provide readable terminal snapshots with horizontal overflow handling.
   - Add quick filters for agents requiring attention.
   - Support accessible touch targets, dynamic text sizing, and screen readers.
   - Preserve navigation context when switching between sessions.
   - Provide clear offline and reconnecting states.

2. **Notifications**
   - Support notification links to specific machines, sessions, and panes.
   - Surface important transitions such as an agent becoming blocked or
     completing a task.
   - Allow per-session and per-event notification preferences.
   - Deduplicate repeated notifications for the same underlying event.
   - Respect revoked observer access and expired sessions.
   - Avoid sending terminal contents or secrets in notification payloads.
   - Document delivery limitations and platform-specific permissions.

3. **Session review**
   - Display recent activity and relevant agent metadata.
   - Provide bounded terminal output for reviewing progress.
   - Make stale snapshots and unavailable runtimes obvious.
   - Allow users to follow an agent without accidentally issuing input.
   - Support reconnecting to the same session from notification deep links.
   - Provide controls for muting noisy sessions and managing observer
     subscriptions.

4. **Platform strategy**
   - Begin with a responsive web application or installable PWA.
   - Evaluate native mobile applications only when push notifications,
     background behavior, or platform integration justify the additional
     maintenance.
   - Share observer API contracts and reusable client logic.
   - Test on supported mobile browsers and screen sizes.

5. **Privacy and reliability**
   - Do not cache sensitive terminal content indefinitely.
   - Respect system notification privacy settings.
   - Test expired credentials, revoked links, suspended tabs, and interrupted
     connections.
   - Ensure mobile clients remain strictly read-only.

**✅ Acceptance:** a user can review agent status from a phone, receive
configured notifications where supported, and navigate directly to a blocked
or completed session without receiving unintended control permissions.

---

## M17 · Local Discovery — `v1.0.8` — **LIVE**

Find your machines. Trust them explicitly.

Opt-in mDNS announcements (DNS-SD, `_hive._tcp`) from the daemon, a runtime
identity (an ed25519 key) that pairing proves over SSH and pins, identity
changes held until accepted, and direct queries where multicast does not
reach. Setup, pairing, states, network behaviour, untrusted networks and
limits: [docs/discovery.md](docs/discovery.md).

Make it easier to discover Hive runtimes on a local network without turning
discovery into automatic access or implicit trust.

1. **Discovery protocol**
   - Evaluate opt-in mDNS discovery for compatible local networks.
   - Define a minimal service advertisement containing runtime identity,
     protocol version, and supported capabilities.
   - Avoid broadcasting credentials, environment variables, session contents,
     or unnecessary machine metadata.
   - Support configurable discovery names and advertisement settings.
   - Provide a manual connection path when multicast discovery is unavailable.
   - Handle duplicate names, changing addresses, and stale advertisements.

2. **Identity and pairing**
   - Assign each runtime a stable identity independent of its current IP
     address.
   - Introduce an explicit pairing flow with user confirmation.
   - Authenticate connections after discovery rather than trusting the
     discovery packet.
   - Display machine identity, address, platform, and trust status before
     pairing.
   - Support revoking previously trusted machines.
   - Detect identity changes and require renewed verification when appropriate.
   - Document behavior on untrusted networks.

3. **Machine management**
   - Integrate discovered runtimes into `hive machine list` and the existing
     machine-management workflow.
   - Distinguish discovered, configured, connected, disconnected, and trusted
     machines.
   - Allow users to rename and remove saved machines.
   - Avoid automatically adding discovered machines to trusted lists.
   - Support filtering and deduplicating discovered results.
   - Preserve manually configured SSH connections when discovery is disabled.

4. **Network behavior**
   - Respect interface selection and discovery configuration.
   - Handle VPNs, multiple network interfaces, multicast restrictions, and
     firewalls.
   - Limit discovery traffic and connection attempts.
   - Provide actionable diagnostics when discovery fails.
   - Avoid exposing interactive control services solely because a machine
     advertises Hive.

5. **Testing**
   - Test duplicate advertisements, stale records, identity changes, and failed
     pairing.
   - Verify that discovery cannot grant access without successful
     authentication.
   - Test networks where multicast is unavailable.
   - Document supported environments and security limitations.

**✅ Acceptance:** users can discover nearby Hive runtimes, inspect their
identities, and explicitly pair with them. Discovery never silently authorizes
a connection or exposes session contents.

---

## M18 · Desktop App — `v1.0.9` — **IN PROGRESS (preview)**

Hive on your desktop, with the runtime you already trust underneath.

**Status:** a first version is in `desktop/`, with its own CI
(`.github/workflows/desktop.yml`). Install, use, security and limits:
[docs/desktop.md](docs/desktop.md). The exit criteria are not met yet: see
[M18 progress](#m18-progress) below.

A native desktop client for macOS, Windows and Linux. It is built with
[Wails](https://wails.io) (Go) and React (TypeScript), and talks to the same
daemon, socket and API as the CLI and the TUI. The app is a client only. Agents
keep running under their shims when it quits, and every process, session and
machine is still managed by the runtime. The CLI and the TUI stay first-class.

### Research: why Wails, and which Wails

Checked against the Wails repository and documentation on 2026-10-19.

- **Native webviews, one Go binary.** Wails embeds the frontend into a Go
  binary and renders it with the platform's own webview, not a bundled
  browser:
  - WebKit on macOS;
  - WebView2 on Windows;
  - GTK4 with WebKitGTK 6.0 on Linux (GTK3 and WebKitGTK 4.1 remain available
    with `-tags gtk3` through the v3.0 series).

  It is MIT-licensed, with no Node.js runtime at run time. The app's backend
  is plain Go, so it can use `pkg/hiveapi` directly.
- **v2 or v3.**
  - v2 (`v2.17.0`) is the stable line. Its single-window runtime is driven by a
    context, which limits it.
  - v3 has been in beta since 2026-08-02 (`v3.0.0-beta.32` on 2026-10-18). Its
    desktop API is declared stable and is used in production, but it is not
    yet GA.
  - v3 has what Hive needs:
    - first-class multiple windows;
    - Go services with TypeScript bindings generated by static analysis,
      keeping comments and parameter names;
    - a system tray;
    - native notifications;
    - a single-instance lock, with arguments forwarded to the running app;
    - an in-app updater;
    - a Taskfile build that can be read and changed;
    - server builds (`-tags server`), which run the same app and services in a
      browser with no native window.
  - v3's `go.mod` targets Go 1.26, the same as Hive.
  - **Decision:** build on v3 and pin an exact beta. Ship the app on the
    `preview` channel until Wails v3 reaches a release candidate, and on
    `stable` once it is GA. If v3 slips badly, the services and the React app
    port to v2 with little change, since only the window, tray and event layer
    differ.
- **Requirements.**
  - Building: Go, npm (for the React build), and `wails3` (installed with
    `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`;
    `wails3 doctor` checks a machine), plus the platform toolchain:
    - Xcode command line tools on macOS;
    - WebView2 on Windows (preinstalled on Windows 10 and 11);
    - `gcc`, GTK4 and WebKitGTK 6.0 on Linux.
  - Running: those webviews, on macOS 11 and later (Hive itself needs 13) and
    on Windows 10 and 11.
- **cgo, and why the app is separate.**
  - On macOS and Linux, Wails needs cgo for the webview. Wails cross-compiles
    those targets only through Docker images, and macOS bundles cross-built
    that way are not signed.
  - Hive's `hive` binary is pure Go (`CGO_ENABLED=0`), cross-compiled by
    goreleaser, and pinned by a Nix `vendorHash`.
  - So the desktop app lives in its own Go module (`desktop/`) with its own
    workflow and native runners. `go test ./...`, `go mod tidy`, goreleaser and
    the flake of the main module do not change.
- **Frontend.** The `react-ts` template builds React, TypeScript and Vite into
  `frontend/dist`, which is embedded with `//go:embed`. `wails3 dev` proxies
  Vite for hot reload, and bindings are generated into `frontend/bindings`.

1. **Architecture**
   - Add a `desktop/` module (`github.com/admirable-oss/hive/desktop`) with a
     Wails v3 application and a React and TypeScript frontend (Vite, strict
     TypeScript, ESLint).
   - Reach daemons only through `pkg/hiveapi`, extending the SDK additively
     where the app needs more, with no imports of `internal/`. The app never
     starts or signals agent processes itself.
   - Expose a small set of Wails services: sessions, environments, layout,
     panes, agents, machines and settings. Each method is explicit, typed and
     covered by generated bindings. No method runs shell commands.
   - Stream screens as frames from the daemon (deltas, keyframes, frame
     budgets), the way the TUI consumes them. Carry frames on a binary channel
     from the app's asset server, not as JSON events, and measure both. The
     first version sends JSON events of changed lines, coalesced to one per
     pane per frame; the binary channel is next if measurements call for it.
   - Locate the `hive` binary (bundled with the app or on `PATH`), check its API
     level against the app's, and start the daemon with the CLI's autostart
     rules when it is not running.
   - Read the same `config.toml`, with no second settings file: themes, keys,
     notifications and machines.

2. **Workspace experience**
   - Sidebar of environments and agents with live states (working, blocked,
     done, idle), rollups, and metadata tokens from plugins.
   - Tabs and panes laid out from the daemon's layout (split, resize, zoom,
     swap, move), with drag and drop that applies through the layout API.
   - A terminal view that renders Hive's frames. The first version uses DOM
     rows of runs sized in cells (`ch`), which keeps text selectable and
     accessible; a canvas comes later if measurements call for it. It has:
     - cursor, selection, copy, search and scrollback;
     - links, including OSC 8;
     - Kitty, iTerm2 and Sixel images, within the bounds set in M11.

     Evaluate xterm.js as the renderer, behind a frame adapter, before
     committing to either.
   - Keyboard input with IME composition, bracketed paste, dead keys,
     configurable bindings from `[keys]`, and a command palette and Goto picker
     over agents, panes and actions.
   - Agent actions from the CLI: start (with a worktree), prompt, wait,
     rename, focus and stop, each with the CLI's confirmations.
   - Multiple windows: pop a pane or an environment out into its own window,
     with window positions restored on the next launch.
   - Themes from `[theme]` (light, dark and system), accessible contrast,
     keyboard navigation and screen-reader labels.

3. **Desktop integration**
   - A system tray or menu bar item with agent counts by state, the agents that
     are blocked, and quick actions (open, focus, new agent, quit the app).
   - Native notifications when an agent is blocked or done:
     - they follow `[notify]` and are never shown for the focused pane;
     - they are deduplicated against the daemon's own notifications;
     - they open the agent when clicked.
   - The dock badge on macOS (the number of blocked agents), and the taskbar on
     Windows.
   - A single instance, with `hive://` links (the deep-link format from M16)
     handed to the running app. Data passed between instances is encrypted.
   - Native menus with standard shortcuts, and file drops into a pane as
     pasted paths or images (the image paste bridge from M11).
   - Starting at login, as an option, independent of `hive daemon install`.

4. **Machines and observers**
   - Saved machines (M7), with every machine's agents in the sidebar, and
     reachability and identity verdicts from `hive machine status`.
   - Discovery and pairing (M17) in the app:
     - the pairing sheet shows the identity, fingerprint, address and
       platform, and needs an explicit confirmation;
     - a changed identity blocks the machine until it is accepted, with the
       old identity and the new one side by side.
   - An optional read-only window that connects with an observer credential
     (M14), for dashboards on shared screens. It has no input path at all.

5. **Security and privacy**
   - The app is a control client with the same trust as the CLI:
     - it uses the local socket only, with the peer checked by its credentials;
     - it opens no network listener;
     - it reaches remote machines over SSH, as before.
   - The webview loads only embedded assets:
     - a strict Content-Security-Policy and no remote scripts;
     - external links open in the system browser;
     - devtools are off in release builds.
   - Bindings expose explicit methods only. Input from the frontend is validated
     in Go before it reaches the daemon.
   - No terminal contents, tokens or credentials in webview storage. Window and
     UI state may be kept, but screens may not.
   - No telemetry. Update checks are the only outbound requests, and they can
     be turned off.
   - Extend `SECURITY.md` and the threat model to cover the desktop app.

6. **Distribution and updates**
   - Build on native runners for each platform (macOS arm64 and amd64, Windows
     amd64, Linux amd64 and arm64), in a separate `desktop.yml` workflow that
     runs when `desktop/`, the packages it builds on (`pkg/`, `internal/`, `cmd/hive/`) or `go.mod` change.
   - macOS: a `.app` in a DMG, signed with Developer ID and notarized, plus a
     Homebrew cask.
   - Windows: an NSIS installer or MSIX, signed.
   - Linux: AppImage, DEB and RPM (GTK4 and WebKitGTK 6.0), with signed
     packages.
   - Release the app with the same tag as the CLI, with its own signed `desktop-checksums.txt`, signed
     with the release key (`tools/release-sign`).
   - In-app updates with the app's own updater (`desktop/internal/updater`), from GitHub Releases. An update is
     installed only after its checksum and ed25519 signature are checked, as
     `hive update` does. Keep the previous version for rollback.
   - Keep the app and the bundled `hive` CLI on one version. Upgrading the app
     never stops agents.

7. **Testing and performance**
   - Go tests for every service against a real daemon in a temporary
     `HIVE_HOME`, and Vitest with Testing Library for React components.
   - End-to-end tests in CI using a Wails server build (`-tags server`) driven
     by headless Chrome with chromedp, as the web observer's browser tests are.
     They cover:
     - starting and prompting agents;
     - layout changes;
     - reconnecting after a daemon restart;
     - pairing confirmation;
     - notifications.
   - Smoke tests of the native builds on each platform: launch, connect,
     render one pane, quit with agents still running.
   - Budgets, measured and enforced by benchmarks:
     - keystroke-to-screen p99 under 25 ms locally;
     - 16 streaming panes at 60 fps without dropped input;
     - idle CPU under 1 %;
     - memory under 250 MB with 16 panes.
   - Accessibility checks (axe) on the main views, light and dark.
   - `docs/desktop.md`: install, requirements per platform, the Wails and webview
     versions, keyboard shortcuts, updates, limitations and troubleshooting.

### M18 progress

- [x] `desktop/` module: Wails v3 (pinned `v3.0.0-beta.32`) and React 19 with
  TypeScript, Vite, ESLint and Vitest. The `hive` binary stays pure Go.
- [x] `pkg/hiveapi` grew what a GUI needs: frames, input, status and the
  API level, tabs and pane layout. These are additive and frozen in
  `api/v1/hiveapi.txt`.
- [x] Engine (`desktop/internal/core`):
  - connects and reconnects, starting the daemon through the login shell;
  - checks the API level and reads the config through `hive config show`;
  - builds a view model with no secrets;
  - streams per-pane views, shared between windows and paced to the frame
    budget;
  - pastes safely and validates every input.
- [x] Workspace UI:
  - sidebar ("Needs you", environments, branches, tabs) and tab bar;
  - panes laid out as the daemon arranges them, with the tab claiming the
    window's size;
  - terminal with true colour, attributes, wide characters, OSC 8 links,
    cursor shapes, IME, bracketed paste and the application cursor mode;
  - command palette;
  - dialogs: new agent (worktree too), prompt, new environment, confirm;
  - light and dark themes from `[theme]`.
- [x] Desktop integration:
  - tray or menu bar item listing who needs you, and the macOS dock badge;
  - native notifications (`notify.on`, quiet for the focused pane, none when
    `notify.system` is on);
  - `hive://` links, a single instance, menus with shortcuts that leave
    terminal keys alone;
  - pop-out pane windows; closing the window hides it to the tray.
- [x] Security:
  - Content-Security-Policy, and links limited to `http`, `https` and
    `mailto`;
  - one bound service with typed methods; system services are not exposed to
    the page;
  - documented in `SECURITY.md`.
- [x] Tests:
  - Go tests of the engine against a real daemon (start, stream, type, paste,
    split, survive the app, reconnect after a daemon restart);
  - Vitest with Testing Library;
  - an end-to-end test of a server build in headless Chrome;
  - CI on macOS, Linux and Windows, including a check that bindings are up to
    date.
- [x] Packaging tasks: a `.app` with `hive` bundled, an NSIS installer with
  `hive.exe`, AppImage, DEB and RPM. CI builds unsigned previews on demand.
- [ ] Signing:
  - Developer ID signing and notarization;
  - Windows code signing;
  - signed Linux packages;
  - the Homebrew cask;
  - attaching packages to tagged releases with `checksums.txt` and the
    release signature.
- [x] In-app updates (the app's own updater), with signature verification
  and rollback: done in M21.
- [x] Machines (M7, M17) in the app:
  - every enabled, trusted machine's agents in the sidebar, reached through
    `hive machine proxy`;
  - discovery, and pairing by discovery or ssh;
  - review of a changed identity: the app shows the proof first
    (`--check`), then confirms exactly that identity (`--expect-id`).
- [x] The read-only observer window (M14):
  - its own scoped, expiring credential, made through the control
    connection and revoked when the window closes;
  - it reads only through the observer socket;
  - the control service refuses every call from it.
- [x] Terminal: scrollback and search, mouse reporting (SGR and legacy),
  focus events, the Option-as-Meta setting.
- [x] Terminal images: Kitty graphics from agents, assembled, validated and
  re-encoded as PNG in Go (`hiveapi.KittyImages`), shown over their cells,
  support queries answered. iTerm2 and Sixel are output formats `hive ui`
  writes to outer terminals; agents send Kitty, which is what the app reads.
- [x] Start at login, restoring the window position, file drops (paths
  quoted for the shell).
- [x] Accessibility: axe in jsdom and in a real browser, in both colour
  schemes, with colour contrast checked.
- [x] Frame encoding measured: 32 µs a streaming frame, 0.55 ms a keyframe.
- [ ] Keystroke-to-screen latency, idle CPU and memory measured in the native
  webviews; then the binary frame channel if needed.

**✅ Acceptance:** on macOS, Windows and Linux, a signed desktop app installs,
connects to the local daemon (starting it when needed), shows every
environment, agent and pane with live states, and lets you start, prompt and
lay out agents from it. Blocked and done agents raise native notifications that
open the agent. Remote and paired machines work as in the CLI, with identity
changes blocked. Quitting the app leaves every agent running. Updating the app
verifies the release signature. The main module's CI, goreleaser and Nix builds
do not change.

---

## M19 · Release Quality — `v1.0.10` — **IN PROGRESS**

Make the entire 1.0 release train dependable.

**Status:** the review is done and its fixes are on `main`. The exit
criteria wait on a self-hosted runner, the desktop app's signing, and
community input on the next roadmap: see [M19 progress](#m19-progress).
What works with what: [docs/compatibility.md](docs/compatibility.md).
Upgrading and recovery: [docs/upgrading.md](docs/upgrading.md). Operations:
[docs/operations.md](docs/operations.md). Security:
[docs/threat-model.md](docs/threat-model.md) and
[docs/security-review.md](docs/security-review.md). Governance:
[GOVERNANCE.md](GOVERNANCE.md), [rfcs/](rfcs/README.md).

This release consolidates the improvements introduced throughout
`v1.0.1–v1.0.9`. It prioritizes compatibility, security, operational clarity,
and a sustainable maintenance model rather than introducing another large
feature set.

1. **Compatibility audit**
   - Review compatibility across all supported `v1.0.x` releases.
   - Validate CLI, socket protocol, SDK, configuration, and plugin API
     compatibility.
   - Document deprecated commands, configuration keys, and extension points.
   - Add migration paths for supported configuration and plugin manifest
     changes.
   - Verify behavior when older clients connect to newer runtimes where
     compatibility is supported.
   - Publish a versioned compatibility matrix.

2. **Runtime reliability**
   - Run extended soak tests with multiple active local and remote agents.
   - Exercise daemon crashes, shim failures, dropped connections, and
     interrupted upgrades.
   - Verify session restoration, process cleanup, and recovery from corrupted
     state.
   - Track memory, file descriptors, goroutines, and terminal rendering
     latency.
   - Investigate performance regressions against established benchmarks.
   - Add regression tests for every release-blocking reliability defect.

3. **Security review**
   - Audit runtime socket permissions, peer authentication, and remote transport
     boundaries.
   - Review credential storage, SSH integration, secret redaction, and
     environment exposure.
   - Reassess plugin permissions, package integrity, provenance, and rollback
     behavior.
   - Validate observer scopes, token expiry, and revocation.
   - Review the desktop app's webview policy, bindings, and update
     verification.
   - Review dependency vulnerabilities and release artifact integrity.
   - Publish security reporting and supported-version policies.
   - Document the threat model and limitations of plugin isolation.

4. **Plugin ecosystem health**
   - Review registry availability, package integrity, and publishing
     reliability.
   - Measure plugin compatibility failures and failed upgrades.
   - Document policies for abandoned, compromised, deprecated, or removed
     plugins.
   - Verify that official plugins have maintainers and support information.
   - Test recovery from unavailable registries and revoked releases.
   - Publish guidance for maintainers on compatibility, security disclosures,
     and versioning.

5. **Distribution and upgrades**
   - Validate installation and upgrades on every supported operating system
     and architecture.
   - Test fresh installations, upgrades from earlier supported versions, and
     rollback.
   - Verify checksums and signatures where provided.
   - Test package-manager and standalone installation paths.
   - Validate the desktop app's installers, signing, notarization and in-app
     updates on every supported platform.
   - Ensure updates fail safely when an artifact cannot be verified or
     installed.
   - Publish an upgrade and recovery guide.

6. **Documentation and operations**
   - Consolidate installation, configuration, troubleshooting, and recovery
     documentation.
   - Publish current CLI, API, plugin, and configuration references.
   - Document remote runtime security and deployment recommendations.
   - Provide operational guidance for backups, log retention, and diagnosing
     failed sessions.
   - Clearly distinguish stable, experimental, and unsupported features.
   - Update the security policy, contribution guide, and support matrix.

7. **Community and governance**
   - Publish a transparent process for proposing breaking changes.
   - Introduce RFCs for substantial protocol and plugin API changes.
   - Establish expectations for maintainers and official integrations.
   - Document release support and deprecation policies.
   - Gather feedback from plugin authors and users across supported platforms.
   - Publish the next roadmap based on observed adoption, reliability gaps, and
     community priorities.

**✅ Acceptance:** supported `v1.0.x` upgrade paths are tested, public APIs and
migration expectations are documented, critical security and reliability gaps
are addressed, and plugin installation and recovery work predictably. The next
major-version roadmap is published with community input.

### M19 progress

- [x] Compatibility audit:
  - a versioned matrix of releases, API levels, plugin, observer and
    discovery APIs, and state schemas;
  - deprecations listed, with migration paths;
  - older clients against newer daemons, tested.
- [x] Upgrade test: each earlier release is built from its tag, starts
  agents, upgrades with its own `hive update`, runs its CLI against the
  newer daemon, and goes back (`make upgrade`, nightly from every 1.x tag).
  Upgrades made by `v1.0.0-rc.1` keep no previous binary, so going back from
  one means installing it again (documented).
- [x] Updates fail safely: a new daemon that does not start is rolled back
  with its binary. Release candidates sort by semver, Homebrew casks are
  recognised, and tests cover truncated, oversized, unsigned and
  unwritable cases.
- [x] State written by a newer hive (credentials, machines, identity,
  plugins, web) is refused and left untouched.
- [x] Security review ([record](docs/security-review.md)):
  - ten findings fixed. The first was ten standard-library vulnerabilities
    Hive's code could reach, closed by moving to Go 1.26.9;
  - `govulncheck` in CI and nightly, `npm audit`, Dependabot;
  - plugin release tags can no longer become hive's version or trigger a
    hive release;
  - [threat model](docs/threat-model.md) published.
- [x] Plugin ecosystem:
  - revocable releases (`revocations.json`), with failing closed;
  - deprecated and archived warnings, and registry status in `plugin doctor`;
  - the catalog may grow (unknown fields ignored);
  - [policies](docs/plugins/ecosystem.md) for abandoned, compromised,
    deprecated and removed plugins, and guidance for maintainers;
  - official plugins checked.
- [x] Docs and operations:
  - [upgrade and recovery guide](docs/upgrading.md);
  - [operations guide](docs/operations.md): backups, retention, diagnosing
    failed sessions, remote runtimes, feature maturity;
  - SECURITY.md and CONTRIBUTING.md updated, and a bug report form with
    severity.
- [x] Governance: [GOVERNANCE.md](GOVERNANCE.md), covering roles, decisions,
  breaking changes, releases with a release checklist, support and
  deprecation, and maintainer and official-integration expectations. Plus
  the [RFC process](rfcs/README.md), and the
  [proposed roadmap after 1.0.10](rfcs/0001-after-1.0.10.md).
- [ ] The 72-hour chaos soak and an automated cross-user connection test.
  Both need a self-hosted runner.
- [ ] Installs validated on every supported OS and architecture through
  every path: the Homebrew formula and cask, Nix, `install.sh` and
  `install.ps1`. The desktop installers, signing and in-app updates depend
  on M18.
- [ ] The official plugins moved to their own repository, released and
  seeded (open item A of the review). Installer signature checks (open
  item B).
- [x] The docs on the website: compatibility matrix, upgrading, operations,
  threat model, security policy and review, plugin ecosystem, governance,
  RFCs, discovery and the desktop app. They are generated from this
  repository (`admirable-oss/hive-web`, `.github/scripts/sync-hive-docs.mjs`) and kept
  current by the `sync-docs-to-website` workflow, which also serves
  `/plugins/revocations.json`.
- [ ] RFC 0001 discussed with plugin authors and users, and accepted; then
  tag `v1.0.10`.

---

## The 1.x train (M20 to M30) — **PROPOSED**

After `v1.0.10`, each milestone is a minor release: `v1.1.0` for M20 through
`v1.11.0` for M30. They are proposed in
[RFC 0001](rfcs/0001-after-1.0.10.md) and open for comment: the order and
the scope change with what users and plugin authors say, and the accepted
version replaces this section ([GOVERNANCE.md](GOVERNANCE.md#feedback-and-the-roadmap)).
Every milestone keeps the
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/).
Nothing is removed before 2.0, and an API that a milestone adds starts as
an RFC.

The order: finish what 1.0 left open (M20, M21), then the agent workflows
the runtime is built for (M22 to M25), then the ecosystem (M26), then
working with others and more machines (M27 to M29), and last the road to
2.0 (M30).

---

## M20 · Trusted Distribution — `v1.1.0` — **IN PROGRESS**

Every install verified, every release reproducible.

M19 left the first install unverified and the release gates on hardware the
project does not have. This milestone closes both, so that what users run
can be traced back to the source.

1. **Release infrastructure**
   - A self-hosted runner for the 72-hour chaos soak, long multi-machine
     soaks, and the cross-user connection test.
   - The release checklist ([GOVERNANCE.md](GOVERNANCE.md#releases)) run by
     a workflow that blocks the tag until every gate is green.
   - Windows arm64 in CI and releases, once a runner exists.

2. **Verified installs**
   - `install.sh` and `install.ps1` check the release signature, not only the
     checksum (a small verifier, or minisign-compatible signatures).
   - The Homebrew formula and cask, the Nix flake and the installers tested
     on every supported OS and architecture, fresh and as upgrades.

3. **Provenance**
   - Build provenance attestations (SLSA) and an SBOM for every release
     artifact.
   - Reproducible builds: the same tag builds the same bytes, checked in CI.
   - `hive version --verify` reports how the running binary was built and
     verified.

4. **Official plugins**
   - Move them to their own repository with per-plugin tags, and seed the
     catalog with installable releases (M10's and M19's open items).
   - Release them from CI with the same provenance as Hive.

**✅ Acceptance:** a fresh install on every supported platform verifies the
release signature before running anything. Every artifact has provenance
and an SBOM, and rebuilds byte for byte. The 72-hour chaos soak gates the
release. The official plugins install by ID from the catalog.

### M20 progress

How releases are made, gated and verified: [docs/releasing.md](docs/releasing.md).

- [x] Installers verify the release signature: `checksums.txt.sshsig` (the
  release key in OpenSSH's format, `tools/release-sign sign-ssh`), checked
  with `ssh-keygen -Y verify` by `install.sh` and `install.ps1`. Forged,
  unsigned and unverifiable releases are refused (tested end to end); the
  only exception is `v1.0.0-rc.1`, published before signatures.
- [x] `hive version --verify`: the running binary compared byte for byte
  with its signed release (checked against the real `v1.0.0-rc.1`).
- [x] Provenance: SBOMs (syft) and build provenance attestations for every
  archive.
- [x] Reproducible builds: commit timestamps in archives, and a CI job that
  builds twice and compares (`make reproducible`), which passes for all six
  archives.
- [x] The release gate: the release workflow refuses a tag until CI, a
  nightly run, and for minor and major releases a 72-hour chaos soak passed
  on the tagged commit.
- [x] Windows arm64 in CI (`windows-11-arm`) and in releases, and
  `install.ps1` picks it on ARM64 machines.
- [x] The self-hosted runner specified ([docs/releasing.md](docs/releasing.md#the-self-hosted-runner)).
- [ ] The self-hosted runner set up and registered (`hive-soak`), and the
  cross-user connection test automated on it.
- [ ] Installs tested on every OS and architecture, through the Homebrew
  formula and cask, Nix and both installers, fresh and as upgrades.
- [ ] The official plugins in their own repository, with per-plugin tags,
  released with provenance and seeded in the catalog.
- [ ] The first release with all of this (`v1.0.10`, then `v1.1.0`).

---

## M21 · Desktop App 1.0 — `v1.2.0` — **IN PROGRESS**

The desktop app leaves preview.

M18 built the app. This milestone makes it something to install without a
second thought, and brings it under the compatibility policy.

1. **Signed packages**
   - Developer ID signing and notarization on macOS, code signing on Windows,
     signed DEB and RPM packages and AppImages.
   - The Homebrew cask, and packages attached to each release with
     `checksums.txt` and its signature.

2. **In-app updates**
   - The app's own updater, from GitHub Releases. An update installs only after
     its checksum and signature are checked, keeps the previous version, and
     rolls back if the app does not start.
   - The bundled `hive` and the app always move together. Updating never
     stops agents.

3. **Performance in the native webviews**
   - Keystroke-to-screen latency, idle CPU and memory measured on each
     platform and held to M18's budgets in CI.
   - The binary frame channel, if the measurements call for it.

4. **Finish the workspace**
   - Drag and drop of panes through the layout API, and environment windows.
   - Configurable bindings from `[keys]`, and the Goto picker over agents,
     panes and actions.

5. **Stable**
   - Wails v3 at a release candidate or GA, pinned.
   - The app's surfaces covered by the compatibility policy, and its preview
     label removed.

**✅ Acceptance:** on macOS, Windows and Linux, a signed app installs,
updates itself only to verified releases, rolls back a failed update, and
meets the latency, CPU and memory budgets. M18's acceptance holds in full.

### M21 progress

How the app is released and updated: [docs/releasing.md](docs/releasing.md#the-desktop-app),
[docs/desktop.md](docs/desktop.md#updates).

- [x] Release packages built and signed on each system
  (`desktop/build/release.sh`): Developer ID and notarization on macOS,
  Authenticode on Windows, PGP for the `.deb` and `.rpm`, with the release's
  own `hive` bundled (macOS, Windows). Attached to the release with a signed
  `desktop-checksums.txt` and provenance; the `hive-desktop` cask.
- [x] In-app updates: verified against the release key, the previous version
  kept, rolled back when the new one does not start, agents untouched
  (tested with a real signed bundle: staged, swapped and still valid).
- [x] Latency, idle CPU and Go heap measured in CI (macOS and Linux): p99
  17.3 ms, 0.00 % of a core, 14.4 MB of heap with 16 panes on a developer
  machine. CI's shared runners are held to 50 ms of latency (the best of
  three rounds). The binary frame channel is not needed.
- [ ] The rest of M18's budgets: the whole app's memory (250 MB), 16
  streaming panes at 60 fps without dropped input, and the same on
  Windows.
- [x] Drag and drop of panes (`pane.swap`), environment windows, `[keys]`
  in the app (`hive config keys`), and Goto over agents, panes, tabs,
  environments and actions.
- [ ] The signing credentials set up and `HIVE_DESKTOP_RELEASE` turned on,
  and the first signed release installed and updated on every platform.
- [ ] Wails v3 at a release candidate or GA, pinned; then the app under the
  compatibility policy and its preview label removed.
- [ ] The latency budget measured in the native webviews, not only in a
  browser.

---

## M22 · Parallel Agent Orchestration — `v1.3.0` — **IN PROGRESS**

Many agents, one plan.

Agents can already start, prompt and wait for each other (M5). This
milestone adds the missing piece: a task model, so a set of agents works
through a plan with dependencies, limits and results, and a human approves
what matters.

1. **Tasks**
   - A task is a prompt for an agent kind, in an environment or a fresh
     worktree, with a completion condition (idle, done, a regex, a command's
     exit status) and a timeout.
   - Plans: tasks with dependencies, written in TOML or built through the API,
     run as a DAG.
   - `hive task run | list | get | wait | cancel | retry`, MCP tools, and
     `pkg/hiveapi` support. The wire API is designed in an RFC first.

2. **Limits and budgets**
   - Concurrency per plan, per agent kind and per machine.
   - Time budgets per task and per plan.
   - Spend budgets where an agent reports usage (M25).
   - Placement on saved machines (M7) by label and capacity.

3. **Results**
   - Structured results per task: final screen, exit status, changed files,
     a summary the agent writes to a known path, and the task's log.
   - A plan's report, as JSON and readable text.

4. **Human approval**
   - Steps marked as needing approval stop, notify (desktop, push, terminal),
     and continue only after a yes.
   - A blocked agent pauses only its own task, never the whole plan.

5. **In every client**
   - Plans and their progress in the TUI's Overview, the desktop app and the
     web observer (read-only there).

**✅ Acceptance:** a plan of 10 tasks with dependencies runs across 2
machines within its concurrency limits, survives a daemon restart, stops for
one approval, and produces a report with every task's result. A failed task
is retried or cancelled without disturbing the others.

### M22 progress

How plans work: [docs/tasks.md](docs/tasks.md). The design:
[RFC 0002](rfcs/0002-task-orchestration.md).

- [x] Tasks: a prompt for an agent kind, in an environment or a fresh
  worktree, with a completion condition (`done`, `idle`, `match`, `command`)
  and a time budget (`internal/task`).
- [x] Plans as a DAG, from TOML (strictly checked, with cycles reported by
  their path, and fuzzed) or the API.
- [x] `hive task run | list | get | wait | cancel | retry` (and `approve`,
  `report`), the `task_*` MCP tools, and `pkg/hiveapi` support. The wire
  API is `task.*` at API level 12.
- [x] Concurrency per plan, per agent kind and per machine; time budgets
  per task (not counting time blocked) and per plan.
- [x] Placement on saved machines by label and capacity (`hive machine
  set`). A task runs on that machine's daemon, with its progress mirrored
  back.
- [x] Structured results: final screen, exit status, changed files, the
  summary at `$HIVE_TASK_SUMMARY`, and the log. A plan's report as JSON and
  as text.
- [x] Human approval: such tasks stop and notify (TUI, desktop), and go on
  only after a yes. A blocked agent pauses only its own task.
- [x] Plans in the TUI's Overview, the desktop app (with approvals) and the
  web observer (read-only, scoped).
- [x] The acceptance, end to end (`TestM22PlanAcrossMachines`): 10 tasks
  across 2 machines within `max_parallel` and capacity, through a daemon
  restart, a blocked agent and an approval, and a failed check retried
  without disturbing the others. Every task's result is in the report.
- [x] Spend budgets: M25's `per = "plan"` budgets hold a plan's queued
  tasks ([docs/insights.md](docs/insights.md#budgets)).
- [ ] RFC 0002 discussed and accepted; then tag `v1.3.0`.

---

## M23 · Git-Native Development — `v1.4.0` — **IN PROGRESS**

Isolated work, clean handoffs.

Agents get worktrees (M2, M5). This milestone manages what happens around
them: branches from start to merge, conflicts between agents found early,
and changes reviewed before anyone integrates them. The developer keeps
every commit, merge and push.

1. **Branch lifecycle**
   - A branch per task or agent, named by a configurable pattern, with its
     base recorded.
   - States from created to working, ready for review, merged and cleaned up,
     shown in the sidebar.
   - Cleanup policies: remove merged worktrees, keep unmerged ones, never
     delete uncommitted work without asking.

2. **Conflicts**
   - Overlapping changes between agents' branches detected as they happen,
     and reported before a merge is attempted.
   - Rebase or merge assistance that shows the conflict, and never resolves
     one without a person.

3. **Review**
   - A diff view in the TUI and the desktop app, per agent and per task.
   - Comments that go back to the agent as a prompt.
   - Validation gates before a change is marked ready: tests, lint and build,
     run in the agent's worktree.

4. **Publishing, by consent**
   - Opening a pull request (GitHub, GitLab) only when asked, and never
     pushing without an explicit command.
   - Hooks for plugins (M26) to add other forges.

**✅ Acceptance:** three agents work in parallel branches of one repository.
Hive reports their overlap before merging, shows each diff for review,
gates readiness on tests passing, and opens a pull request only on request.
Merged worktrees are cleaned up and unmerged work is never lost.

### M23 progress

How branches work: [docs/branches.md](docs/branches.md). The design:
[RFC 0003](rfcs/0003-git-native-development.md).

- [x] Branch lifecycle: every worktree Hive makes is followed
  (`internal/branch`) from created through working, ready and merged to
  cleaned (or removed), with its base and owner recorded and shown in the
  TUI's sidebar and the desktop app. Merges are detected by fast-forward,
  merge commit or squash.
- [x] Naming patterns for agents (`hive agent start --isolate`,
  `[branches] agent_pattern`) and tasks (`task_pattern`).
- [x] Cleanup policies: merged worktrees are removed once nothing
  uncommitted or running is in them; unmerged branches are never deleted;
  a forced cleanup saves uncommitted work under `refs/hive/saved/` first.
- [x] Overlaps between agents' branches, uncommitted work included
  (snapshots made without touching a worktree), and the files a merge would
  conflict on (`git merge-tree`), as they happen and before any merge. The
  base moving under a branch is reported too.
- [x] Merge and rebase on request (`hive branch update`), stopping on a
  conflict for a person; `hive branch conflicts` shows them without
  merging.
- [x] Review: `hive branch diff`, a review overlay in the TUI (prefix `D`)
  and a review window in the desktop app; comments that go back to the
  agent as a prompt.
- [x] Gates (`[[branches.gate]]`): tests, lint and build run in the
  branch's worktree; ready only when they pass on unchanged files.
- [x] Publishing by consent: `hive branch push` (never forced) and
  `hive branch pr` (GitHub through `gh`, GitLab through `glab`, other hosts
  by `[[branches.forge]]`), closed to plugins and absent from MCP. Forges
  register through `branch.Forges`, the hook for M26 plugins.
- [x] Wire API `branch.*` at API level 13, the `branch_*` MCP tools,
  `pkg/hiveapi` support, plugin capabilities and frozen surfaces.
- [x] The acceptance, end to end (`TestM23ParallelBranches`): three agents
  in parallel branches of one repository, their overlap and conflict
  reported before merging, each diff reviewed, readiness gated on tests,
  a pull request opened only on request, the merged worktree cleaned up,
  and the unmerged work kept (and saved when forced).
- [x] Forges from plugins: M26's `contributes.forges`, looked up after the
  configured hosts and before the built-in forges (`TestM26PluginForgeAndTracker`).
- [ ] RFC 0003 discussed and accepted; then tag `v1.4.0`.

---

## M24 · Workflow Automation — `v1.5.0` — **IN PROGRESS**

Reusable workflows from agents, scripts and events.

Plans (M22) run once. Workflows run whenever their trigger fires, the same
way every time.

1. **Workflows**
   - Steps that are agents' tasks, commands or plugin actions, with inputs,
     outputs, conditions and loops, in a versioned TOML format.
   - Shareable: a workflow is a file in a repository, or a package in the
     registry (M26).

2. **Triggers**
   - Hive events (an agent done or blocked, a worktree created), schedules,
     git events (a push, a new branch), and webhooks received through a
     plugin.
   - Deterministic: the same inputs give the same steps, and each run is
     recorded with its inputs.

3. **Reliability**
   - Retries with backoff, timeouts, cancellation, and failure handling per
     step.
   - Runs survive daemon restarts and reboots, like agents do (M6).

4. **History and CI**
   - Run history with each step's output, kept by the retention settings.
   - Headless mode for CI: `hive workflow run` in a runner, with a JSON
     report and an exit status.

5. **Any provider**
   - Steps name agent kinds, not providers, and a workflow runs with whichever
     agents a machine has.

**✅ Acceptance:** a workflow triggered by a push runs an agent review, the
tests and a fix-up agent on failure. It survives a daemon restart in the
middle, records every run with its inputs and outputs, and runs the same
way headless in CI.

### M24 progress

How workflows work: [docs/workflows.md](docs/workflows.md). The design:
[RFC 0004](rfcs/0004-workflow-automation.md).

- [x] Workflow files (`internal/workflow`): versioned TOML (`version = 1`),
  checked strictly; agent, command and plugin action steps with `needs`,
  inputs, outputs (`$HIVE_OUTPUT`, `[outputs]`), conditions, `on_failure`,
  `foreach` and `repeat`; a deterministic `${{ … }}` expression language.
- [x] Shareable: the person's own files in `~/.hive/workflows`, and shared
  ones (`[workflows] paths`, such as a repository's `.hive/workflows`) that
  fire only once trusted at their current text.
- [x] Triggers: Hive events (with aliases such as `agent.done` and field
  matches), cron schedules and `every`, git pushes and new branches (saved
  references, so pushes while no daemon ran are seen), and webhooks through
  `workflow.fire` for plugins. Concurrency (`queue`, `parallel`, `skip`) and
  `max_runs`; a run never triggers its own workflow.
- [x] Deterministic runs: each records the workflow's text and digest, its
  inputs, its trigger, and every attempt's resolved command or prompt;
  `hive workflow rerun` repeats one.
- [x] Reliability: retries with exponential backoff, step and run
  timeouts, cancellation, failure handling per step; runs saved as they
  change and resumed after a restart (agent steps are M22 tasks whose
  agents outlive the daemon; interrupted commands run again without
  spending a retry).
- [x] History kept by `[retention] workflow_runs` and `workflow_run_age`,
  with each step's output, outputs and summary; `hive workflow report`.
- [x] Headless mode for CI: `hive workflow run --headless` with a daemon
  of its own, a JSON report and an exit status.
- [x] Any provider: steps name agent kinds in order of preference, or
  `"any"`, and run with whichever is installed.
- [x] Wire API `workflow.*` at API level 14, `pkg/hiveapi`, MCP tools,
  plugin capabilities (trust closed to plugins), the action context's
  `workflow`, and frozen surfaces. The TUI's Overview and the desktop app
  show runs and notify on failures.
- [x] The acceptance, end to end (`TestM24WorkflowOnPush`): a push to a
  trusted repository workflow runs an agent review, the tests and a fix-up
  agent on failure; the daemon restarts in the middle of the review without
  the agent being prompted again; the run is recorded with its trigger,
  inputs and outputs; and the same file runs headless as CI would, with a
  JSON report and exit 1.
- [ ] Workflows as registry packages, once Plugin API 2 and the registry
  can carry them (M26).
- [ ] RFC 0004 discussed and accepted; then tag `v1.5.0`.

---

## M25 · Agent Insights — `v1.6.0` — **IN PROGRESS**

Know what your agents cost and where their time goes, on your machine.

Hive sees every agent's state changes. This milestone turns them into
insight without sending anything anywhere.

1. **Time**
   - Time in each state (working, blocked, idle) per agent, task, environment
     and day.
   - Time waiting on you, and what you were asked.

2. **Usage**
   - Tokens and cost where an agent reports them (hooks, status lines,
     manifests), per agent kind and model, with the source shown.
   - Unknown, not guessed, when an agent does not report.

3. **Budgets and alerts**
   - Limits per day, per plan (M22) or per environment, which warn and can
     pause new work.

4. **Local and private**
   - Kept in `HIVE_HOME` under the retention settings. No telemetry.
   - Exported as CSV or JSON on request.
   - Readable through the observer API with a scoped credential, for
     dashboards.

5. **Views**
   - `hive insights`, a panel in the TUI's Overview, and pages in the desktop
     app and the web observer.

**✅ Acceptance:** after a day of work, Hive shows time in state and the
reported usage of each agent kind, raises a budget alert, and exports the
data. Nothing leaves the machine unless it is exported.

### M25 progress

- [x] Time in state and waits on you, per agent, task, environment and day
  (`internal/insights`).
- [x] Usage reported by agents, with its source; unknown when not reported.
- [x] Budgets per day, plan or environment that warn and hold new work
  (`hive insights budgets`, pause and resume).
- [x] Export as CSV or JSON; `observe.insights` for scoped dashboards.
- [x] Views: `hive insights`, the TUI's Overview panel, the desktop app and
  the web observer.
- [x] The acceptance test (`TestM25Insights`).
- [x] A user guide ([docs/insights.md](docs/insights.md)).
- [ ] An RFC, or a note in RFC 0002, for the design; then tag `v1.6.0`.

---

## M26 · Plugin Ecosystem 2 — `v1.7.0` — **IN PROGRESS**

Richer plugins, safer by default.

Plugin API 1 (M12) and the store (M10) are the foundation. This milestone
grows what plugins can do and narrows what a bad one could do.

1. **Registry schema 2**
   - Per-plugin release refs, publisher signatures and build provenance per
     release.
   - Served beside schema 1, so releases before 1.1 keep reading the catalog
     they know.

2. **Isolation by default**
   - Plugin commands run in a sandbox where the platform has one, and a
     Windows sandbox is evaluated.
   - A WebAssembly runtime for plugins that need no processes at all.
   - Capabilities enforced for files, network and processes, not only for
     Hive API calls.

3. **Richer contributions**
   - Plugin views in panes, with a restricted rendering contract.
   - Forges and issue trackers for Git-native work (M23).
   - Workflow steps and triggers (M24).
   - Plugin API 2, designed in an RFC, alongside API 1.

4. **Publishing**
   - `hive plugin publish`: tag, checksum, provenance and submission in one
     step.
   - Compatibility automation that tests a plugin against each Hive release
     before it ships.

5. **Trust**
   - A clear line in every client between official plugins, verified
     publishers and community plugins.
   - Revocations (M19) shown wherever a plugin appears.

**✅ Acceptance:** a community plugin is published with signed provenance,
installs sandboxed by default on macOS and Linux, contributes a pane view
and a workflow step, and is tested against the next Hive release before it
ships. Plugin API 1 plugins keep working.

### M26 progress

The design: [RFC 0006](rfcs/0006-plugin-ecosystem-2.md). The registry
format: [internal/plugin/REGISTRY.md](internal/plugin/REGISTRY.md#schema-2-hive-17);
the policy: [docs/plugins/ecosystem.md](docs/plugins/ecosystem.md#publishers-tiers-and-signatures).

- [x] Trust data (delivery step 1): the installed record and `plugin.list`
  carry tier, publisher, signer, provenance, revocation and sandbox;
  `hive plugin list`/`info`/`install`/`update` show them, revoked releases
  stand out, and `hive plugin doctor` refreshes them. The TUI's palette has
  a Plugins view, the desktop app a Plugins window, and the web observer a
  Plugins section through `observe.plugins` (credentials that see the whole
  runtime). `pkg/hiveapi` `Plugins`. API level 16.
- [x] Registry schema 2 (delivery step 2): `registry-2.json` beside schema
  1, read first with a fallback only on 404; publishers with tiers and
  ed25519 SSH keys; per-release signatures over the release statement
  (verified in Go, interoperable with `ssh-keygen -Y sign`), provenance,
  test results, plugin API and minimum Hive per release, per-plugin refs;
  key revocations in `revocations.json`; `[plugins] require_signature`;
  publisher continuity on update (`TestM26SignedPluginReleases`).
- [x] Publishing (delivery step 3): `hive plugin publish` (tag, checksum,
  sign, provenance, `--submit`), `hive plugin test` (against this Hive, a
  binary or a downloaded release) and the manifest's `[test]`; the
  `plugin-publish` action; the `plugin-compat` workflow and an official
  plugin gate in the release workflow; `tools/plugin-registry` (verify,
  sign, build, record-tests) wired into the submission, official publishing
  and store sync workflows (`TestM26PublishSignedRelease`,
  `TestM26PluginTest`).
- [ ] The official signing key provisioned (`HIVE_PLUGIN_SIGNING_KEY` and
  the `hive` publisher's key, docs/plugins/ecosystem.md). The pipeline
  signs the official releases and publishes `registry-2.json` as soon as it
  is; until then the public registry serves schema 1 alone.
- [x] Plugin API 2 (delivery step 4): `api = 2` beside API 1, sandboxed by
  default on macOS and Linux; `filesystem.read`, `:workspace` scopes and
  `unconfined`; `[permissions.network] hosts` through a per-plugin proxy
  and `[permissions.process] exec` (SBPL on macOS, bwrap plus Landlock on
  Linux, set up by `hive __plugin-sandbox`); `isolation = "none"` and
  sandbox-less systems need `unconfined`, per release; `[plugins]
  isolation = "api2"`; v2 contract fixtures and SDK constants. A hostile
  plugin is held to its grants on macOS and on Linux, as root and as a
  user (`TestAPI2SandboxConfinesByDefault`).
- [x] The WebAssembly runtime (delivery step 5): `[[runtime]] kind =
  "wasm"` plugins run WASI modules in the daemon through wazero, on every
  system; `/data` and `/config` only, no network or programs, 64 MiB of
  memory, timeouts; `hive.call` and `hive.response` call Hive with the
  socket's capability checks (`TestWasmPlugin`, `TestM26WasmPlugin`
  through the real daemon, on macOS, Linux and Windows).
- [x] Pane views, workflow steps and triggers (delivery step 6):
  `[[contributes.views]]` with `plugin.view.set`, drawn as data in panes
  with no process (`--view`, layouts, the TUI, `hive plugin views`), whose
  buttons run the plugin's declared actions; `[[contributes.steps]]` with
  typed inputs and outputs (`uses = "plugin/step"`); `[[contributes.triggers]]`
  fired with `workflow.trigger` (`[[on]] plugin`) (`TestViewPane`,
  `TestPluginSteps`, `TestPluginTriggers`, `TestM26PluginContributions`).
- [x] Plugin forges and issue trackers (delivery step 6, continued):
  `[[contributes.forges]]` behind configured hosts and before the built-in
  ones, needing `network` for their hosts; `[[contributes.trackers]]`
  and GitHub built in, `issue.get|search|comment` and `issue.close`
  (closed to plugins), `hive issue`, `hive task run --from-issue`, a
  task's `issue` in its pull request and `close_issue` on merge
  (`TestForgesAndTrackers`, `TestForgesLookup`, `TestCloseMergedIssues`,
  `TestM26PluginForgeAndTracker`).
- [ ] RFC 0006 discussed and accepted; then tag `v1.7.0`.

---

## M27 · Team Collaboration — `v1.8.0` — **IN PROGRESS**

Share the work, keep control.

Observers (M14, M15) let others watch. This milestone lets people work
together on a runtime, with every permission explicit and every change
audited, and no hosted service required.

1. **People and roles**
   - Invite a teammate to a runtime over ssh with a role: observer, operator
     (prompt and approve, no new processes) or owner.
   - Roles enforced by the daemon, per environment, and listed by
     `hive access`.

2. **Shared workspaces**
   - Two people in one environment see each other's focus and presence, and
     input is taken by one at a time, visibly.
   - Handing an agent or a task over to someone else.

3. **Audit**
   - Every action by someone other than the owner recorded: who did what,
     when, and from where.

4. **Notifications for teams**
   - Blocked and done routed to whoever owns the task, with a fallback.

5. **Local-first**
   - Everything works peer to peer over ssh. A hosted service (M29) is an
     option, never a requirement.

**✅ Acceptance:** two people work in one runtime, one as operator. The
operator can prompt and approve but not start processes. Input never
interleaves. Every action is audited, and revoking access ends it at once.

### M27 progress

The design: [RFC 0007](rfcs/0007-team-collaboration.md).

- [x] Members and the member socket (delivery step 1): `hive access
  invite|list|show|grant|renew|revoke|audit|doctor|whoami`, `hive proxy
  --member` (only `hive proxy` served over ssh), the managed
  `authorized_keys` block (unrestricted duplicates refused), the member
  socket re-checking members per request, connections and denials audited,
  revocation that ends them at once. API level 17 ([docs/teams.md](docs/teams.md);
  `TestM27Members`, `TestServer`, `TestKeysFile`).
- [x] Roles per environment (delivery step 2): every control method
  classified for members (`api/v1/roles.txt`), targets resolved to their
  environment and checked on every request, lists, `api.snapshot` and
  events filtered to the member's environments, observer-style redaction
  below owner, operators' input to running agents only, read-only frames
  views, `by` filled from the member, actions audited with their outcome
  (`TestM27Roles`, `TestMemberViewsTypeOnlyWhenAllowed`,
  `TestMemberRulesClassifyEveryMethod`, `TestRedactionCoversSecrets`).
- [x] Shared workspaces (delivery step 3): presence (`presence.*`,
  `hive presence`), each member's own focus, one typist per pane for
  everyone (`pane.claim|release`, `[access] input_idle`, `pane.floor`), and
  both in the multiplexer (`TestOneTypistPerTerminal`,
  `TestMembersFocusIsTheirOwn`, `TestM27OneTypistAtATime`, `FuzzRegistry`).
- [x] Presence and the holder in the desktop app and the web observer
  (`observe.presence`, `pane.floor`, `floors` in snapshots; `TestPresenceIsScoped`).
- [x] Handing over (delivery step 4): `owner` on agents, tasks and plans,
  `agent.handover`, `task.assign` (`hive agent handover`, `hive task
  assign`), and `actor` on events a member caused (`TestHandingOver`).
- [x] Notifications routed by the daemon (delivery step 5): `notify`
  events to the work's owner, else `[access] notify_fallback`, followed by
  the multiplexer and the desktop app (`TestNotificationRecipients`).
- [x] The acceptance test (`TestM27TeamRuntime`), `docs/teams.md` and the
  threat model (delivery step 6).
- [ ] RFC 0007 discussed and accepted; then tag `v1.8.0`.

---

## M28 · Runtime Fleet Management — `v1.9.0` — **PROPOSED**

Many machines, one view, safe rollouts.

Saved machines (M7) and discovery (M17) connect a handful of runtimes. This
milestone manages dozens: their health, versions, upgrades and policies,
self-hosted.

1. **Inventory and health**
   - Every machine's version, API level, load, agents and health in one view,
     in the TUI, the desktop app and `hive fleet status`.
   - Diagnostics gathered on request, without terminal contents.

2. **Coordinated upgrades**
   - Rolling upgrades in waves, verified by signature, with automatic
     rollback when health checks fail, and agents kept running throughout.

3. **Policies**
   - Per-machine and per-group rules: which agents and plugins may run, update
     channels, resource limits and access roles (M27).
   - Policies distributed over the existing ssh trust, and verified by
     machine identity (M17).

4. **Scale**
   - 50 machines and 500 agents in one view, within the bandwidth budgets of
     M7.

**✅ Acceptance:** a fleet of 50 machines upgrades in waves to a new release.
One unhealthy wave rolls itself back. A policy blocks a plugin fleet-wide.
The fleet view stays responsive with 500 agents.

---

## M29 · Hosted Foundations (private beta) — `v1.10.0` — **PROPOSED**

An optional control plane, for teams that want one.

Moved out of the 1.0 train (it was M18). Hive stays self-hosted first: the
hosted service adds reach, never a dependency.

1. **Control plane**
   - Organizations, users, memberships and runtime registrations, with
     tenant isolation.

2. **Outbound connections**
   - Runtimes connect out, with authenticated registration, revocation,
     reconnects and bounded buffering, and no inbound ports on developer
     machines.

3. **Roles and separation**
   - Administrator, operator and observer roles (M27), with audited
     membership and administrative changes.
   - Process control kept separate from observation by default.

4. **Data ownership**
   - What stays on the machine, what is synchronized, and export, deletion
     and offboarding.
   - Terminal contents never stored by default.

5. **Operations**
   - Health checks, metrics, retention, backups, rate limits and incident
     response.
   - Charging for hosted capabilities only, never for the core runtime, with
     usage measured apart from terminal contents.

**✅ Acceptance:** in a private beta, a team registers runtimes, shares
observation and operation by role, and exports or deletes its data. Every
runtime keeps working with the hosted service switched off.

---

## M30 · 1.x LTS and the Road to 2.0 — `v1.11.0` — **PROPOSED**

A long-term 1.x, and a 2.0 nobody has to fear.

The last 1.x milestone, in the spirit of M19: no large features, a
dependable release to stay on, and a planned path to the next major
version.

1. **Long-term support**
   - `v1.11` designated LTS: security fixes for a published period after 2.0
     ships.
   - Its support window written into [SECURITY.md](SECURITY.md) and the
     compatibility matrix.

2. **The 2.0 RFC**
   - What 2.0 removes: plugin API 0, deprecated configuration keys, protocol
     1 if the RFC decides so, and anything else deprecated in 1.x.
   - What it changes, with a migration for each item.

3. **Migration tooling**
   - `hive migrate --check` lists every deprecated surface a setup uses
     (config, plugins, scripts through their API calls) and how to move.
   - `hive migrate` applies what can be applied automatically.

4. **Release quality**
   - A full compatibility, reliability and security review, as in M19,
     across the 1.x train.
   - The upgrade test from every 1.x release to the LTS.

**✅ Acceptance:** the LTS release passes the full review. `hive migrate
--check` reports every deprecated surface in use, with its migration. The
2.0 RFC is accepted with community input, and its removals are announced at
least one minor release ahead.

---

## Beyond M30

Ideas without a milestone yet. Each needs an RFC that shows who asks for it.

- **Native mobile apps**, if the installable web app (M16) hits limits that
  only native notifications or background work can solve.
- **Windows as a remote host** for `--remote`, not only a client.
- **Translated documentation.**
- **Protocol 3**, if 2.0 needs a new envelope.

---

## Roadmap Principles

- **Ship value early.** Prioritize capabilities that are already built and
  close to release.
- **Reliability before complexity.** Strengthen runtime fundamentals as the
  ecosystem grows.
- **Agent-agnostic by design.** Keep the core runtime independent of individual
  AI providers.
- **Local-first, remote-capable.** Make local development excellent without
  limiting Hive to one machine.
- **Extensible without compromising the core.** Build on stable, documented
  contracts.
- **Secure by default.** Treat runtime access, remote connections, credentials,
  and third-party plugins as security boundaries.
- **Acceptance criteria over promises.** Each milestone should have verifiable
  outcomes, tests, and documented limitations.
