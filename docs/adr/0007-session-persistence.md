# ADR 0007: Session snapshots, restore after a reboot, and godbus

- Status: Accepted
- Date: 2026-10-10

## Context

Shims (ADR 0006) keep agents alive through daemon crashes, restarts and
upgrades, but not through a host reboot: every agent dies with the host. The
workspace itself (environments, tabs, layouts) was already on disk, written on
every change, so it survived. Each pane, however, was left showing a dead
process. What a reboot loses is what only a running system knows: the
directory a shell had `cd`-ed into, and which conversation each agent was in.

## Decision

1. **`session.json` snapshots** (package `persist`) record, per pane, its
   command, its working directory as read from the live process, and its agent
   kind, session ID and command line. They are written two seconds after a
   change (a burst of changes makes one snapshot), every 30 s (`cd` produces
   no event), and as the daemon stops. An unchanged snapshot is not written
   again. Each write keeps the previous file in `snapshots/` (48 by default,
   hard links, so `session.json` is never missing). An unreadable file is moved
   aside as `<file>.corrupt-<time>` and the newest readable one is used. The
   format has a schema version, and a newer one is skipped, not destroyed.
   Snapshots supplement the live stores; they don't replace them. A damaged
   `layout.json` is rebuilt from the latest snapshot instead of failing every
   pane call.
2. **"Lost" is decided by recovery, not guessed at restore.** Recovery marks a
   process `lost` when its shim is gone, or when it started before the host's
   current boot time. In that second case it is lost whatever the shim
   recorded, because a shutdown kills agents "cleanly" too. Restore starts
   exactly those panes again, in place (`pane.Respawn` keeps the pane's ID and
   position), so an agent the user stopped is never resurrected, and running
   restore twice does nothing.
3. **Resuming is manifest data.** `[resume] args` (with `{session_id}`) and
   `last` say how each agent continues a conversation. Session IDs come from
   the agents' hooks (`hive integration install`). An agent typed into a
   shell is typed into the restored shell again, once its prompt shows.
   Agents start `session.restore_stagger` apart.
4. **Shutdown notice.** On Linux the daemon holds a logind *delay* inhibitor
   and snapshots on `PrepareForShutdown`/`PrepareForSleep`. On macOS a
   CGO-free binary cannot observe `NSWorkspaceWillPowerOff`. launchd sends
   `SIGTERM` at logout and shutdown, and the daemon snapshots on every stop,
   backed by the timer.

## New dependency: `github.com/godbus/dbus/v5`

A logind inhibitor lock is a file descriptor returned over D-Bus
(`org.freedesktop.login1.Manager.Inhibit`), and the shutdown notice is a D-Bus
signal. `systemd-inhibit` cannot help: it holds a lock around a command and
cannot tell us when the shutdown starts. godbus is pure Go (CGO stays off),
has no dependencies of its own, and is the D-Bus library the Go ecosystem uses
(containerd, Kubernetes). It is used only in `platform.shutdown_linux.go`.

## Costs

- Snapshots duplicate a little of the live state, which is kept consistent
  by deriving each snapshot from the live services.
- Resuming by command line drops an agent's other flags. A user manifest can
  override `[resume]` to add them.
- Pane history replay copies screen text to disk, so it is off by default
  (see docs/persistence.md).
