# ADR 0006: Shims, protocol 2 and screen frames

- Status: Accepted
- Date: 2026-10-08
- Milestone: M1

## Context

M1 had three acceptance criteria:

1. `kill -9` the daemon, start it again, and every agent is still running with
   an identical screen.
2. Reattaching to an agent like Claude Code shows the correct screen at once.
3. With 50 agents producing output and a throttled client, nothing is
   corrupted.

Before M1, agents were children of the daemon and died with it. A client
attaching got the last 64 KiB of raw output, which is garbage for full-screen
programs, and a slow client silently lost bytes.

## Decisions

### 1. One shim per agent

Every agent runs under `hive __shim <dir>`, the same binary, started with
`setsid` in its own session and detached from the daemon. The shim owns the
agent's PTY, its terminal emulator (package `vt`), its logs and its exit
status. It serves them on `run/<id>/shim.sock` using protocol 2, and records
`state.json` (running, exited with a code, or failed to start).

The daemon is only a client of the shims. When it stops, crashes or is
upgraded, nothing happens to the agents. The next daemon adopts them in
`process.Recover`:

| Shim state on adoption | Result |
|---|---|
| Answers on its socket | Re-attached; supervised as before (`process.recovered` event) |
| `state.json` says exited | The exit code is recorded; the shim is released and its directory removed |
| Directory present, shim not answering | Treated as gone. If the PID is provably ours (ADR 0005), it is stopped |
| No directory (a pre-shim record) | ADR 0005's orphan reaping |

A shim whose agent exited waits up to 30 s for the daemon to collect the
result (`shim.release`), then exits on its own; `state.json` keeps the result.

Alternatives considered:

- **A single server holding every PTY, with fd passing for upgrades**
  (herdr's "live handoff"). It is fragile: Unix only and experimental there,
  and a crash of the one server still kills every agent.
- **A double fork with no supervisor process.** There is then nothing to
  hold the PTY master; when the daemon dies the agent receives SIGHUP.

### 2. Shutdown semantics

- `hive stop` and `hive daemon stop` stop the agents too, because the user
  asked for that explicitly. This is `runtime.shutdown {"stop_agents": true}`,
  which is also the default for protocol-1 clients.
- `hive daemon restart`, signals (Ctrl+C, SIGTERM from launchd or systemd)
  and crashes leave agents running. `runtime.shutdown {"stop_agents": false}`
  does the same.
- Service definitions keep this true: systemd gets `KillMode=process`,
  launchd gets `AbandonProcessGroup`.
- In-process mode (tests, `Config.Shim == nil`) cannot keep agents across a
  stop, so `Detach` stops them. Whether a process can be detached is decided
  by its session, not by the handle wrapping it. A bug where every terminal
  handle claimed it could be detached was caught by the 50-agent test and
  pinned by `TestService_DetachStopsInProcessTerminals`.

### 3. Screens move as frames, never as raw bytes

Each session emulates its terminal. Viewers receive frames: a keyframe, then
whole changed lines plus cursor, title, modes and bell count. Frames go
through a compact binary codec (`vt.AppendFrame`, capability `frames/1`).

A viewer's next frame is computed from the screen as it is at the moment the
viewer has taken the previous one. So:

- A slow viewer gets fewer, larger frames and can never see a gap. The
  50-agent test: 692 frames for 20,000 printed lines, and every final screen
  matches exactly.
- Joining late is a keyframe, so the screen is correct at once.
- Backpressure is end to end: client, daemon, shim and frame loop. Nothing in
  the chain needs a queue of frames.

`terminal.attach` paints frames as ANSI on the server (`vt.Painter`). The
CLI therefore needs no emulator, protocol-1 clients keep working, and the
terminal the user attaches from never sees the agent's queries. With raw
passthrough the outer terminal would have answered them too, giving double
replies. The cost is that sequences outside the screen model (OSC 52
clipboard, images) are not passed through yet; that comes later.

`terminal.frames` hands the frames themselves to clients that draw (the
dashboard).

### 4. Size arbitration

Each viewer joins with its size. An attach takes over the terminal size at
once. Other viewers take it when they type: the last client to interact wins,
as in tmux and herdr. Implemented in `terminal.Service` (`Join`, `Interact`,
`ResizeView`).

### 5. Protocol 2

Same NDJSON framing. A client that opens with `hello` gets a `welcome`
listing the server's methods and capabilities, and the connection becomes
multiplexed:

- concurrent requests;
- any number of pipes (`stream` IDs, `data` and `close` messages);
- bounded queues, and pipe input that stalls for 30 s closes that pipe;
- a single connection per client, re-dialled after a daemon restart.

Without a hello, the connection is protocol 1, so `nc` and older clients
still work. The client falls back to protocol 1 against an older daemon
(`ErrProtocol1Only`). Pipes replace the old connection hijack for both
protocols.

The daemon and its shims speak protocol 2 too. A shim built by an older
binary keeps serving after an upgrade, and the shim API only grows.

### 6. Events

The `event.Bus` publishes `environment.*` and `process.*` (started, exited,
recovered). Subscribers have bounded queues; one that falls behind gets
`events_lost` and re-reads its state. The dashboard subscribes instead of
polling the process list, and polls only while the stream is down.

## Consequences

- **Memory is the main cost.** Measured with a release build: a shim at the
  default 220x50 size is about 21 MiB RSS (18 MiB with `GOGC=50`), and the
  daemon is about 12 MiB. Most of a shim is the x/vt emulator's cell buffers
  and GC headroom, not the extra process: Go's runtime overhead per process
  is about 3–4 MiB. The ADR 0001 gate (at most 1 MiB per pane before M4)
  addresses this. Until then, a 100-agent machine needs about 2 GB.
- Agents now outlive everything except `hive stop` and a reboot. Restoring
  after a reboot is M6.
- Unix socket paths are limited to about 104 bytes. Shims check this and
  ask for a shorter `HIVE_HOME`.
- Tests run shims by re-executing the test binary (`HIVE_SHIM_TEST_EXEC`,
  `HIVE_CLI_TEST_EXEC`). Race-enabled children need
  `GORACE=atexit_sleep_ms=0`, or they linger a second after exiting, and
  shim tests check the children's stderr for data races.

## Amendment (M3, 2026-10-09): hyperlinks and output activity

**frames/2.** Cells carry OSC 8 hyperlinks (`vt.Cell.Link`), and frames
can too: a frame with the links flag carries each run's link, and runs
split where links change. Every peer that speaks it offers `frames/2`
beside `frames/1` (clients and the daemon in their hello, the daemon and
shims in their welcome), and a server sends links only to a connection
whose hello offered it (`protocol.PeerHas`). Older peers keep working:

| Writer → reader | Encoding |
|---|---|
| new shim → old daemon | frames/1: the old daemon's hello offers nothing |
| old shim → new daemon | frames/1, which the new decoder reads |
| new daemon → old client | frames/1 |
| new daemon → new client | frames/2 |

Links are dropped when they contain control characters or exceed 2048
bytes, since clients pass them to the user's terminal inside OSC 8. The
multiplexer does, so links stay clickable in terminals that support them,
and ctrl-click opens a cell's link before looking for a URL in its text.
Scrollback keeps links (in memory only).

**process.output.** The UI marks tabs whose agents print while hidden.
Rather than stream every hidden pane, the daemon watches each session as
a slow viewer, taking one frame per second: per the frame contract above,
that one frame covers everything that changed meanwhile, so a busy agent
costs one frame computation a second and an idle one nothing. The
runtime publishes `process.output` `{"id"}` at most once a second per
agent.
