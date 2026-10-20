<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://github.com/user-attachments/assets/e1dd814c-ed76-4d2d-823b-2a3046b07a7d">
    <img alt="Hive roadmap" src="https://github.com/user-attachments/assets/708cc3c2-5d35-4dbb-b1e3-35f4735d89be" width="100%">
  </picture>
</p>


# Roadmap

> **Hive** — AI infrastructure for always-working agents.  
> Site: [https://hive.admir-saheta.com](https://hive.admir-saheta.com)

Each milestone ships a tagged release and has clear acceptance criteria, so you always know when it's truly done. Hive `v1.0.0` is shipped; the next work is the `v1.0.x` release train. 🚀

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

**✅ Acceptance:** you can use Hive daily as a tmux replacement. `teatest` goldens cover each mode. Input latency stays under 10 ms p99 with 16 visible panes.

---

## M4 · Agent Intelligence — `v0.6` - **LIVE**

Hive learns what your agents are doing, so you only look when one needs you.

1. **`agent` engine** — process detection, the manifest engine (regions and matchers), report overrides with TTL, the state machine (working ⇄ blocked → done → idle), rollups and seq counters.
2. **Manifests** — Claude Code, Codex, OpenCode, Gemini, Cursor Agent, Copilot CLI, Amp and Aider, each with recorded-screen fixtures in tests.
3. **Tooling** — `hive agent list | get | explain | manifests`, `hive server reload-agent-manifests`, user manifests in `~/.config/hive/agent-detection/`.
4. **Integrations** — `hive integration install | uninstall | status <agent>`: Claude `settings.json` and Codex `hooks.json` hooks call `hive pane report-agent`. Writes are idempotent, back up the original file, and are fully reversible.
5. **`notify`** — toasts, terminal notifications (OSC 9/777, which work over SSH), system notifications (osascript or notify-send) and sounds for done and blocked, configurable per agent and never for the focused pane.
6. **UI** — sidebar state icons, the Overview's running/awaiting/complete counts from real states, and `next_agent`, `focus_agent_N`, `open_notification_target`.

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
4. **Shutdown safety** — Linux logind delay inhibitor; on macOS, snapshot on `NSWorkspaceWillPowerOff` via a signal path, or on a timer.
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

## M9 · Hardening → `v1.0.0` - **LIVE**

The final stretch: freeze the APIs, polish the rough edges, and ship a 1.0 we can all be proud of. 🎉

1. **API freeze** — audit `api/v1`, `pkg/hiveapi` and the config keys; write a compatibility policy doc.
2. **Performance** — run the benchmark suite; reach 100 panes with p99 frame under 16 ms and idle CPU under 1 %; profile with pprof, available behind `hive debug pprof`.
3. **Reliability** — fuzzers run 24 h in nightly CI, a 72 h chaos soak (kill shims and the daemon, drop the network), and fd and goroutine leak checks.
4. **Security** — socket perms 0600 with peer-cred UID checks (`SO_PEERCRED`/`getpeereid`), signed manifest and update verification, a plugin trust prompt, `gosec`, and an updated `SECURITY.md`.
5. **Distribution** — goreleaser for darwin and linux on amd64 and arm64, `install.sh`, a Homebrew tap, a Nix flake, `hive update`, stable/preview channels.
6. **Docs** — quick start, concepts, keybindings, the config reference generated from `config` structs, the API reference generated from the schema, agent guides, and the herdr→Hive migration guide (including importing herdr config and keys).
7. **RC cycle** — `v1.0.0-rc.1` … `rc.N`, with 2 weeks of no P0/P1 bugs, then **v1.0.0**.

---

## M10 · Plugin App Store — `v1.0.1` - **LIVE**

**Status: in progress.** Registry-backed discovery and initial installation
exist, but the exit criteria below are not met.

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
- [x] Align storefront validation and registry records with the CLI contract;
  the existing `hive-pulse@0.1.0` tag matches its pinned commit.
- [ ] Publish and seed official catalog releases. The official examples still
  need immutable remote release tags before they can be listed as installable.
- [x] Keep the plugin TUI extension claim gated on a daemon contribution API
  and TUI consumer; current plugin runtime supports actions and popups, while
  contributed commands/views/keybindings are not wired through the core.
- [ ] Verify non-interactive install policy, progress/errors, and registry
  outage behavior across CLI and storefront.

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
   - Introduce `platform/windows` implementations for process management,
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
     runs only when `desktop/` or `pkg/hiveapi` changes.
   - macOS: a `.app` in a DMG, signed with Developer ID and notarized, plus a
     Homebrew cask.
   - Windows: an NSIS installer or MSIX, signed.
   - Linux: AppImage, DEB and RPM (GTK4 and WebKitGTK 6.0), with signed
     packages.
   - Release the app with the same tag and `checksums.txt` as the CLI, signed
     with the release key (`tools/release-sign`).
   - In-app updates with the Wails updater, from GitHub Releases. An update is
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
- [ ] In-app updates (Wails updater), with signature verification and
  rollback.
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

## M19 · `v1.0.10` Release Quality — **IN PROGRESS**

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
  repository (`hive-landing/.github/scripts/sync-hive-docs.mjs`) and kept
  current by the `sync-docs-to-website` workflow, which also serves
  `/plugins/revocations.json`.
- [ ] RFC 0001 discussed with plugin authors and users, and accepted; then
  tag `v1.0.10`.

---

## Beyond v1.0.10

These are longer-term directions, not commitments to specific release dates.

### Parallel Agent Orchestration

- Coordinate agents working on independent tasks and shared projects.
- Provide task dependencies, execution limits, and completion conditions.
- Support configurable concurrency and resource budgets.
- Collect structured results from completed agents.
- Preserve explicit human approval for sensitive operations.

### Git-Native Development

- Deepen Git worktree integration for isolated agent work.
- Add branch lifecycle management and conflict detection.
- Support review workflows and configurable cleanup policies.
- Explore automated validation before proposed changes are integrated.
- Preserve developer control over commits, merges, and publication.

### Hosted Foundations

Moved out of the `v1.0.x` train (it was M18) in favour of the desktop app.

- Define a hosted control plane distinct from user-operated runtimes:
  organizations, users, memberships, runtime registrations and tenant
  isolation.
- Connect runtimes outbound to it, with authenticated registration,
  revocation, reconnects and bounded buffering, and no inbound ports on
  developer machines.
- Keep privileged process control separate from read-only observation by
  default.
- Separate administrator, operator and observer roles, with audited
  membership and administrative changes.
- Define data ownership: what stays on the machine, what is synchronized, and
  export, deletion and offboarding.
- Set operations and reliability targets: health checks, metrics, retention,
  backups, rate limits and incident response.
- Charge for hosted capabilities only, never for the core runtime, and keep
  usage measurement apart from terminal contents.
- Validate all of this in a private beta. The self-hosted product must keep
  working without the hosted service.

### Runtime Fleet Management

- Manage multiple Hive installations through a unified control plane.
- Provide runtime health, version visibility, and upgrade coordination.
- Introduce explicit access controls and machine-level policies.
- Support fleet diagnostics without collecting unnecessary terminal contents.
- Define safe rollout and rollback mechanisms.

### Advanced Plugin Ecosystem

- Expand community publishing and maintainer tooling.
- Introduce richer integrations and reusable workflow packages.
- Improve provenance, security analysis, and compatibility automation.
- Explore isolated plugin execution where practical.
- Maintain a clear distinction between trusted official integrations and
  independently maintained community plugins.

### Team Collaboration

- Explore shared workspaces and organization-level access policies.
- Support collaborative observation and notifications.
- Define ownership, auditability, and conflict handling for shared state.
- Preserve local-first operation when hosted services are unavailable.
- Establish explicit permission boundaries before adding remote control.

### Workflow Automation

- Compose agents, scripts, and lifecycle events into reusable workflows.
- Support deterministic triggers, retries, cancellation, and failure handling.
- Integrate with CI systems and developer tooling.
- Provide execution history and structured outputs.
- Avoid coupling workflows to a single model provider.

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
