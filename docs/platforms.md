# Platforms

Hive runs the same daemon, protocol, workspace model, agent detection and
plugin system on every platform. What differs is the operating-system layer
underneath — `internal/platform`, `internal/pgroup` and the terminal device in
`internal/terminal` — and this page says where those differences show.

## Support matrix

| | macOS | Linux | Windows |
|---|---|---|---|
| Versions | 13 and later | glibc or musl, kernel 4.x and later | Windows 10 1809 (build 17763) and later, Windows 11, Server 2019 and later |
| Architectures | arm64, amd64 | amd64, arm64 | amd64 |
| CI | `macos-latest` (race) | `ubuntu-latest` (race) | `windows-latest` |
| Terminals | PTY | PTY | ConPTY |
| Process trees | process groups | process groups | job objects |
| Daemon socket | Unix socket, peer UID checked | Unix socket, peer UID checked | AF_UNIX socket, peer SID checked |
| Login service | launchd agent | systemd user service | Task Scheduler task at logon |
| Install | Homebrew cask, `install.sh` | `install.sh`, Nix flake | `install.ps1` |
| Self-update and rollback | ✓ | ✓ | ✓ |
| Agents survive daemon restarts | ✓ (shims) | ✓ (shims) | ✓ (shims) |
| Strict plugin isolation | `sandbox-exec` | `bwrap` | not available |
| Remote machines (`--remote`) | client and host | client and host | client only |

**Architecture policy.** A platform/architecture pair is *supported* when CI
builds and tests it on every change and releases ship it. Windows arm64 is not
built yet: it is added when CI can run it. Builds for other systems may compile
but are not supported.

## Windows

### Installing, upgrading and removing

```powershell
irm https://raw.githubusercontent.com/admirable-oss/hive/main/distribution/install.ps1 | iex
```

The installer downloads the release `.zip` for windows/amd64, checks it against
the release's `checksums.txt`, puts `hive.exe` in
`%LOCALAPPDATA%\Programs\Hive` (`$env:HIVE_INSTALL_DIR` overrides it) and adds
that to your user `PATH`. `$env:HIVE_VERSION = 'v1.0.4'` installs a given
release.

- **`hive update`** downloads the next release, checks its checksum *and* its
  signature (`checksums.txt.sig`, the release key built into hive), makes sure
  it runs, and swaps it in. A running `hive.exe` cannot be overwritten on
  Windows but can be renamed, so the old binary moves to `hive.exe.old` first;
  then running daemons restart on the new one, and agents keep running.
- **`hive update --rollback`** puts the previous binary back (and keeps the
  newer one as `hive.exe.old`, so it can be undone). Rollback works the same on
  macOS and Linux.
- **Removing it:** run the installer with `-Uninstall`. It stops the daemon,
  removes the login task, `hive.exe` and the `PATH` entry. Your data stays
  unless you add `-RemoveData`.
- **Starting the daemon at logon:** `hive daemon install` registers a hidden
  Task Scheduler task, **Hive Daemon**. It runs as you at logon with your
  logon environment, restarts the daemon if it fails, and never starts a
  second one. `hive daemon uninstall` removes the task.

### Where things are

