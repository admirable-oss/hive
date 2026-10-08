# ADR 0005: Stopping orphaned agents safely

- Status: Accepted
- Date: 2026-10-08

## Context

A plain (non-PTY) agent survives a daemon crash. Before M0, the next daemon
marked its record `failed` and left the process running, untracked, possibly
still editing a repository. The fix is to stop it, but PIDs are reused, and
after a reboot the recorded PID can belong to anything. Signalling a reused
PID's process group would kill an unrelated program.

## Decision

At start-up (`Recover`) and on `hive ps stop` of a record this daemon did not
launch, a recorded PID is only signalled if all of these hold:

1. a process with that PID exists (`platform.LookupProcess`);
2. it leads its own process group (`pgid == pid`). Hive starts every agent
   with `Setpgid` or `Setsid`; ordinary processes rarely lead a group whose
   ID equals their own PID;
3. its kernel start time is within [-2 s, +30 s] of the record's
   `StartedAt`. The record is written just before the launch; the -2 s
   allows for Linux's start-time granularity, which uses seconds-resolution
   `btime`.

Then the whole group gets SIGTERM, and SIGKILL after the grace period. The
record becomes `killed`. Otherwise the process is left alone and the record
becomes `failed`.

## Consequences

- Start times come from `sysctl kern.proc.pid` on macOS and
  `/proc/<pid>/stat` + `btime` on Linux, behind `internal/platform`. Other
  operating systems get `ErrUnsupported` and never signal anything.
- PTY agents usually die with their terminal when the daemon dies (SIGHUP),
  so in practice this mostly reaps plain processes.
- In M1 agents move into per-pane shims that outlive the daemon. Recovery
  then becomes reconnecting to shims, and this path remains as a safety net
  for records whose shim is gone.
