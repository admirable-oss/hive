# Changelog

All notable changes to Hive. From 1.0.0 the project follows
[semantic versioning](https://semver.org) and the
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/).

## v1.7.0 (unreleased): Plugin ecosystem 2 (M26)

Richer plugins, safer by default ([RFC 0006](rfcs/0006-plugin-ecosystem-2.md)). In progress.

### Trust in every client

- Every installed plugin carries its tier (official, verified, community, or
  unlisted for Git and linked installs), its publisher, the key that signed
  its release, its build provenance and any revocation, in the installed
  record and in `plugin.list`, with `sandbox` (none, sandbox-exec or bwrap).
- `hive plugin list` has a TIER column and marks revoked releases; `info`,
  `install` and `update` show the tier, publisher, signer and provenance.
- The TUI's palette always offers **Plugins…**, a view with each plugin's
  tier, sandbox and revocations, and its details.
- The desktop app has a **Plugins** window (File menu and palette).
- The web observer shows a runtime's plugins to links that see all of it,
  through the new `observe.plugins` observer method.
- `pkg/hiveapi` gains `Plugins` and the tier constants. API level 16.

### Plugin API 2: sandboxed by default

- `api = 2` plugins run in the system sandbox wherever one exists
  (`sandbox-exec` on macOS; bubblewrap with Landlock on Linux): they read
  only their own files and the system's, write only their own directories,
  reach only the hosts they list and run only the programs they list,
  unless granted more. Plugin API 1 plugins run as before.
- New capabilities: `filesystem.read`, `filesystem.read:workspace`,
  `filesystem.write:workspace` and `unconfined`; `[permissions.network]
  hosts` and `[permissions.process] exec` narrow `network` and
  `process.exec`.
- Listed hosts are reached through a per-plugin proxy with its own
  credentials, which logs what it refuses.
- `isolation = "none"` (with `isolation_reason`) and systems without a
  sandbox need `unconfined`, approved once per release.
- `[plugins] isolation = "api2"` runs only API 2 plugins.
- `hive plugin init` scaffolds an API 2 plugin; the TypeScript, Python and
  Rust SDKs know the new capabilities. Plugin API 2 fixtures are in
  `sdk/protocol/fixtures/v2/`.

### Publishing