| | Windows | macOS and Linux |
|---|---|---|
| Data, sockets, logs (`HIVE_HOME`) | `%LOCALAPPDATA%\Hive` | `~/.hive` |
| Configuration (`HIVE_CONFIG`) | `%APPDATA%\Hive\config.toml` | `~/.config/hive/config.toml` |
| Agent manifests | `%APPDATA%\Hive\agent-detection` | `~/.config/hive/agent-detection` |
| Binary | `%LOCALAPPDATA%\Programs\Hive\hive.exe` | `~/.local/bin/hive` (or the package manager's) |

`HIVE_HOME`, `HIVE_CONFIG` and `XDG_CONFIG_HOME` override these as everywhere.
Data is machine-local (not roaming): sockets, logs and agents' state belong to
the machine they run on.

### How Windows differs

- **Terminals are pseudo consoles (ConPTY).**
  - Programs see `TERM=xterm-256color`, and Hive reads their output as VT
    sequences like any terminal's.
  - Input, resizing and Unicode in both directions are supported.
  - ConPTY repaints the screen itself, so a program's output can arrive as
    redraws rather than as the bytes it wrote. That makes no difference on
    screen, but output logs can show cursor moves.
- **Process trees are job objects.**
  - Each agent, pane process and plugin command runs in its own job.
  - Stopping it stops everything it started, and when its owner (the agent's
    shim, or the daemon for plugins) ends, the job ends with it, so nothing is
    orphaned.
  - Windows has no SIGTERM. A terminal agent is asked to stop by closing its
    pseudo console, which sends its programs `CTRL_CLOSE_EVENT`. Whatever is
    left after the grace period (`process.stop_grace`) is ended.
  - A program that is not in a terminal gets no polite request; it is ended
    after the grace period.
- **Detached processes.** The daemon and shims start without a console, in
  their own process group, outside the job of the terminal that started them.
  Closing that terminal therefore does not stop them. If that job forbids
  leaving it, they start inside it: closing *that* terminal then ends them.
  Use `hive daemon install` to be independent of terminals.
- **Sockets.**
  - The daemon and shims listen on AF_UNIX sockets (Windows 10 1803 and later)
    in your profile, whose ACL already keeps other users out.
  - Every connection is also checked: the peer process's user (SID) must be
    yours.
- **The foreground program.** ConPTY has no foreground process group. The
  program Hive treats as running in a shell pane is the shell's newest live
  descendant, which is what agent detection and `hive pane` read.
- **Working directories.** Windows keeps another process's working directory
  in that process's memory, which Hive does not read. A new pane starts in its
  environment's directory, not where you last `cd`-ed in the neighbouring
  pane.
- **The default shell** of a new pane is `$SHELL` when set (Git Bash, MSYS2),
  else PowerShell 7 (`pwsh`), else Windows PowerShell, else `%ComSpec%`
  (`cmd.exe`). `terminal.shell` in the config overrides it.
- **Commands and quoting.** Programs are found on `PATH` with `PATHEXT`, as a
  shell finds them. `.cmd` and `.bat` files run through `cmd.exe`. Arguments are
  quoted by Windows' rules (`CommandLineToArgvW`), so pass them as separate
  arguments; do not pre-quote them.

### Agents and shells

| | Expectation on Windows |
|---|---|
| **Claude Code, Codex, Gemini CLI, opencode, Copilot CLI** | Run natively (Node.js) when started by Hive (`hive agent start --kind …`). Hive pins their manifest, so state detection and resume work as elsewhere. |
| **An agent typed into a shell pane** | Recognised by its program name (`claude.exe`, `claude.cmd` and `claude` are all `claude`). npm's `.cmd` shims run `node …\cli.js`, which names no agent: start agents with `hive agent start`, or add a manifest whose `detect.args_regex` matches. |
| **Aider** | Runs where Python and Aider are installed. |
| **PowerShell 7, Windows PowerShell, cmd.exe** | Supported shells for panes. |
| **Git Bash, MSYS2, Cygwin** | Work as pane shells (set `terminal.shell`); paths inside are their own. |
| **WSL** | A pane can run `wsl.exe`. Agents inside it are Linux processes Hive sees only as `wsl.exe`; for agents in WSL, install Hive inside WSL instead. |
| **Terminals for `hive ui`** | Windows Terminal, and other VT-capable consoles (the classic console host on Windows 10 1809 and later) |

### Limitations (Windows-specific, tracked apart from shared defects)

Shell-script parts of Hive do not run on Windows:

- the reference plugins (`pr-status`, `spawn-reviewer`) and other plugins whose
  commands are `sh` scripts;
- `install.sh`;
- the shell snippets that `hive --remote` runs on a remote host.

Plugins that do not list `windows` in `platforms` are refused at install with
"runs on darwin, linux, not windows". Plugins written for Node.js (the official
ones) or as `.exe` programs work.

**Strict plugin isolation is not available.** Windows has no sandbox tool Hive
uses: with `[plugins] isolation = "auto"` (the default) plugins run standard,
and with `"strict"` none run (see
[the plugin API](plugins/api.md#isolation-and-the-security-boundary)).

**A Windows machine cannot be a remote host** (`hive --remote`, `hive machine
add`). The remote side runs POSIX shell scripts through `ssh`. Windows works as
the *client*: OpenSSH's `ssh.exe` (built into Windows 10 and later) reaches
macOS and Linux hosts.

**The UI's clipboard and image paste.** Text copy works through the terminal
(OSC 52). Copying to the system clipboard and image paste (`prefix I`) read and
write it with Unix tools and are not wired to the Windows clipboard yet.

**No `hive ui` notifications through the desktop.** Terminal notifications
(OSC 9) work in Windows Terminal; Windows toast notifications are not used.

**Process start times and `hive gc`.** Restored sessions recognise their
processes by PID and start time, as elsewhere. Boot time comes from the uptime
counter.

## Troubleshooting (Windows)

| Symptom | Cause, and what to do |
|---|---|
| `create pseudo console (Windows 10 1809 or later)` | ConPTY is missing: Windows is older than 1809. |
| `AF_UNIX listen` errors | Windows older than 1803, or a socket path over 108 characters. Set a shorter `HIVE_HOME`; long paths fall back to `%TEMP%\hive`. |
| The daemon stops when you close the terminal | That terminal runs its programs in a job that forbids breaking away. Run `hive daemon install` so Task Scheduler starts the daemon. |
| `the program in this pane does not declare image input`, clipboard copy does nothing | See the limitations above. |
| Plugin refused with `not windows` | Its author has not listed `windows`; it probably runs shell scripts. |
| `hive update` says it cannot move `hive.exe` aside | Another program has `hive.exe` open with exclusive access (an antivirus scan). Retry; `hive.exe.old` from an earlier update is removed on the next one. |
| Agents' text looks garbled in an old console | Use Windows Terminal; the classic console before 1809 does not process VT output. |
