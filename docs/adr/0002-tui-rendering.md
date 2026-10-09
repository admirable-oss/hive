# ADR 0002: Rendering the multiplexer

- Status: Accepted
- Date: 2026-10-08
- Milestone: M0 spike, implemented in M3

## Context

M3 turns the dashboard into a multiplexer: up to 16 visible panes of live
terminal content, plus sidebar, tab bar and overlays. The budget is input
latency under 10 ms at p99 and low CPU. Remote clients (M7) also care about
bytes on the wire.

The dashboard uses Bubble Tea v1. Its renderer diffs lines of a string
`View()`. Bubble Tea v2 (`charm.land/bubbletea/v2`, stable at v2.0.10) diffs
cells, using the `ultraviolet` engine. The question was whether to migrate,
and how pane content should reach the screen.

## Method

The code is in `spikes/tui`. A 3x3 grid of bordered panes in a 220x50
window received 600 updates at 120 per second in two scenarios: every pane
appends a coloured log line (busy agents), or one pane gets one typed
character (a user typing). Output went to a byte counter, and CPU was
measured with `getrusage`.

- `bubbletea-v1`: Bubble Tea 1.3 and Lip Gloss 1.1, with a string `View()`.
- `bubbletea-v2`: Bubble Tea 2.0.10 and Lip Gloss 2.0.6, with a string `View()`.
- `ultraviolet`: nine **x/vt emulators**, each drawn with `Emulator.Draw`
  into a `uv.ScreenBuffer` and rendered by `uv.TerminalRenderer`. No strings
  are involved, and this variant also does the terminal emulation the other
  two skip.

## Results

| Scenario | Implementation | Bytes per update | CPU (share of wall time) |
|---|---|---|---|
| 9 panes scrolling | Bubble Tea v1 | 13,697 | 37.6% |
| | Bubble Tea v2 | 10,355 | 66.5% |
| | **ultraviolet + x/vt** | **10,095** | **29.5%** (including emulation) |
| 1 pane typing | Bubble Tea v1 | 333 | 22.2% |
| | Bubble Tea v2 | 29 | 51.2% |
| | **ultraviolet + x/vt** | **1** | 25.7% (redraws all 9 panes; damage tracking removes most of it) |

Findings:

- Bubble Tea v2's cell diff cuts bandwidth (91% less when typing). Because
  `View` is still a string in v2.0.10, every frame is built as styled text
  and then parsed back into cells, which costs 1.8–2.3x the CPU of v1.
- Drawing cells directly gives v2's bandwidth at less CPU than v1, even
  while emulating nine terminals.
- **Bubble Tea v1 and v2 cannot live in one module.** They require
  incompatible versions of `charmbracelet/x/ansi`, so a migration must
  replace the whole TUI at once.

## Decision

- In M3, build the multiplexer shell on **ultraviolet directly**:
  `uv.Terminal` for input events, plus a `uv.ScreenBuffer` composed each frame
  and handed to `uv.TerminalRenderer`.
- Panes are drawn from emulator cells (`vt.Emulator.Draw`). Only panes with
  damage are redrawn.
- Chrome (sidebar, tab bar, overlays, the Overview dashboard) is rendered
  with Lip Gloss v2 into regions of the same screen buffer.
- Bubble Tea is removed in M3 rather than upgraded to v2: one rendering
  engine, no string round trip on the hot path.
- Until M3, the existing Bubble Tea v1 dashboard stays as it is. M0 fixed
  its input ordering and error reporting.

## Consequences

- One engine (ultraviolet) is shared by the emulator (ADR 0001) and the
  renderer. Cells flow from PTY to screen without being formatted as text.
- Hive takes on the event loop that Bubble Tea provided: focus, resize and
  message dispatch. That is acceptable for a multiplexer, which is mostly
  routing input to panes.
- ultraviolet has no tagged release (pseudo-versions). Pin it and cover it
  with golden-screen tests in `internal/tui/compositor`, so an upgrade that
  changes output fails CI rather than users.

## Amendment (M1, 2026-10-08)

The finding that "Bubble Tea v1 and v2 cannot live in one module" was wrong
about the cause. The conflict came from Lip Gloss v1 pinning an old
`charmbracelet/x/cellbuf` that does not compile against `x/ansi` v0.11.
Requiring `x/cellbuf` v0.0.15 resolves it; M1 does this to use `x/vt` next to
the Bubble Tea v1 dashboard. A migration can therefore be gradual if needed.
The decision itself (draw cells through ultraviolet in M3) is unchanged.

## Amendment (M3, 2026-10-09)

M3 implemented the decision; the dashboard and Bubble Tea are gone.

- **Shape.** `internal/tui/mux` is the app: one loop goroutine owns the UI
  state, daemon calls run in the background and post results back, and
  `Run` connects it to `uv.Terminal`. The compositor draws a `Scene` into
  the `uv.TerminalScreen`, whose renderer diffs cells. Pane screens are
  `vt.Screen`s fed by frame streams (ADR 0006), not local emulators: the
  shim already emulates, so the client only applies line deltas.
- **Damage.** Rather than redrawing only damaged panes, every frame is
  composed in full and the renderer's cell diff decides the output. Frames
  are paced instead: agent output is drawn at most every 8 ms, while input
  and the echo that follows it (within 50 ms) are drawn at once.
- **Chrome** is drawn by the compositor directly in cells, without Lip
  Gloss. The Overview mode replaces the dashboard and keeps its bee.
- **Tests.** The roadmap named `teatest` goldens, which need Bubble Tea.
  Golden screens take their place: compositor goldens per element, and app
  goldens per mode and overlay, driven against a real in-process daemon
  whose shell is `cat`.
- **Latency.** With 16 visible panes, fifteen printing 50 lines a second,
  a keystroke reaches a drawn frame in 1.5 ms at p50 and under 5 ms at p99
  on an M4 Pro (`BenchmarkInputLatency16Panes`; `TestInputLatency16Panes`
  enforces the 10 ms budget outside `-race`). Keys are spaced at
  auto-repeat speed: the daemon sends at most one frame per view per 8 ms
  (`terminal.FrameInterval`), so keys typed back to back measure that cap.
