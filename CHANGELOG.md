# Changelog

All notable changes to Hive. From 1.0.0 the project follows
[semantic versioning](https://semver.org) and the
[compatibility policy](docs/compatibility.md).

## v1.0.0-rc.1 (2026-10-12)

The first release candidate of 1.0: the APIs are frozen, and the remaining
work is hardening. Release candidates follow until two weeks pass with no
P0 or P1 bug; then v1.0.0.

### Frozen

- The wire API, the CLI, the configuration keys and the Go SDK are frozen
  in `api/v1/`. Tests fail on a breaking change, and additions must be
  reviewed into the frozen copies. See docs/compatibility.md for what 1.x
  promises.

### Security

- Every daemon and shim connection is checked by the kernel's peer
  credentials (`SO_PEERCRED`, `LOCAL_PEERCRED`). Connections from another
  user are refused.
- Releases are signed: `checksums.txt.sig` is an ed25519 signature that
  `hive update` checks with the release key built into hive
  (`tools/release-sign`). Unsigned or wrongly signed releases are not
  installed.
- SECURITY.md describes what Hive protects, how, and which versions get
  fixes.

### Performance and reliability

- `hive debug pprof` (cpu, trace, heap, goroutine, …) and `hive debug stats`
  (goroutines, open files, memory).
- Budgets checked by tests: p99 frame time with 100 streaming panes is under
  16 ms (measured 4–14 ms), idle CPU with 100 panes is under 1 % (0.35 %),
  and input latency with 16 panes is under 10 ms.
- A chaos soak (`TestChaos`) kills the daemon and shims, freezes the daemon
  and cuts remote links at random, checking that every pane comes back and
  nothing leaks. It runs nightly, with 25 hours of fuzzing over five targets
  (two new: agent and plugin manifests) and the multi-machine soak.
- Leak checks: goroutines and open files across many client connections.
- Fixed: an agent's session ID reported by its start hook before its first
  screen reached the daemon was lost, so the agent could not be resumed
  after a reboot.

### Distribution

- Homebrew: `brew install --cask admirable-oss/tap/hive`.
- Nix: `nix run github:admirable-oss/hive` (`flake.nix`, built in CI).
- Update channels: `hive update --channel preview` (or `update.channel`)
  includes release candidates; `install.sh` takes `HIVE_CHANNEL` and
  `HIVE_VERSION`.

### Docs and migration

- New docs: quick start, concepts, agent guides, and a configuration
  reference and API reference generated from the code (tests keep them
  current).
- `hive config import-herdr` brings herdr's settings and key bindings
  (`--herdr-keys` for its whole keymap). See docs/migrating-from-herdr.md.

## v0.10: Extensibility (M8)

Plugins (startup, events, actions; installed from git after a trust
preview), `[[keys.command]]`, sidebar tokens and titles
(`report-metadata`), the status bar's right side, `--template` output.

## v0.9: Remote and multi-machine (M7)

`--remote` and `--machine` over the system's ssh (`hive proxy`), saved
machines, every machine's agents in the sidebar, compressed and paced screen
streams (about 14 MB/h for a busy pane), OSC 52 to the local clipboard.

## v0.8: Persistence and resume (M6)

Session snapshots, restore after a reboot with agents resuming their
conversations, optional pane history replay, the logind inhibitor,
`hive gc`, output log rotation, `hive update`.

## v0.7: Agents operating agents (M5)

`hive agent start | prompt | wait | read | …`, `hive api schema | snapshot`,
`--json` everywhere, the `pkg/hiveapi` Go SDK, the `hive mcp` server, the
agent skill.

## v0.6: Agent intelligence (M4)

Agent detection from manifests and hooks for eight agents, states and
rollups, notifications, `hive integration install`.

## v0.5: Multiplexer (M3)

Tabs and split panes, sidebar, Overview, copy mode, mouse, themes,
configurable keys.

## v0.4: Workspace model (M2)

Sessions, environments, tabs and panes, BSP layouts, the pane API, git
status and worktrees.

## v0.3: Terminal core (M1)

Protocol 2, shims (agents survive the daemon), the VT emulator, frames and
events.

## v0.2: Foundations (M0)

Tooling and CI, logging, configuration, cobra CLI, daemon autostart and
login services.
