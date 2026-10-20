# Operating Hive

Running Hive day to day: what to back up, where logs go and how long things
are kept, how to diagnose a session that failed, how to run runtimes on
other machines safely, and which features are stable. Installation and
upgrades are in [docs/upgrading.md](upgrading.md); platform differences are
in [docs/platforms.md](platforms.md).

## Where Hive keeps things

`HIVE_HOME` is `~/.hive` (`%LOCALAPPDATA%\Hive` on Windows). The
configuration file is `~/.config/hive/config.toml` (`%APPDATA%\Hive\config.toml`);
`HIVE_CONFIG` overrides it. The default session lives in `HIVE_HOME`
itself; each named session (`--session`) in `HIVE_HOME/sessions/<name>`,
laid out the same way. Machines, the identity, plugins and the web
observer's files are shared by every session, in `HIVE_HOME`.

| Path | Holds | Private |
|---|---|---|
| `hive.sock`, `observer.sock`, `hive.pid` | The daemon's sockets and process ID | 0600 in a 0700 directory |
| `session.json`, `snapshots/` | The workspace (environments, tabs, panes, layout, focus) and its last `session.keep_snapshots` versions | yes |
| `environments/<env>/` | Each environment's record, layout and processes (`processes/<id>/stdout.log`) | yes; agents' output can hold secrets |
| `history/` | Pane text kept for replay, only while `session.replay_history` is on | yes |
| `run/` | The shims' sockets and state, one per running agent | yes |
| `logs/daemon.log`, `logs/daemon.stderr` | The daemon log and the last start's standard error | yes |
| `plugins/` | Installed plugins, their record (`registry.json`), settings (`.config/`), data (`.data/`) and cache (`.cache/`) | yes |
| `observers/` | Observer credentials (hashes only) and `audit.log` | yes |
| `machines.json`, `identity.json` | Saved machines and this runtime's identity key | yes; `identity.json` is a private key |
| `web/` | The web observer's runtimes, sign-ins and push subscriptions | yes |

## Backups

Back up the configuration file and `HIVE_HOME`, leaving out what is rebuilt
or only describes running processes:

```sh
tar -czf hive-backup.tgz -C ~ \
  --exclude='.hive/run' --exclude='.hive/*.sock' --exclude='.hive/hive.pid' \
  --exclude='.hive/logs' --exclude='.hive/plugins/.cache' \
  .hive .config/hive
```

- **Take it while the daemon runs if you like.** Every state file is
  replaced atomically, so a copy never holds half a file. `hive session save`
  first makes `session.json` current.
- **`identity.json` is a private key.** Anyone holding it can pose as this
  runtime to machines that paired with it. Keep backups as private as the
  file (0600), or leave it out. A runtime without one makes a new identity,
  and paired machines must accept it.
- **Plugins** can also be exported on their own: `hive plugin export <id>
  [--data]`, and `hive plugin import` to restore.
- **Output logs** (`environments/*/processes/*/stdout.log`) are whatever
  agents printed, secrets included. Leave them out unless you need them.

