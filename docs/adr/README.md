# Architecture decision records

Each ADR records one decision: the context, what was decided, and what it
costs. ADRs are not rewritten after they are accepted: a later ADR supersedes
an earlier one, and a factual correction is added as a dated amendment. New dependencies need an ADR (see CONTRIBUTING.md).

| ADR | Decision | Status |
|-----|----------|--------|
| [0001](0001-vt-emulator.md) | Terminal emulation: `charmbracelet/x/vt` behind a Hive-owned port, with Hive-owned scrollback | Accepted (with performance gate) |
| [0002](0002-tui-rendering.md) | Multiplexer rendering: draw cells through ultraviolet, not Bubble Tea string views | Accepted (amended in M1) |
| [0003](0003-m0-dependencies.md) | M0 dependencies: cobra, go-toml v2, goleak, x/sys | Accepted |
| [0004](0004-single-daemon-lock.md) | One daemon per storage root, enforced by `flock` | Accepted |
| [0005](0005-orphan-reaping.md) | Recovery stops orphaned agents only when PID and start time match | Accepted |
| [0006](0006-shims-protocol-2-and-frames.md) | Agents run under shims; protocol 2; screens travel as frames; events | Accepted |

Spike code that produced the measurements lives in [`spikes/`](spikes/). Each
spike is its own Go module and is not part of the main build.
