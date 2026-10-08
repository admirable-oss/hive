# ADR 0004: One daemon per storage root

- Status: Accepted
- Date: 2026-10-08

## Context

Before M0, a daemon decided it was alone by dialling the socket. If nothing
answered, it deleted the socket file and listened. Two daemons starting at the
same moment both saw no answer, and the second deleted the first one's freshly
created socket. The first daemon kept running, holding agents, unreachable by
any client. Autostart, where two CLI commands race to start a daemon, makes
this likely. Several orphaned `hive daemon` processes were found on a
development machine.

## Decision

The daemon takes an exclusive `flock(2)` on `<root>/hive.pid` before touching
the socket, and holds it for its whole life. The file contains the daemon's
PID.

- The kernel releases the lock when the process dies, so a crashed daemon
  never blocks the next one. There is no stale-PID guessing.
- A second daemon fails with `ErrAlreadyRunning`. A client that lost the
  autostart race sees the winner answer, and `daemonctl.Spawn` treats that as
  success.
- The socket probe remains for one case: another storage root configured
  with the same socket path.

## Consequences

- `TestRuntime_ConcurrentStartsElectExactlyOneDaemon` starts 8 daemons at
  once. Exactly one must win and stay reachable.
- `flock` locks belong to an open file description. Two opens in one process
  still conflict, which the tests rely on. NFS home directories may not
  support `flock`; that is documented as unsupported for `HIVE_HOME`.
