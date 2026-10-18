# The desktop app

Hive in a window. It shows your environments, tabs and agents, with live
terminals. A tray (menu bar) item counts the agents that need you, and the
app notifies you when an agent is blocked or done.

The app works on the Hive daemon, the same one `hive` and `hive ui` use. It is
only a client: when you quit it, your agents keep working.

> **Preview.** The app is built on [Wails](https://wails.io) v3, which is in
> beta. Packages from CI are unsigned previews; signed releases come later. See
> [status and limits](#status-and-limits).

## Install

- **Install it.** Download the package for your system:
  - macOS: `hive-desktop.app` (Hive in the Dock and the menu bar), which
    bundles the `hive` it needs;
  - Windows: the installer, which installs `hive.exe` beside the app;
  - Linux: an AppImage, `.deb` or `.rpm`.
- **macOS, unsigned previews.** On first launch, right-click the app and choose
  **Open**, or run
  `xattr -dr com.apple.quarantine /Applications/hive-desktop.app`.
- **Linux.** Install `hive` itself as well, with `install.sh` or your package
  manager.

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

Remote machines (`hive --remote`, saved machines) are not in the app yet; the
CLI and `hive ui` have them. Windows is a client only, as everywhere in Hive
([platforms](platforms.md)).

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
- **Windows.** **Open in a new window** (⧉) shows a pane on its own, for a
  second screen.
- **Closing the window** keeps Hive in the tray or menu bar. **Quit** ends the
  app; agents keep running.

### Keyboard

On macOS the shortcuts use ⌘. Elsewhere they use Ctrl+Shift, because Ctrl+D, W
and T mean things to shells.

| Action | macOS | Windows, Linux |
|---|---|---|
| Command palette: go to an agent or pane, run an action | ⌘K | Ctrl+Shift+K |
| New agent | ⌘N | Ctrl+Shift+N |
| New tab | ⌘T | Ctrl+Shift+T |
| Prompt the agent | ⌘P | Ctrl+Shift+P |
| Split right / down | ⌘D / ⌘E | Ctrl+Shift+D / E |
| Zoom pane | ⌘Z | Ctrl+Shift+Z |
| Close pane | ⌘W | Ctrl+Shift+W |
| Next agent needing you | ⌘J | Ctrl+Shift+J |
| Toggle sidebar | ⌘B | Ctrl+Shift+B |

On macOS, Option types characters (such as `ƒ`). It is not sent as Alt.

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
- `notify.on` and `notify.system`, as above.

`HIVE_HOME` and `HIVE_SESSION` choose the daemon, as they do for `hive`.

## Privacy and security

- **Same trust as `hive`.** The app talks to the daemon over its local socket,
  where the daemon checks the peer's credentials. It listens on no network
  port. It reaches no service except the update check, which does not exist
  yet. It sends no telemetry.
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

## Status and limits

The app is the first part of [M18](../ROADMAP.md). What works:
- the workspace and live terminals;
- agents and prompts;
- layout: split, zoom, close, new tab, pop-out windows;
- notifications, the tray, the dock badge;
- `hive://` links and a single instance;
- builds for macOS, Windows and Linux, tested in CI.

Not yet:
- **Signed releases.** That means Developer ID signing and notarization,
  Windows code signing, signed Linux packages, a Homebrew cask, and attaching
  packages to tagged releases. CI builds unsigned packages on demand.
- **In-app updates,** which will check the release signature as `hive update`
  does.
- **Remote and paired machines** (M7, M17), and a read-only observer window
  (M14).
- **Terminal features:** images (Kitty, iTerm2, Sixel), scrollback and search,
  and mouse reporting to programs. `hive ui` has these.
- **App features:** start at login, restoring window positions, file drops,
  and an Option-as-Meta setting.

## Development

```sh
cd desktop
wails3 dev                    # hot reload: Vite and the Go app
wails3 build                  # bin/hive-desktop
wails3 package HIVE_CLI=../hive   # the .app / installer, with hive bundled
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
