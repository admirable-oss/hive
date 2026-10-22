# The desktop app

Hive in a window. It shows your environments, tabs and agents, with live
terminals. A tray (menu bar) item counts the agents that need you, and the
app notifies you when an agent is blocked or done.

The app works on the Hive daemon, the same one `hive` and `hive ui` use. It is
only a client: when you quit it, your agents keep working.

> **Preview.** The app is built on [Wails](https://wails.io) v3, which is in
> beta, so the app stays a preview until Wails v3 reaches a release
> candidate. Releases are signed and update themselves. See
> [status and limits](#status-and-limits).

## Install

Download the package for your system from the
[latest release](https://github.com/admirable-oss/hive/releases/latest):
- **macOS:** `hive-desktop_<version>_darwin_universal.zip`. Unzip it and
  move `hive-desktop.app` to Applications. It is signed and notarized, and
  bundles the `hive` it needs. Or use Homebrew:
  `brew install --cask admirable-oss/tap/hive-desktop`.
- **Windows:** `hive-desktop_<version>_windows_amd64_setup.exe`. It installs
  for your user only (no administrator needed), with `hive.exe` beside the
  app.
- **Linux:** the AppImage, `.deb` or `.rpm` for your architecture. Install
  `hive` itself as well, with `install.sh` or your package manager: the
  Linux packages do not bundle it.

Each release lists the packages' checksums in `desktop-checksums.txt`, signed
with the release key ([verifying a release](releasing.md#verifying-a-release)).

The app finds `hive` in this order:
1. `$HIVE_BIN`;
2. the copy bundled with the app;
3. `hive` on your `PATH`;
4. `~/.local/bin`.

When no daemon is running, the app starts one. It does this through your login
shell (macOS, Linux), so agents get your usual `PATH`. Opened from the Dock or a
launcher, the app otherwise has a bare environment.

**Requirements:**

| | macOS | Windows | Linux |
|---|---|---|---|
| Version | 13 and later | 10 and 11 | GTK 4 and WebKitGTK 6.0 (Ubuntu 24.04, Fedora 40 and later) |
| Webview | WebKit (built in) | WebView2 (preinstalled on 10 and 11) | WebKitGTK |
| Architectures | arm64, amd64 | amd64 | amd64, arm64 |

Windows is a client only, as everywhere in Hive ([platforms](platforms.md)).

## Updates

The app updates itself from GitHub releases, as `hive update` updates the
CLI:
- **It checks once a day,** a little after it starts. The check is the app's
  only request to the network. Turn it off with **Check for updates
  automatically** in the palette; **File → Check for Updates…** checks now.
- **It installs only verified releases.** The release's
  `desktop-checksums.txt` must be signed by the release key built into the
  app, and the package must match its checksum. Development builds have no
  key and never update.
- **It follows `update.channel`:** `stable`, or `preview` for release
  candidates too.
- **Installing restarts the app, never your agents.** **Restart to update**
  hands over to a small supervisor. It keeps the installed version beside
  the new one (`hive-desktop.app.old`), swaps the new one in and starts it.
  If the new version does not start within a minute, the supervisor puts the
  previous one back and starts that. Either way, the app says how it went.
- **The bundled `hive` moves with the app.** Updating the app never stops
  the daemon or your agents. A newer `hive` replaces an older daemon only as
  the [compatibility policy](compatibility.md#clients-and-daemons-of-different-versions) describes.

The app updates itself when it was installed from the release's zip
(macOS), with the installer (Windows), or as an AppImage (Linux). Installed
from a `.deb` or `.rpm`, it leaves updates to your package manager. Installed
with Homebrew, it still updates itself (the cask is marked `auto_updates`).

## Use

- **Sidebar.**
  - **Needs you** lists agents that are blocked, then those that are done.
  - Under it are your environments, with each one's git branch and tabs.
  - Click an item to open it.
- **Tabs and panes.** Panes are laid out as the daemon arranges them. The
  window's size becomes the tab's size, so `hive ui` in a terminal and the app
  can share a tab. Whichever client typed last decides the size.
- **Terminal.** Type as in any terminal. Input methods, dead keys, bracketed
  paste and the application cursor mode all work. Links (including OSC 8) open
  in your browser.
- **Copy and paste.** Select text and copy it with ⌘C, or Ctrl+Shift+C on
  Windows and Linux. Paste with ⌘V or Ctrl+Shift+V. Text a program copies with
  OSC 52 lands on your clipboard.
- **Agents.**
  - **New agent** starts one in an environment, optionally on a new git
    worktree branch.
  - **Prompt** sends a prompt to the agent in the focused pane. The app refuses
    if that agent is blocked: answer it in its pane first.
- **Scrollback.** ⌘F (Ctrl+Shift+F) opens the pane's scrollback as text,
  searchable. Enter goes to the previous match, Shift+Enter to the next.
- **Mouse.** Programs that ask for the mouse (editors, `htop`, some agents)
  get clicks, drags and the wheel. Hold Shift to select text instead.
- **Images.** Images agents print (the Kitty graphics protocol, as
  [in `hive ui`](plugins/image-graphics.md)) show over their cells. Hive decodes
  each one and encodes it again as a PNG before the window sees it, within the
  daemon's size limits. Images are cleared when the screen scrolls, as in
  `hive ui`.
- **Drops.** Drop files on the window to paste their paths, quoted for the
  shell, into the focused pane.
- **Moving panes.** Drag a pane by its handle (top left, on hover) onto
  another pane of the tab to swap the two. The daemon does the move, so
  `hive ui` sees it too.
- **Windows.** **Open in a new window** (⧉) shows a pane on its own, for a
  second screen. **View → Open Environment in New Window** shows an
  environment, with its tabs, in a window of its own. The main window
  reopens where you left it.
- **Closing the window** keeps Hive in the tray or menu bar. **Quit** ends the
  app; agents keep running.

### Keyboard

On macOS the shortcuts use ⌘. Elsewhere they use Ctrl+Shift, because Ctrl+D, W
and T mean things to shells.

| Action | macOS | Windows, Linux |
|---|---|---|
| Command palette (Goto): go to an agent, pane, tab or environment, or run an action | ⌘K | Ctrl+Shift+K |
| New agent | ⌘N | Ctrl+Shift+N |
| New tab | ⌘T | Ctrl+Shift+T |
| Prompt the agent | ⌘P | Ctrl+Shift+P |
| Split right / down | ⌘D / ⌘E | Ctrl+Shift+D / E |
| Zoom pane | ⌘Z | Ctrl+Shift+Z |
| Close pane | ⌘W | Ctrl+Shift+W |
| Next agent needing you | ⌘J | Ctrl+Shift+J |
| Toggle sidebar | ⌘B | Ctrl+Shift+B |
| Search scrollback | ⌘F | Ctrl+Shift+F |

**The prefix key works as in `hive ui`.** The app follows the `[keys]`
section of your configuration: press the prefix (Ctrl+B by default), then a
key bound in prefix mode. For example, `%` splits right and `o` goes to the
next pane. The prefix twice sends it to the program. Actions that belong to
the terminal UI (copy mode, popups, detach) say so. `hive config keys`
prints the bindings in effect.

On macOS, Option types characters (such as `ƒ`) by default. To make it Alt
in terminals instead, use **View → Option Key Types Alt** (or the palette). The
command palette also turns **Start Hive at Login** on or off.

## Observer window

**View → Open Observer Window…** opens a read-only window for a shared screen
or a second monitor. It shows agents with their states and, if you allow it,
their screens.

- **You choose what it may see:** everything on this computer or some
  environments, screens or states only, and for how long (1 hour to 7 days).
- **It gets its own observer credential** ([observers](observers.md)), made
  through your control connection. The window reads only through the daemon's
  read-only observer socket. The token stays in the app's Go side, and closing
  the window (or quitting) revokes the credential.
- **It cannot act.** It has no input, and the control service refuses every
  call from it.

This keeps a shared screen from typing into agents. It is not a sandbox
against the app itself, which holds full control of Hive anyway.

## Machines

The sidebar shows the agents of your saved machines (M7) beside this
computer's, under each machine's name, live. The app reaches each machine the
way `hive --machine` does: through `hive machine proxy <name>`, over ssh, with
your keys and `~/.ssh/config`. It connects only to machines that are enabled
and whose identity has not changed.

**Machines…** (in the sidebar, the File menu or the palette) does the
following:
- **Lists** the saved machines with their trust and connection, and lets you
  enable, disable or forget each one.
- **Finds** runtimes on the local network ([discovery](discovery.md)).
- **Pairs** a runtime found nearby, or any ssh destination.
- **Reviews** a machine whose identity changed, or one saved without an
  identity.

Pairing and trusting take two steps:
1. The app has the machine prove its identity, and shows what it proved:
   address, name, platform, identity and fingerprint. For a changed identity it
   also shows the old fingerprint. Nothing is saved yet.
2. When you confirm, the app trusts exactly the identity you saw. If another
   identity answers by then, nothing is saved.

### Notifications and the tray

The app notifies you when an agent enters a state listed in `notify.on`
(default: blocked and done), with these exceptions:
- it stays quiet about the pane you are looking at;
- it stays quiet when the daemon already notifies the system itself
  (`notify.system = true`), so you never get two notifications.

Clicking a notification opens that agent. On macOS the dock badge and the menu
bar item show how many agents are blocked. The tray menu lists the agents that
need you.

### Links

`hive://pane/<id>` and `hive://agent/<id>` open the app on that pane. Only one
copy of the app runs: a second launch passes its link to the first.

## Configuration

The app reads your Hive configuration through `hive config show`, so there is no
second settings file:
- `theme.name`: `auto` follows the system; `catppuccin-latte` is light; other
  themes are dark;
- `notify.on` and `notify.system`, as above;
- `[keys]`: the prefix key and its bindings (through `hive config keys`);
- `update.channel`: which releases the app updates to.

The app's own preferences are in `Hive/desktop.json` in your configuration
directory: the window's position, and whether it checks for updates.

`HIVE_HOME` and `HIVE_SESSION` choose the daemon, as they do for `hive`.

## Privacy and security

- **Same trust as `hive`.** The app talks to the daemon over its local socket,
  where the daemon checks the peer's credentials. It listens on no network
  port. It reaches no service except the daily update check (GitHub's
  release API and downloads), which you can turn off. It sends no
  telemetry.
- **Its page loads only its own code.**
  - A Content-Security-Policy allows no remote scripts, styles, frames or
    connections.
  - The page can call one Go service, with typed methods. Each method checks
    its input before anything reaches the daemon. None of them runs a command
    or reads a file.
  - Terminal links open in your browser, and only `http`, `https` and `mailto`
    links open at all.
- **What the page gets.** It never gets environment variables, command lines
  or arguments. It keeps screens in memory only, never in storage.
- **Pastes.** A pasted text cannot end its own bracketed paste: any end marker
  inside it is removed.

## Performance

These numbers come from a MacBook Pro (Apple silicon), measured by
`go test -bench . ./internal/core/` and the end-to-end test.

| | |
|---|---|
| Encoding a streaming frame (3 changed lines of a 220×50 screen) | 32 µs |
| Encoding a whole busy 220×50 screen (a keyframe) | 0.55 ms |
| Updates per pane | at most one per frame (16 ms), coalesced |

At these rates, 16 panes streaming at 60 updates a second take about 3 % of one
core in the app's Go side.

M18's budgets, measured on that machine. CI measures them on every change on
macOS and Linux, holding latency to 50 ms there: hosted runners are shared
and stall now and then.

| Budget | Measured |
|---|---|
| Keystroke to screen under 25 ms (p99): a key typed into a pane until the shell's echo is on screen | p50 17.0 ms, p99 17.3 ms |
| Idle with 16 panes on screen, under 1 % of a core | 0.00 % |
| The Go side's heap under 64 MB, with 16 panes | 14.4 MB |

The latency is measured in a browser over loopback (the end-to-end test),
not in the native webviews. It is one frame: updates are coalesced to one
per 16 ms. At these numbers the JSON frame channel is fast enough, so the
binary frame channel M18 kept in reserve is not needed.

## Status and limits

The app was built in M18 and made ready to install in M21
([roadmap](../ROADMAP.md)). What works:
- the workspace and live terminals, including scrollback search, mouse
  reporting, focus events and images (Kitty graphics);
- a read-only observer window with its own expiring credential;
- agents and prompts;
- layout: split, zoom, close, swap by dragging, new tab, pop-out windows for
  panes and environments;
- `[keys]`: the prefix key and its bindings, as in `hive ui`, and the Goto
  palette over agents, panes, tabs and environments;
- saved machines, discovery, pairing and identity review;
- notifications, the tray, the dock badge;
- `hive://` links and a single instance;
- file drops, start at login, restored window position;
- signed packages for macOS, Windows and Linux, attached to each release,
  and a Homebrew cask;
- updates that install only verified releases and roll back a version that
  does not start;
- builds for macOS, Windows and Linux, tested in CI, with latency and idle
  budgets;
- accessibility checks: axe, in both colour schemes, in a real browser.

Not yet:
- **Stable.** Wails v3 is in beta (`v3.0.0-beta.32`, pinned). The app leaves
  preview, and comes under the
  [compatibility policy](compatibility.md), once Wails v3 reaches a release
  candidate.
- **Windows on arm64.** The app is built for amd64; Windows on arm64 runs it
  emulated.
- **`hive` in the Linux packages.** Install it beside the app.

## Development

```sh
cd desktop
wails3 dev                    # hot reload: Vite and the Go app
wails3 build                  # bin/hive-desktop
wails3 package HIVE_CLI=../hive   # the .app / installer, with hive bundled
HIVE_DESKTOP_UNSIGNED=1 HIVE_CLI=../hive build/release.sh v0.0.0-dev   # a release's packages, unsigned
```

You need Go, Node.js 24 and `wails3`:
```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.32
```
Run `wails3 doctor` to check the platform dependencies.

How the code is laid out:
- `internal/core` is the engine, with no window: the connection, the view
  model, pane views and notifications.
- `main.go` binds the engine to Wails: windows, menus, the tray and the system.
- `frontend/` is React and TypeScript. Its `bindings/` are generated: run
  `wails3 task common:generate:bindings` after changing `HiveService`.

Tests:
- `go test ./internal/...` drives a real daemon.
- `cd frontend && npm test` runs the React tests.
- `go test -run TestDesktopEndToEnd .` runs the app as a server build (`-tags
  server`, for tests only) in headless Chrome.
