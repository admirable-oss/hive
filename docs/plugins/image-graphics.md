# M11: Image graphics over terminal panes

Status: live (v1.0.3), 2026-10-15

## Evaluation

Hive's rendering path runs agent PTY output through a VT emulator, line-based frames, a cell compositor, then the outer terminal renderer. The current screen/frame types contain cells, cursor, title, modes, bells, and clipboard text. They have no image resource, image placement, or terminal graphics capability state.

Kitty graphics commands are APC sequences with base64 payloads. Uploads may be chunked; image data and placements are separate; responses are asynchronous. Graphics therefore cannot be represented as text cells or blindly copied to the outer terminal. Hive needs a stateful parser at the PTY boundary, a bounded graphics side channel, and a renderer gated by explicit outer-terminal capability and agent-integration support. The Kitty protocol provides an explicit query; do not infer support from TERM. The initial outer-terminal probe now sends a correlated Kitty query, DA1, and iTerm2 feature query. iTerm2 can be handled through its Kitty implementation; native iTerm2 file transfer has no equivalent standalone capability probe.

## Implemented renderer support

- Detect outer-terminal protocols independently from agent image-input support. The initial probe recognizes Kitty, iTerm2 inline files only when its feature report advertises `FILE`, and Sixel via DA1 or iTerm2 feature reporting. Select one rendering protocol deterministically (Kitty, iTerm2 inline image, Sixel, then ANSI truecolor half-block cells). Do not infer support from `TERM` or treat formats as interchangeable.
- **Parser foundation added:** an optional VT callback observes syntactically valid Kitty APC commands across arbitrary PTY read boundaries, with an 8 MiB encoded-command cap and a 4 KiB header cap. Like the emulator, it follows the VT string rules: CAN or SUB aborts a command, and any ESC not followed by `\` ends it unterminated, so the observer and the screen always agree on where a command ends. Header fields are split and duplicate or malformed fields are rejected. An explicit relay validator accepts direct-data (`t=d`) only, caps encoded chunks at 4096 bytes, checks base64, chunk flags, format values, and image identifiers, and refuses file/shared-memory sources. It does not rewrite PTY bytes or synthesize replies. A Go assembler now enforces Kitty’s 4096-byte encoded chunk size, base64 validity, continuation header shape, and a 16 MiB image-data cap and an 8192-chunk cap. Empty or padded non-final chunks are rejected to prevent stalled or malformed uploads. It emits a static image upload only after its final chunk; unsupported animation, placement, and delete actions are rejected by this upload path. Raw RGB/RGBA uploads must declare positive dimensions matching their exact byte count; dimensions are capped at 16384 per side and 16 megapixels. PNG uploads are fully decoded with the same pixel cap, and optional declared dimensions must match the PNG header. A separate `terminal.graphics`/`shim.graphics` stream now replays validated direct-data APC commands in order, independent of cell-frame coalescing; file and shared-memory sources are excluded. The per-session journal is capped at 32 MiB and returns an explicit overflow error rather than silently losing commands.
- Image bytes stay out of cells and ordinary line updates. The `terminal.graphics`/`shim.graphics` stream carries cursor-anchored commands separately from coalesced frames. The mux now remaps image IDs per view, replays placements when pane geometry changes, and emits only explicit `c`/`r` placements fully contained in the pane. A pane delete or screen restoration removes Hive-owned placement and image IDs. A detected scroll clears screen-anchored placements; images do not enter cell scrollback and are not restored from it. Journal and viewer overflow reset graphics state instead of leaving a partial upload displayed.
- The mux routes outer Kitty acknowledgements to the originating view using its remapped image or query ID; the startup capability-probe response is consumed before view routing. Unsupported terminals receive an explicit `ENOTSUP` response to Kitty queries.
- Placements are anchored to the agent cursor and translated into pane coordinates. This increment suppresses any placement that would partially cross the pane viewport. Resize reuses the placement ID to move the image; detected scroll removes placements instead of attempting to reconstruct image positions from cell-only scrollback.
- Future work: move the per-pane resource table into negotiated frame/keyframe state, anchor placements to logical output positions, and release resources with scrollback eviction. The current mux-only state rebuilds from the bounded command journal when a view reopens. The ANSI cell renderer is the generic fallback; native Sixel and iTerm output fall back to ANSI cells when a placement cannot be encoded within its output limits.
- On detach and terminal restoration, delete only Hive-owned Kitty placements and image IDs. Reattached views receive the bounded event history and rebuild visible placements. Sixel has no image identity/delete operation, so when the visible Sixel set changes Hive invalidates the cell renderer, clears the alternate screen, redraws the UI, then redraws all visible Sixel placements. Sixel cache memory and each frame's output are bounded; unsupported crop offsets and partial pane overlaps are suppressed. Terminal-specific Sixel erase behavior can vary; leaving alternate screen restores the prior display. Unsupported terminals use normal cell rendering only. Kitty scrollback anchoring and exact pixel clipping remain unsupported.
- Image paste is a separate prefix action (`I`): it reads PNG bytes from the local clipboard (macOS `pngpaste` or NSPasteboard via JXA; Wayland `wl-paste`; X11 `xclip`), bounded by `[images] max_kb`, and sends them with `terminal.image` (below). Existing text paste is unchanged.

## Image transfer (local and remote)

`terminal.image` is a pipe method. The open request carries metadata only: process, transfer ID, format (`png`), byte size and SHA-256. The image follows as ordinary pipe data, so it is split into the connection's 32 KiB data messages. It never goes in a request, in `terminal.input`, or in the keystroke channel of a view. Control and keystroke messages for other panes interleave with those chunks. On a remote link the client also paces the upload (`[remote] image_budget_kb`, 1 MiB/s by default), so its bytes do not queue ahead of keystrokes in SSH's buffers.

The daemon delivers nothing until the transfer is complete and valid:

1. At open it checks the metadata against its own limits (`[images] max_kb`, hard cap 16 MiB), the transfer ID shape, and the destination's declared image input. A refusal costs no upload.
2. It reads exactly the declared size into a buffer of that size. At most 4 transfers are held at once (others get `unavailable`), and a client that sends nothing for 30 s is cut off (`timeout`).
3. It verifies the SHA-256 and the PNG header: dimensions positive, each side ≤ 16384, pixels ≤ `[images] max_megapixels`. Pixels are not decoded.
4. It delivers in the form the destination agent declares in its manifest (`[input] image`):
   - `path`: the image is saved as `hive-image-<transfer>.png` in `<hive home>/<session>/images` (directory 0700, file 0600, written via a temporary file and a rename). Its path is typed into the pane, bracketed when the agent enabled bracketed paste. Claude Code and Codex attach such paths as images, and their built-in manifests declare `path`.
   - `kitty`: the image is typed as Kitty direct-transmission commands (`a=t,t=d,f=100,q=2`, continuations carry only `m` and `q`). Each complete APC command is one PTY write, so keystrokes from other views can fall between commands but never inside one. If a write fails part-way, a final empty chunk ends the upload.
   - Undeclared (shells, unrecognised programs, agents without `[input]`): refused with `unsupported`. Hive never types escape sequences into a program that did not ask for them.
5. It answers with a receipt line (`delivery`, `path`).

**Interrupted transfers and retries.** If the link drops or the pipe closes early, the partial buffer is discarded and the pane's input is untouched. The client retries up to three times with backoff, reconnecting (over SSH, a new `hive proxy`) under the same transfer ID. The daemon remembers delivered transfers for 10 minutes and a retry of one gets `done` at open, so a retry after a lost receipt is never delivered twice. A retry that races a delivery still in progress is told to try again. Refusals (too large, not a PNG, digest mismatch, no image input) are final and are not retried.

**Destination capabilities.** The daemon that runs the agent decides how, and whether, it takes images. Its limits and manifests apply, not the client's. A client paste is bounded by the client's own `max_kb` as well, so the stricter side wins. The UI's outer-terminal graphics capabilities (rendering) are independent of image input.

### Sensitive data

Clipboard images and images that agents print can hold secrets.

- **Clipboard.** Hive reads the clipboard only when you press the paste key. It reads only PNG data and never falls back to text, HTML or URLs.
- **In transit and in memory.** Image bytes travel inside the existing connection (local socket, or the SSH tunnel to `hive proxy`) and are held in memory only while a transfer runs or a pane shows the image.
- **Logs.** Agents' output logs (`stdout.log`, kept for `[retention] exited_processes`) record what agents print, but Kitty graphics commands are stripped from them before writing, so image data never reaches them. `[images] log_graphics = true` keeps them, for debugging a graphics-producing tool. Text around the images is logged as usual.
- **Never stored.** Session snapshots and replay history store screen text only, and the UI's per-pane graphics state and the daemon's graphics journal (32 MiB per pane) live in memory only. Neither is written to disk.
- **Saved pastes.** The only copy on disk is a `path` delivery's file, private to your user and removed after `[images] keep` (default 1 h; at most 64 files, oldest first). `keep = "0s"` turns saving off, and agents that take a path then refuse pasted images.
- **Shared screens.** Agents on a remote machine see the image only after you paste it. Anyone who can read your files on that machine (root, backups) can read a saved paste until it expires.

## Resource bounds

| What | Bound | When exceeded |
|---|---|---|
| Pasted image (client and daemon, each by its own config) | `[images] max_kb`, default 5 MiB, hard cap 16 MiB; `max_megapixels`, default 16, hard cap 64; 16384 px per side | refused before upload (size) or after (pixels), nothing delivered |
| Transfers in flight per daemon | 4 | `unavailable`, retried by the client |
| Stalled transfer | 30 s without data | `timeout`, buffer freed |
| Remote upload rate | `[remote] image_budget_kb`, default 1 MiB/s | paced, never refused |
| Graphics a remote client is sent | the client's frame budget (`[remote] frame_budget_kb`), shared with screen frames | paced; frames coalesce meanwhile |
| One relayed graphics command | 4 KiB header + 4 KiB payload; reader cap 16 KiB per event | command not relayed / stream refused |
| One observed APC in the parser | 8 MiB | command dropped, bytes still reach the emulator |
| Daemon graphics journal | 32 MiB per pane | stream ends with an overflow error; viewers reset that pane's graphics |
| One assembled image | 16 MiB decoded, 8192 chunks, 16 MP | upload refused |
| UI: queued events per pane / all panes | 32 MiB / 64 MiB | that pane's graphics reset |
| UI: decoded images per pane / all panes | 32 MiB / 96 MiB | image named as `[image]` text instead of drawn |
| UI: Sixel or iTerm2 output per frame | 16 MiB (iTerm2 sequence 900 KiB) | placement drawn as cells |

Image transfer cannot starve ordinary traffic. Pasted images move as 32 KiB data messages that interleave with keystrokes and control requests on the same connection, and on a remote link they are paced. Printed images reach a remote client within the byte budget it shares with screen frames, and frames coalesce rather than queue behind them.

## Rendering modes and fallbacks

`[images] render` picks how images that agents print are drawn:

- `auto` (default): the protocol the outer terminal confirmed at startup, in order Kitty, iTerm2 inline images, Sixel, else coloured half-block cells.
- `cells`: always coloured half-block cells (`▀`, 24-bit colour), even where the terminal has a graphics protocol.
- `text`: a dim `[image]` placeholder in the first row of each placement. Nothing is decoded. This is the text-only fallback for terminals without 24-bit colour, screen readers, or slow links.
- `off`: images are not drawn at all; the agent's text is unaffected.

Wherever an image cannot be drawn as pixels (invalid data, cropped placements in cell mode, or past the memory bounds above), it is named as `[image]` text rather than silently dropped. Kitty queries from agents get `ENOTSUP` unless the outer terminal speaks Kitty.

## Terminal compatibility

Detection is by probe, never by `TERM`: at startup the UI sends a Kitty graphics query, DA1 and an iTerm2 capability query, and uses what the terminal confirms. The table is what each terminal is expected to confirm given its documented protocol support. The probe's answer wins, and `render` overrides it.

| Outer terminal | Expected output | Notes |
|---|---|---|
| Kitty, Ghostty | Kitty | Native placements; exact pane clipping is not supported, so placements crossing a pane edge are hidden. |
| WezTerm, Konsole | Kitty (else Sixel) | Partial Kitty implementations; fall back to `render = "cells"` if images misplace. |
| iTerm2 | Kitty if it answers the query, else iTerm2 inline images (`F`) | Inline images are re-sent when placements move. |
| foot, xterm with Sixel, Windows Terminal 1.22+, mlterm | Sixel | Sixel has no delete: the UI clears and redraws when the visible set changes. |
| VS Code terminal | Sixel or iTerm2 when its image support is on, else cells | |
| Alacritty, GNOME Terminal (VTE without Sixel), Apple Terminal | cells | On terminals without 24-bit colour, set `render = "text"`. |
| tmux, screen, or an SSH hop to another multiplexer between Hive and the terminal | usually cells | The multiplexer hides the outer terminal's probes. Run `hive ui` outside them, or use `hive --machine` from the local terminal. |
| Linux console, `TERM=dumb` | text recommended | No glyph or colour support for cells. |

Pasting images does not depend on the outer terminal at all; see the limitations below.

## Limitations

- **SSH.** Transfers ride the existing `ssh … hive proxy` connection, so no extra ports or forwarding are needed. The remote daemon must be this version or newer (`hive machine update`); older ones refuse with a message to update Hive there. Transfers larger than SSH's window take seconds on slow links, and `image_budget_kb` trades speed for typing latency.
- **Clipboard.** The clipboard is read on the machine running `hive ui`, never on the remote host. macOS needs `pngpaste` or permission for `osascript` (JXA) to read NSPasteboard. Wayland needs `wl-paste`; X11 needs `xclip`. Clipboard managers that only expose file URLs or TIFF without a PNG conversion are not read. Over plain SSH without a local `hive ui` (e.g. `ssh host hive ui`) there is no local clipboard to read, so use `hive --remote host ui` instead. OSC 52 cannot carry images.
- **Terminal emulators.** Image paste does not depend on the outer terminal: it bypasses it entirely. Rendering images that agents print does depend on it (Kitty, iTerm2 `FILE`, Sixel, else half-block cells; see above). tmux or screen between Hive and the terminal usually hides graphics capabilities, so Hive falls back to cells.
- Only PNG is accepted; an animated PNG is passed on unchanged.
- Images agents print are anchored to the screen. They are removed when the pane scrolls and are not kept in scrollback. Kitty placements that would cross a pane edge are hidden rather than clipped. Agents that read images from neither paths nor Kitty input cannot receive pasted images.

## Testing and measurements

- **Fixtures.** `internal/vt/testdata/kitty-graphics.txt` covers 41 sequences: valid PNG and raw uploads, chunked and interrupted uploads, file, temp-file and shared-memory sources, bad base64, oversized and padded chunks, malformed headers, CAN/SUB aborts, sequences cut by other escapes, and graphics between text. Each is fed whole, split at every byte, and byte by byte, and must give the same outcome without printing.
- **Fuzzing.** `FuzzKittyGraphicsParser` runs nightly with the other fuzz targets.
- **Transfers.** `terminal.image` tests cover each delivery form, malformed payloads, interrupted transfers, a link cut mid-upload (with retry delivering exactly once), and refusals. Rendering tests cover every output mode, the shared memory budget, scroll clearing, and unsafe commands.
- **Workflows.** Local and remote workflows are tested separately against a real daemon. The remote test uses a remote client's settings over a link limited to 4 MiB/s.
- **Measured on an M-series Mac (Go 1.26, `-race` off):**
  - A local 1 MiB paste end to end (upload, checks, saving, typing the path) takes 7.5 ms (≈140 MB/s); a 256 KiB paste takes about 20 ms.
  - Over the throttled remote link at the 512 KiB/s test budget, 1 MiB took 2.0 s, and keystrokes sent meanwhile on the same connection had a median round trip of 12 ms and a worst of 17 ms.
  - Per frame, for a 512×512 image in 40×20 cells: Kitty 0.24 µs (placements only), text 0.43 µs, cells 61 µs, and Sixel 2.3 ms or iTerm2 1.9 ms when the placement moved (otherwise reused).
  - Decoding a new upload for cell output takes about 2.4 ms once.

  Rerun with `go test -bench 'TerminalImage' ./internal/client/tests` and `go test -bench 'Graphics(Frame|Upload)' ./internal/tui/mux`.

## Zig and Go boundary

Keep Go responsible for PTY parsing, protocol state, transport, lifecycle, and the complete fallback path. Do not put terminal I/O or Kitty parsing behind cgo: these are stateful and I/O-bound, while cgo complicates Hive's current `CGO_ENABLED=0` and cross-compilation builds. Zig can provide an optional narrow C ABI for a CPU-bound image transform or copy path only after profiling shows it is worthwhile. The pure-Go implementation must remain complete when Zig/cgo artifacts are unavailable.

## Implementation order

1. **Done:** explicit outer-terminal capability detection, bounded Kitty observation and static-image validation, daemon/shim streaming, cursor-anchored per-view state, pane-contained Kitty placement, resize replay, acknowledgement routing, scroll clearing, and screen-restoration cleanup are implemented. The mux selects detected Kitty, iTerm2 `FILE`, or Sixel output; ordinary terminals get a Unicode truecolor half-block renderer. Partial placements are suppressed because Kitty has no pane clip rectangle.
2. Add true clipping, logical scrollback anchoring, Sixel cleanup/clipping, and image resources in keyframes before supporting those cases.
3. **Done:** pasted images travel on `terminal.image`, separate from keystrokes and requests, bounded, validated before delivery, and delivered at most once per transfer however often it is retried. Delivery follows the agent's declared input (`path` for Claude Code and Codex, or `kitty`). Next: agent-native attachment APIs where agents expose one, and measured upload overhead on slow links.
4. Consider an optional Zig transform after profiling. The measurements above do not call for one: the costliest path, Sixel re-encoding, runs only when a placement moves.

## References

- [Kitty graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/)
- Hive VT model: `internal/vt`
- Hive renderer/compositor: `internal/tui/mux`, `internal/tui/compositor`
