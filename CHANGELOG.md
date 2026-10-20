# Changelog

All notable changes to Hive. From 1.0.0 the project follows
[semantic versioning](https://semver.org) and the
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/).

## v1.0.10 (unreleased): Release quality (M19)

The first stable release of the 1.0 train. Nothing has been tagged since
`v1.0.0-rc.1`, so this release carries everything since then. Users of
`v1.0.0-rc.1` upgrade straight to it; the nightly upgrade test checks that
path, and the way back. What works with what is in
[docs/compatibility.md](docs/compatibility.md).

### Since v1.0.0-rc.1 (M10 to M18)

- **Plugin store (M10):** `hive plugin search | info | install <id> | update
  | rollback | validate | init`, a versioned registry with governed
  submissions.
- **Plugin API 1 (M12):** capabilities enforced per plugin, an optional
  sandbox, health and restart limits, sidebar cards, contextual actions,
  settings forms and backups.
- **Images (M11):** Kitty graphics from agents, an image paste bridge, and
  text fallbacks.
- **Windows (M13):** ConPTY, job objects, Task Scheduler, zip releases;
  `hive update --rollback` on every system.
- **Observers (M14):** read-only, scoped, expiring credentials, and an
  observer socket with resync.
- **Web and mobile (M15, M16):** the web observer, an installable app, and
  end-to-end encrypted Web Push.
- **Local discovery (M17):** opt-in mDNS, and pairing that proves each
  machine's identity.
- **Desktop app (M18, preview):** environments, agents and live terminals in
  a window, on Wails v3.

### Security

- Release builds use Go 1.26.9 (`toolchain` in `go.mod`), with
  `golang.org/x/net` v0.60.0. They fix ten standard-library vulnerabilities
  that Hive's HTTP, TLS and Windows file code could reach
  ([security review](docs/security-review.md)).
- `govulncheck` runs on every change and nightly; `npm audit` checks the
  desktop frontend; Dependabot proposes updates.
- New: the [threat model](docs/threat-model.md) and the
  [security review](docs/security-review.md) of the train.
- Builds take their version from annotated tags only, and the release
  workflow refuses lightweight tags. Plugin release tags in the repository
  (`v0.1.0`) can no longer become hive's version or publish a hive release.

### Updates

- When the new daemon does not start after `hive update`, the previous
  binary is put back and the daemons restart on it. Agents keep running.
- Release candidates sort by semantic versioning (`-rc.10` after `-rc.9`).
- Homebrew casks are recognised, and `hive update` names the right
  `brew upgrade` command for the formula (`hive-agents`) or the cask (`hive`).

### Plugins

- The registry can revoke a release (`revocations.json`, beside the
  catalog). Hive refuses to install or update to a revoked release,
  `plugin update` moves off one, and `plugin doctor` fails on one.
- `plugin install` and `update` warn about deprecated and archived listings.
  `plugin doctor` checks each registry plugin against the registry: revoked,
  delisted, deprecated, archived, or an update available.
- The registry catalog may gain fields: unknown fields are ignored. (Earlier
  releases reject them, so the published catalog adds none while they are
  supported.)

### Reliability and compatibility

- State files written by a newer hive (observer credentials, machines, the
  identity, installed plugins, web state) are refused and left untouched,
  not misread or overwritten after a rollback.
- New tests: upgrades from earlier releases with an older CLI against the
  newer daemon (`make upgrade`, nightly from every 1.x tag); updates that
  fail safely; revoked releases; refused newer state files.

### Docs and governance

- [Compatibility matrix](docs/compatibility.md), the
  [upgrade and recovery guide](docs/upgrading.md), the
  [operations guide](docs/operations.md) (backups, retention, diagnosis,
  remote runtimes, feature maturity), and
  [plugin ecosystem policies](docs/plugins/ecosystem.md).
- [GOVERNANCE.md](GOVERNANCE.md) (roles, decisions, breaking changes,
  releases, support), [RFCs](rfcs/README.md), and the proposed roadmap after
  1.0.10 ([RFC 0001](rfcs/0001-after-1.0.10.md)).

## v1.0.0-rc.1 (2026-10-12)

The first release candidate of 1.0: the APIs are frozen, and the remaining
work is hardening. Release candidates follow until two weeks pass with no
P0 or P1 bug; then v1.0.0.

### Frozen

- The wire API, the CLI, the configuration keys and the Go SDK are frozen
  in `api/v1/`. Tests fail on a breaking change, and additions must be
  reviewed into the frozen copies. See the compatibility policy for what 1.x
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
  (`--herdr-keys` for its whole keymap). See the migration guide.

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