- `hive plugin publish` tags, checksums, signs (`ssh-keygen -Y sign`, with
  `--key`, git's `user.signingkey` or ssh-agent) and records a release, with
  build provenance from GitHub Actions; `--push`, `--dry-run`, and
  `--submit` to append it to the registry submission and open the pull
  request. Moved tags and uncommitted changes are refused.
- `hive plugin test` checks a plugin, or a catalog plugin's newest release,
  against this Hive, a binary or a downloaded release (`--hive 1.7.0-rc.1`):
  validate, install, doctor and the manifest's new `[test]` command, in a
  scratch `HIVE_HOME`.
- A reusable GitHub Action, `admirable-oss/hive/.github/actions/plugin-publish`.
- The registry's `plugin-compat` workflow tests every listed plugin against
  each release candidate; the release workflow refuses to publish a Hive
  that fails an official plugin.
- `tools/plugin-registry` builds and verifies `registry-2.json`, signs the
  official releases with the official publisher key, and records
  compatibility results.

### Registry schema 2 and signatures

- Hive reads `registry-2.json` beside the catalog first, and the schema 1
  catalog only when the registry serves no schema 2 (404).
- Schema 2 lists publishers with their tier and ed25519 SSH keys, and per
  release a publisher signature (`ssh-keygen -Y sign -n
  hive-plugin-release` over the release statement), build provenance, the
  Hive releases it was tested against, its plugin API and minimum Hive
  version, and per-plugin refs (`pr-status/v1.2.0`).
- `[plugins] require_signature`: `official-verified` (default), `all` or
  `none`. A release whose signature does not verify is never installed; an
  update keeps the publisher, and never goes from signed to unsigned.
- `revocations.json` gains `keys`: a revoked publisher key revokes every
  release it signed from the time it is distrusted.
- `hive plugin install` and `update` pick the newest release this hive can
  run (its plugin API and minimum Hive version), and `hive plugin doctor`
  checks each release's signature and records what the registry says of it.

## v1.5.0 (unreleased): Workflow automation (M24)

Reusable workflows from agents, scripts and events ([docs/workflows.md](docs/workflows.md), [RFC 0004](rfcs/0004-workflow-automation.md)).

### Workflows

- A workflow is a TOML file (`version = 1`) of steps: agents' tasks, commands
  (an argv or a shell line) and plugin actions, with inputs, outputs
  (`$HIVE_OUTPUT`), conditions (`if`, `on_failure = "continue"`) and loops
  (`foreach`, `repeat`). Steps run in order unless `needs` says otherwise,
  and in parallel when it allows.
- `${{ … }}` expressions read inputs, the trigger and earlier steps'
  results, with comparisons, `&&`/`||` and functions such as `contains`,
  `format` and `fromJSON`. They have no clock, environment or I/O.
- Agent steps name kinds, not providers: `agent = ["claude", "codex"]` or
  `"any"` runs with the first kind installed on the machine.

### Triggers

- Hive events (`agent.done`, `agent.blocked`, any event type or prefix with
  field matches), cron schedules and `every`, git pushes and new branches
  (seen even when they happened while no daemon ran), and webhooks fired by
  plugins (`workflow.fire`) or `hive workflow fire`.
- `concurrency` queues, skips or runs alongside; `[workflows] max_runs`
  bounds runs across workflows. A run never triggers its own workflow.
- Your own workflows live in `~/.hive/workflows`. Shared ones (a
  repository's `.hive/workflows`, listed in `[workflows] paths`) fire only
  once trusted with `hive workflow trust`, and again after each change.

### Reliability and history

- Retries with exponential backoff, step and run timeouts, and
  cancellation that stops agents' tasks and commands with everything they
  started.
- Runs are saved as they change and resume after a daemon restart: agent
  steps keep their agents and their tasks, and interrupted commands run
  again without spending a retry.
- Every run records the workflow's text, its inputs and its trigger, and
  each step's resolved command or prompt and its results.
  `hive workflow rerun` repeats a run exactly. `[retention] workflow_runs`
  and `workflow_run_age` bound the history.

### CI and clients

- `hive workflow run --headless` runs a workflow with a daemon of its own,
  prints its report (`--json`), and exits 0, 1 or 4.
- The TUI's Overview and the desktop app's sidebar show workflow runs, and
  notify when one fails. The desktop app cancels and reruns them.

### API

- `workflow.*` methods and events at API level 14. MCP tools
  `workflow_list`, `workflow_run`, `workflow_runs`, `workflow_status`,
  `workflow_wait` and `workflow_cancel`. `pkg/hiveapi`: `Workflows`,
  `RunWorkflow`, `RunWorkflowFile`, `WaitWorkflowRun`, `RerunWorkflow`,
  `FireWebhook`, `ParseWorkflow` and the rest. A plugin action run by a
  workflow gets `workflow` in its context.

## v1.4.0 (unreleased): Git-native development (M23)

Isolated work, clean handoffs ([docs/branches.md](docs/branches.md), [RFC 0003](rfcs/0003-git-native-development.md)).

### Branch lifecycle

- Every worktree Hive makes (for an agent, a task, or `hive worktree
  create`) has a branch Hive follows: `created`, `working`, `ready`,
  `merged`, then `cleaned` (or `removed` before it merged), with its base
  and the agent or task it was made for.
- `hive agent start --isolate` starts an agent on a branch Hive names
  (`[branches] agent_pattern`, default `hive/{kind}/{id}`). Isolated tasks
  use `task_pattern` (default `hive/{plan}/{task}`, as before).
- Merged worktrees are removed once nothing uncommitted and no running
  process is in them (`cleanup = "merged"`). Unmerged branches are never
  deleted. `hive branch clean --force` saves uncommitted work under
  `refs/hive/saved/` before removing a worktree.

### Overlaps and conflicts

- Branches that change the same files are reported as they do, with the
  files a merge would conflict on, before anyone merges. Uncommitted work
  counts: worktrees are compared through snapshots made without touching
  them. A base that moved under a branch is reported too.
- `hive branch conflicts` shows each conflict with its markers, without
  merging. `hive branch update [--rebase]` brings the base in on request and
  stops on a conflict for you. Hive never resolves one.

### Review

- `hive branch diff`: a branch's changes against its fork point,
  uncommitted ones included.
- `hive branch comment` and `send`: review comments that go back to the
  branch's agent as one prompt.
- `hive branch ready` runs `[[branches.gate]]` (tests, lint, build) in the
  worktree; the branch is ready only while its files are those the gates
  passed on.
- The TUI marks each worktree's state in the sidebar, lists branches in the
  Overview, notifies on new overlaps, and reviews a branch with prefix `D`.
  The desktop app has a Branches section and a review window.

### Publishing, by consent

- `hive branch push` (never forced) and `hive branch pr` (GitHub through
  `gh`, GitLab through `glab`, other hosts with `[[branches.forge]]`, using
  your own login). A pull request needs its branch pushed (or `--push`) and
  committed.
- Plugins cannot push or open pull requests, and the MCP tools offer only
  reading, comments and gates.

### API

- `branch.*` methods and events at API level 13; `agent.start` takes
  `isolate`. MCP tools `branch_list`, `branch_get`, `branch_diff`,
  `branch_conflicts`, `branch_comment` and `branch_ready`. `pkg/hiveapi`:
  `Branches`, `BranchDiff`, `ReadyBranch`, `CommentBranch`,
  `OpenPullRequest`, `CleanBranches` and the rest.

## v1.3.0 (unreleased): Parallel agent orchestration (M22)

Many agents, one plan ([docs/tasks.md](docs/tasks.md), [RFC 0002](rfcs/0002-task-orchestration.md)).

### Tasks and plans

- A task is a prompt for an agent kind, in an environment or a fresh
  worktree (`isolate`, `worktree`), with a completion condition (`until`:
  the turn is `done`, the agent is `idle`, its screen matches `match`, or
  `command` exits 0) and a time budget that does not count time blocked.
- Plans: tasks whose `needs` form a DAG, written as TOML (strictly checked:
  unknown keys, unknown needs and cycles are errors, with the cycle's path)
  or built through the API.
- `hive task run | list | get | wait | cancel | retry | approve | report`,
  the `task_*` MCP tools, and `RunPlan`, `WaitPlan` and the rest in
  `pkg/hiveapi`. The wire API is `task.*` (API level 12).
- `agent.wait` takes `until: "unblocked"`: it waits until someone answers a
  blocked agent.

### Limits and placement

- Concurrency per plan (`max_parallel`), per agent kind (a plan's `[kinds]`,
  and `[tasks.kinds]` across plans) and per machine (`[tasks] capacity`).
- Time budgets per task and per plan. A plan's budget cancels what is left.
- Placement on this machine and saved machines by `labels` and capacity
  (`hive machine set <name> --label … --capacity N`). A task placed on a
  saved machine runs on that machine's daemon, and its progress is
  mirrored back.

### Results and approvals

- Every task keeps its agent's last screen, the files it changed since its
  base commit, its check's exit status and output, the summary its agent
  wrote to `$HIVE_TASK_SUMMARY`, and its log. A plan's report comes as text
  or JSON.
- `approve = true` stops a task until a person approves it. The TUI and the
  desktop app notify, and the desktop app has Approve and Refuse buttons.
  A blocked agent pauses only its own task.
- Failed attempts retry by themselves (`retries`). `hive task retry` runs
  failed, cancelled and skipped tasks again, in the worktrees they had,
  without touching the rest.

### Every client

- The TUI's Overview lists plans with their progress and what waits for
  you. The desktop app's sidebar has a Plans section. The web observer shows
  plans read-only: no prompts or results, and only the tasks in its scope.

### Reliability

- Plans survive a daemon restart. They are saved as they change, and
  running tasks are picked up where they were, without prompting again.
- Bounded everywhere: screens, changed files, summaries, logs and finished
  plans (`[tasks] keep_plans`) are capped.

### Look and first run

- The TUI wears the desktop app's colours: `hive` (amber on graphite) and
  `hive-light` are new themes, and `auto` now picks them. Other themes are
  unchanged, and a `[theme.custom]` table without `base` still starts from
  catppuccin.
- The empty pane area welcomes you: the bee flaps beside a speech bubble
  that types itself out, over a checklist of the next steps and their keys,
  while connecting, when the daemon is out of reach, on a first run and in
  an environment with no tabs. Small screens keep the plain text.
- The installer's onboarding: the bee says hello, then the theme and
  starting at login are picked with the arrow keys (or j/k, a digit, Enter),
  each question folding into its answer. `NO_COLOR` and non-interactive
  installs get plain text.

### Fixes

- The TUI no longer hangs on "Connecting to the hive daemon…": a frame
  locked a pane's view twice when planning its images.
- A task resumed after a daemon restart no longer counts as finished in the
  moment between someone answering its blocked agent and the agent going
  back to work.

## v1.2.0 (unreleased): Desktop app 1.0 (M21)

### Signed packages

- The release workflow builds the desktop app on each system
  (`desktop/build/release.sh`) and attaches it to the release: a universal
  macOS zip signed with a Developer ID and notarized, a signed per-user
  Windows installer, and the Linux AppImage, `.deb` and `.rpm` (amd64 and
  arm64, the packages signed with PGP). The macOS and Windows packages bundle
  the release's own `hive`, verified before packing.
- `desktop-checksums.txt`, signed with the release key (`.sig` and
  `.sshsig`), and a build provenance attestation for every package.
- The `hive-desktop` Homebrew cask, updated on stable releases.
- The `desktop` workflow's package run builds the same packages, unsigned,
  without a tag.

### In-app updates

- The app checks for a newer release once a day (it can be turned off) and
  on demand, on `update.channel`. It installs one only after
  `desktop-checksums.txt` verifies against the release key built into the
  app, and the package against its checksum.
- Installing hands over to a supervisor: it keeps the previous version,
  swaps the new one in, and puts the previous one back if the new one does
  not start. Agents keep running throughout.
- A release without desktop packages is not an update. Zips made on macOS
  unpack without their AppleDouble files, which broke the bundle's signature.

### Workspace

- Drag a pane onto another to swap them, through the layout API
  (`pane.swap`).
- Environment windows: an environment, with its tabs, in a window of its own.
- The app follows `[keys]`: the prefix key and its prefix-mode bindings, as
  in `hive ui`. `hive config keys` prints the effective key map (`--json`
  too).
- The Goto palette covers tabs and environments as well as agents, panes and
  actions.

### Performance

- Keystroke-to-screen latency (p99 under 25 ms on a developer machine,
  50 ms on CI's shared runners) and the idle budget with 16 panes (under 1 %
  of a core, under 64 MB of heap) are measured in CI on macOS and Linux.

### Security

- Go 1.26.10 (`toolchain` in both modules' `go.mod`), for GO-2026-6809 in
  `crypto/x509`, which `govulncheck` found reachable.

## v1.1.0 (unreleased): Trusted distribution (M20)

### Verified installs

- `install.sh` and `install.ps1` check the release signature before
  installing: `checksums.txt.sshsig`, the release key's signature in
  OpenSSH's format, verified with `ssh-keygen -Y verify`. A signature that
  does not verify, or a release without one, stops the install.
  `HIVE_ALLOW_UNVERIFIED=1` relies on the checksum where `ssh-keygen` is
  missing. `v1.0.0-rc.1`, published before installer signatures, still
  installs on its checksum.
- `hive version --verify` downloads the signed release the binary claims to
  be and compares the two byte for byte.

### Provenance

- Every archive has an SPDX SBOM and a build provenance attestation
  (`gh attestation verify`).
- Reproducible builds: archives carry the commit's timestamps, and CI builds
  every change twice and compares the bytes (`make reproducible`).
- `tools/release-sign sign-ssh` and `allowed-signers`.

### Release gate

- The release workflow publishes nothing until CI, a nightly run, and for a
  minor or major release a 72-hour chaos soak, passed on the tagged commit.
  Nightly runs are named after their chaos duration.
- The changelog's previous release and snapshot versions come from
  annotated tags, never from plugin tags.

### Platforms

- Windows arm64: tested in CI on `windows-11-arm`, released, and chosen by
  `install.ps1` on ARM64 machines.

### Docs

- [Releasing Hive](docs/releasing.md): what a release contains, cutting one,
  the gate, the self-hosted runner, and verifying a release.

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
