# Persistence, restore and housekeeping

Agents run under shims, so they keep running when the daemon crashes, restarts
or is upgraded. A host reboot ends them all. Hive brings the session back when
the daemon next starts:

- environments, tabs, the layout, the focused pane and the active tab, as they
  were;
- every pane, started again **where it was working** (a shell's directory after
  `cd`, not just where it started);
- every agent, **resuming its conversation**: `claude --resume <id>`,
  `codex resume <id>`, and so on (see `[resume]` in the agent manifests).

```sh
hive session save        # snapshot now (it is also saved on every change)
hive session restore     # start lost panes again by hand (--fresh: agents start over)
hive gc --dry-run        # what clean-up would remove
hive update              # new release; agents keep running
```

## How it works

The daemon writes `~/.hive/session.json` two seconds after the workspace
changes, every `session.snapshot_interval` (30 s, because `cd` raises no
event), and when it stops. The 48 previous snapshots are kept in
`~/.hive/snapshots/`. A snapshot that cannot be read is moved aside as
`session.json.corrupt-<time>`, and the newest readable one is used.
`layout.json` files get the same treatment: a damaged one is moved aside, and
its tabs are rebuilt from the snapshot.

When the daemon starts, it looks at every agent the previous daemon left
running. Agents whose shim is gone, and all agents after a reboot, are marked
**lost**. With `session.restore` on (the default), each pane whose process was
lost is started again in place. Plain panes start at once. Agents follow one
by one, `session.restore_stagger` apart, so ten agents do not all hit the disk
and the network together. Panes you closed and agents you stopped (`hive stop`,
`hive agent stop`) are not lost, so they never come back by themselves.

### Resuming agents

An agent's conversation is resumed by its session ID, which the agent reports
through its hooks. Install them once per agent:

```sh
hive integration install claude
hive integration install codex
```

Without hooks Hive does not know the ID. It then uses the manifest's `last`
command (`claude --continue`, `codex resume --last`), which picks the latest
conversation in the pane's directory. That is right unless several agents of
the same kind share a directory. An agent you typed into a shell comes back
the same way: the shell is restored, and the resume command is typed into it
once its prompt shows.

Set `session.resume_agents_on_restore = false` to start agents fresh instead,
or run `hive session restore --fresh` yourself after turning
`session.restore` off.

## Pane history replay (off by default)

With `session.replay_history = true`, Hive keeps the last
`session.replay_lines` lines of every pane (with their colours) and shows them
in the restored pane above a "restored by hive" line, so you can see what was
there.

**Think before turning this on.** The kept text is exactly what your
programs printed. That includes anything secret that appeared on screen: API
keys an agent echoed, tokens in `env` output, passwords pasted where a
program showed them. Hive keeps it:

- in `~/.hive/history/<pane>.txt`, readable only by you (0600 in a 0700
  directory), rewritten while the pane runs;
- only while the setting is on. Turning it off deletes every history file the
  next time the daemon starts, and `hive gc` removes the history of closed
  panes;
- out of snapshots and logs. The replayed text is shown on the screen only,
  and never added to the new process's output log.

Agents' output logs (`environments/<env>/processes/<id>/stdout.log`) already
hold everything a terminal printed, whatever this setting. Treat `~/.hive` as
you would your shell history: keep it off shared and backed-up-to-the-cloud
disks, or turn the logs' retention down (see below).

## Shutdown safety

- **Linux:** the daemon holds a logind delay inhibitor. When the system is
  about to shut down or sleep, logind announces it, the daemon writes a final
  snapshot, then releases the lock (within logind's `InhibitDelayMaxSec`,
  5 s by default).
- **macOS:** a binary without CGO cannot observe
  `NSWorkspaceWillPowerOff`. launchd sends the daemon `SIGTERM` at logout and
  shutdown instead (run it as a login service with `hive daemon install`), and
  the daemon writes a snapshot on every stop. The timer covers a power cut.

A reboot is recognised from the host's boot time, so it works however the
machine went down.

## Housekeeping

| Setting | Default | What it bounds |
|---|---|---|
| `retention.exited_processes` | `168h` | How long the records and logs of processes that ended (and no pane shows) are kept |
| `retention.process_log_mb` | `50` | Each agent's output log rotates at this size, keeping one older file (`stdout.log.1`) |
| `retention.gc_interval` | `6h` | How often the daemon cleans up by itself (`"0s"`: never) |
| `session.keep_snapshots` | `48` | Earlier snapshots kept |
| `log.max_size_mb`, `log.max_backups` | `10`, `3` | The daemon's own log |

`hive gc` cleans up now. It removes old process records and logs, leftover
shim directories, the history of closed panes, and files moved aside as
unreadable once they are as old as the retention. `--dry-run` lists what it
would remove, and `--older-than 24h` narrows the retention for one run.
Running agents and anything a pane shows are never touched.

## Updates

`hive update` downloads the latest release for your system from GitHub,
checks it against the release's `checksums.txt`, runs it once to be sure it
works, and swaps the binary in place. Then it restarts every running daemon.
Agents keep running through it: their shims are separate processes that keep
the old binary, and the new daemon re-attaches to them. Output printed during
the restart is not lost. The shim keeps the screen and writes every byte to
the log.

```sh
hive update --check                # is there a newer release?
hive update --version v0.8.0       # a given release
hive update --from ./hive.tar.gz   # a binary or release archive you have
hive update --no-restart           # leave running daemons on the old binary
```

A hive installed with Homebrew or Nix is updated by them instead.