Restoring a backup: stop the daemon (`hive daemon stop --keep-agents`, or
`hive stop` to stop agents too), put the files back, and run any command.
Recovering a single damaged file is covered in
[docs/upgrading.md](upgrading.md#recovering-damaged-state).

## Logs and retention

| What | Kept | Setting |
|---|---|---|
| Daemon log (`logs/daemon.log`) | Rotated at 10 MB, 3 old files | `log.max_size_mb`, `log.max_backups`; `log.level` (or `HIVE_LOG`), `log.format = "json"` for collectors |
| An agent's output log | Rotated at 50 MB, one older file | `retention.process_log_mb` (0: unlimited) |
| Exited processes no pane shows (records and logs) | 7 days | `retention.exited_processes` |
| Automatic cleanup | Every 6 hours | `retention.gc_interval` (`0s`: never); `hive gc [--dry-run] [--older-than 24h]` runs it now |
| Session snapshots | The last 48 | `session.keep_snapshots`, `session.snapshot_interval` |
| Pane text for replay | Off; 500 lines a pane when on | `session.replay_history`, `session.replay_lines`. Turning it off deletes what it kept. |
| Observer audit log (`observers/audit.log`) | Kept | Rotate it with your system's tools if it grows |
| Observer events for resync | The last `observers.history_events` | `observers.history_events` |

The daemon log never holds terminal contents or tokens. Agents' output logs
hold whatever the agent printed. Treat them as secrets when you share them.

## Diagnosing a failed session

Start with what the daemon thinks:

```sh
hive version                 # the binary and the daemon, and their API levels
hive status                  # is the daemon up, and what does it run
hive daemon logs -n 200      # what it did and why (add -f to follow)
hive config validate         # a bad config stops the daemon from starting
```

| Symptom | Look at |
|---|---|
| The daemon does not start | `logs/daemon.stderr` and `hive config validate`. A stale socket is replaced on its own; a second daemon for the same session refuses to start. |
| An agent is gone after a restart | `hive ps list` (status and exit reason), its log with `hive ps logs <id> -n 100`, and `hive daemon logs` for "re-attached" or "restored pane" lines. |
| An agent was not resumed after a reboot | `session.restore` and `session.resume_agents_on_restore`, and whether its hooks report a session ID (`hive integration status claude`). |
| An agent's state is wrong | `hive agent explain <agent>`: the rule that matched and the screen region it read. |
| A plugin misbehaves | `hive plugin doctor [id]` (installation, permissions, settings, health, and from `v1.0.10` its registry status), then `hive plugin logs <id>`. |
| An observer or the web observer cannot connect | `hive observer validate`, `hive observer audit`, `hive web status`. |
| A remote machine misbehaves | `hive machine status <name>`, then plain `ssh <host>` from the same terminal; `hive machine reconnect <name>`. |
| Something is slow or growing | `hive debug stats` (goroutines, open files, heap, GC, uptime); `hive debug pprof --profile cpu --seconds 30 -o cpu.pprof`, or heap / goroutine. |

When reporting a problem, include `hive version --json`, the operating system
and terminal, and the relevant part of `hive daemon logs`. Read output logs
before you share them.

### Monitoring a long-running runtime

`hive debug stats --json` is cheap; poll it to watch for growth. The budgets
the project checks nightly, so you can tell a regression from a busy
machine:

- idle CPU under 1 % with 100 panes;
- p99 frame time under 16 ms with 100 streaming panes;
- input latency under 10 ms with 16 panes;
- no growth in goroutines, open files or memory over the multi-machine soak
  (5 machines, 40 agents) and the chaos soak (daemon and shims killed, the
  network cut).

For dashboards without control access, use an observer credential
([docs/observers.md](observers.md)), not the control socket.

## Runtimes on other machines

Hive reaches other machines only through your own ssh. Recommendations for
a remote runtime, such as a build server or a cloud VM running agents:

- **Give agents their own account.** Hive does not sandbox agents. They can
  do anything their user can. A dedicated user with only the repositories
  and credentials the agents need limits what a misbehaving agent can reach.
- **Use ssh keys, with an agent or hardware key**, and the host keys you have
  verified. Hive uses your `~/.ssh/config`, so `ProxyJump`, `ControlMaster`
  and key restrictions apply.
- **Keep the daemon off the network.** It listens on no port. Local
  discovery (`discovery.advertise`) answers mDNS on UDP 5353 when you turn it
  on; leave it off on untrusted networks and on servers
  ([docs/discovery.md](discovery.md)).
- **For read-only access, use observers.** A dashboard or notifier gets an
  observer credential, scoped and expiring. To hold it to that, give it an
  ssh key restricted to the observer socket
  (`command="hive proxy --observer",restrict` in `authorized_keys`).
- **Put the web observer behind TLS**, its own or a reverse proxy's. It
  refuses to serve beyond localhost without it ([docs/web.md](web.md)).
- **Keep hive current there**: `hive --remote <host>` offers to update it
  when it is older than yours.
- **Restart with the machine**: `hive daemon install` (launchd, systemd user
  service, Task Scheduler). On Linux, enable lingering
  (`loginctl enable-linger <user>`) so the user service runs without a
  login.

## Feature maturity

| Status | Means | Features |
|---|---|---|
| **Stable** | Covered by the [compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/); breaking it is a bug | The daemon, shims and wire API; the CLI and `--json`; the TUI; agent detection and integrations; sessions, persistence and resume; remote machines over ssh; `pkg/hiveapi`; MCP; plugins (API 0 and 1) and the registry; observers and the observer API; the web observer and its installable app; local discovery and pairing; Windows (amd64, and arm64 from v1.1.0) |
| **Preview** | Works and is tested, but may change in a minor release; not yet covered by the policy | The desktop app (built on Wails v3, a beta; unsigned packages, no in-app updates); the preview update channel (release candidates) |
| **Experimental** | Not linked into the binary; for evaluation only | `internal/experiments/adr-spikes` (VT and TUI spikes behind the ADRs) |
| **Unsupported** | May compile, not built or tested by CI, not released | Operating systems other than macOS, Linux and Windows; remote *hosts* on Windows (Windows works as a client); strict plugin isolation on Windows |

Human-readable output, default key bindings, themes, timings and limits, log
formats, `internal/` packages and the shim protocol are not part of the
compatibility promise in any status. Use `--json` or `--template` in
scripts.
