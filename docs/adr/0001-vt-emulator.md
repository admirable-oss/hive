# ADR 0001: Terminal emulation

- Status: Accepted, with a performance gate (see Consequences)
- Date: 2026-10-08
- Milestone: M0 spike, implemented in M1

## Context

Today the daemon replays up to 64 KiB of raw PTY bytes to a client that
attaches. The replay can start in the middle of an escape sequence, and it
cannot reconstruct a full-screen application's screen, so reattaching to
Claude Code, Codex, vim or htop shows garbage until the program redraws. M1
needs a real virtual terminal in each pane's process (the shim). It powers
correct reattach, `pane read`, agent-state detection on screen regions, and
the multiplexer's pane rendering.

Constraints from the roadmap: pure Go (no cgo, so cross-compiling stays
trivial), correct on the agents people actually run, and a budget of about
100 panes on a laptop.

Candidates:

- `github.com/charmbracelet/x/vt`: the Charm emulator, built on `x/ansi`
  and `ultraviolet`, the same stack as Bubble Tea v2.
- `github.com/hinshun/vt10x`: small and fast, unmaintained since 2022.
- libghostty-vt via cgo (what herdr uses). Excluded by the pure-Go
  constraint; kept as a fallback if both Go options fail.

## Method

The code is in `spikes/vt` and runs with `go run . conformance|record|perf`.

1. **Conformance.** 35 synthetic cases with screens computed by hand:
   - cursor movement, erasing, autowrap and pending wrap;
   - scroll regions, IL/DL/SU/SD and origin mode;
   - DECSC/DECRC and alternate screen 1049;
   - wide, emoji and combining characters, and wide characters at the margin;
   - tab stops and DEC line drawing;
   - ICH/DCH/ECH;
   - an Ink-style redraw (Claude Code), synchronized output (2026), OSC 8
     and OSC titles, and truecolor SGR;
   - UTF-8 split across writes.
2. **Real sessions.** vim, less, top and a coloured shell script were
   recorded in an 80x24 PTY while a key script played. Each recording was
   replayed in 4 KiB chunks, as a PTY reader delivers it, into both
   emulators. Screens were compared at every key press (24 checkpoints).
3. **Performance.** 32 MiB of synthetic agent output (coloured log lines,
   periodic Ink-style status redraws, CJK) were fed into a 220x50 screen in
   4 KiB chunks. Memory was measured per emulator.

## Results

| | x/vt | vt10x |
|---|---|---|
| Synthetic conformance | **34/35** | 33/35 |
| UTF-8 split across writes | ok | **drops the character** |
| Wide char at right margin | drops it (bug) | overflows the margin (bug) |
| Real sessions, 24 checkpoints | 22 identical; the other 2 are vt10x bugs, x/vt correct | — |
| Answers DSR / DA queries | yes (`\x1b[3;7R`) | no |
| Throughput (220x50, 4 KiB chunks) | **3.8 MB/s** | 34.9 MB/s |
| Allocation | ~47 bytes per input byte | — |
| Memory per emulator, empty | **6.8 MiB** (112-byte cells, several buffers) | small |
| Memory with 10k scrollback lines | **~56 MiB** | — |

The profile of x/vt shows 42% of the time in `runtime.madvise` (garbage
collector churn) and 25% in `ultraviolet.(*Buffer).DeleteLineArea`
(scrolling copies every line of cells).

One integration finding: x/vt writes query replies (cursor position, device
attributes) to a synchronous pipe inside `Write`. If nothing reads `Read()`
concurrently, `Write` deadlocks on the first query an application sends.

## Decision

Adopt **x/vt** as the emulator, behind a Hive-owned port:

```go
// internal/vt
type Emulator interface {
	Write(p []byte) (int, error) // PTY output in
	Replies() io.Reader          // query answers out, drained into the PTY
	Resize(cols, rows int)
	Snapshot() Screen            // cells, cursor, modes, title
	Damage() []Rect
	Draw(dst uv.Screen, area uv.Rectangle)
}
```

vt10x is rejected. Losing UTF-8 that is split across reads breaks every
non-ASCII agent, it cannot answer queries, and it is unmaintained.

Rules for M1:

1. **Hive owns scrollback.** x/vt keeps no scrollback (`SetScrollbackSize(0)`).
   Lines that scroll off are compacted into Hive's own store: runs of styled
   text, block-compressed, with a byte budget per pane (default 10 MiB). This
   moves the 56 MiB/pane cost out of the emulator.
2. **Replies are always drained.** The shim runs one goroutine copying
   `Replies()` into the PTY for the emulator's whole life. A unit test writes
   `ESC[6n` and must not block.
3. **Conformance is a gate.** The cases from `spikes/vt` move into
   `internal/vt/testdata` as golden tests. They include the wide-at-margin case
   as a known failure, to be fixed upstream or in our adapter.

## Consequences

- Correctness is solved with a maintained, pure-Go dependency from the same
  family as the rest of the UI stack, and x/vt can draw directly onto an
  ultraviolet screen (see ADR 0002).
- **Performance gate.** At 3.8 MB/s and 6.8 MiB per pane, x/vt is about 10x
  off the 100-agent budget. Before M4 (agent intelligence at fleet scale),
  the emulator must reach **at least 50 MB/s and at most 1 MiB per 220x50
  pane** on the `spikes/vt` benchmark. In order of preference:
  1. upstream fixes: scrolling by rotating line slices instead of copying
     cells, and fewer allocations per byte;
  2. a fork into `internal/vt/ansi` that keeps x/vt's parser and replaces
     storage with a compact cell (packed rune index plus a style-table index,
     about 12 bytes);
  3. a cgo build of libghostty-vt behind the same port, as a build tag.

  The port isolates all three, so callers do not change.
- Until the gate is met, panes whose agent is not visible may be emulated
  lazily (bytes buffered, parsed on demand). The shim design must allow that.

## Amendment (M1, 2026-10-09)

The rules above are implemented in `internal/vt`. The synthetic cases are in
`vt.conformance_test.go`, and the recorded corpus is in
`testdata/recorded/`: vim, less and a colour script captured with
`docs/adr/spikes/vt` (`go run . dump`). Each recording must reproduce its
reviewed golden screen with 4 KiB and 7-byte chunks, through frames and
through the painter. `top` is recorded by the spike but kept out of the
corpus because it shows the recording machine's processes.

Scrollback is Hive-owned as decided: x/vt's own scrollback is only a
staging area, drained after every write into `vt.Scrollback` (byte budget
`terminal.scrollback_mb`). It is readable through `terminal.snapshot
{"scrollback": N}`. The performance gate still stands; see ADR 0006 for the
measured memory per shim.
