# Daemon chaos harness

Fault-injection tests for the persistence of the Hive daemon, its shims and
their agents. Each test starts a throwaway daemon, injects one fault, checks
that the daemon came back clean, and writes a record. They complement the
randomized Go soak (`make chaos`) with one named, readable scenario per
fault.

```sh
tools/chaos/chaos.sh list          # the tests
tools/chaos/chaos.sh run 01 07     # some, in order
tools/chaos/chaos.sh all           # every test, in order
```

Requires Linux, `bash`, `jq` and Go (the binary is built from this checkout
into `$CHAOS_ROOT/bin`, or set `HIVE_BIN`).

## Tests

| # | Fault | Expected |
|---|---|---|
| 01 | daemon SIGKILL | next command autostarts a daemon that adopts every agent |
| 02 | daemon SIGTERM | daemon exits, agents keep running, next daemon adopts them |
| 03 | daemon frozen (SIGSTOP) while a client calls | same daemon afterwards, no second daemon |
| 04 | one shim SIGKILL | its agent is stopped (not orphaned), recorded `killed`; others untouched |
| 05 | one agent SIGKILL | shim records the exit and goes; others untouched |
| 06 | daemon SIGKILL ×`STORM` (10) | every restart adopts; nothing piles up |
| 07 | control socket deleted | daemon recreates it and is reachable (and stoppable) again |
| 08 | stale pidfile pointing at an unrelated live process | new daemon starts; the unrelated process is left alone |
| 09 | daemon crash + one agent's `state.json` truncated | daemon starts; that agent is re-adopted, not killed |

## "Came back clean"

After every fault, `verify_clean` checks: serving again within
`RECOVERY_TIMEOUT` (recovery time recorded); the expected daemon PID (new,
same, or either); exactly one daemon; every pane kept, none stuck
`starting`; every live agent's on-screen counter still advancing; no
orphans (no sandbox process other than the daemon, live shims and live
agents' process groups); no panic in the daemon log. `verify_no_leak` then
checks goroutines and open files do not grow under the same light load.

## Guardrails

Nothing outside the sandbox is ever signalled:

- Each test runs in its own `HIVE_HOME` under `$CHAOS_ROOT/sb-*`; the real
  `~/.hive` is refused.
- Every signal goes through `safe_kill`, which requires the PID's
  `/proc/<pid>/environ` to carry `HIVE_HOME=<sandbox>`; anything else aborts.
- Teardown always runs (EXIT trap): `hive stop`, then repeated SIGKILL passes
  over sandbox-owned PIDs until none are left. A sandbox still holding a
  live process is never deleted, and the run is marked aborted.

Hard thresholds (environment variables, defaults shown) abort a test (exit 3)
and stop `chaos.sh` from moving on:

| Variable | Default | Limit |
|---|---|---|
| `MAX_LOAD_PER_CPU` | 2.0 | host 1-minute load per CPU, before and during |
| `MIN_MEM_AVAIL_MB` | 1024 | host MemAvailable floor |
| `MIN_DISK_FREE_MB` | 1024 | free space on `$CHAOS_ROOT` |
| `MAX_SANDBOX_PROCS` | 64 | processes owned by the sandbox |
| `MAX_DAEMON_RSS_MB` | 512 | daemon resident memory |
| `MAX_DAEMON_FDS` | 512 | daemon open files |
| `MAX_GOROUTINES` | 2000 | daemon goroutines |
| `MAX_LOG_MB` | 64 | sandbox logs |
| `TEST_TIMEOUT` | 300 | seconds per test |

Other knobs: `AGENTS` (4), `RECOVERY_TIMEOUT` (30), `LEAK_GOROUTINES` (10),
`LEAK_FDS` (5), `STORM` (10), `FREEZE_SECS` (5), `CHAOS_KEEP=1` to keep the
sandbox for inspection, `CHAOS_REBUILD=1` to rebuild the binary.

## Records

Each test writes `$CHAOS_ROOT/records/<time>-<test>.json` and `.md`, and
prints the Markdown before the next test starts: verdict (`PASS`, `FAIL`,
`ABORTED`), every check with its detail, and metrics (recovery time, daemon
PIDs, goroutines, open files, heap, host load and memory).

## Adding a test

```bash
#!/usr/bin/env bash
source "$(dirname "$0")/../lib.sh"
begin my-fault          # sandbox + preflight + teardown trap
fixture                 # $AGENTS ticking agents
safe_kill KILL "$(daemon_pid)"   # inject; only through safe_kill
verify_clean "my fault" yes      # yes|no|any: expect a new daemon PID
verify_no_leak
```
