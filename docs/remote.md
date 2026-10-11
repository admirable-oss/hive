# Remote machines

Hive runs agents on other machines as it does on this one. The remote daemon
does the work (agents, shims, persistence), and you drive it from here: the
CLI, the multiplexer, scripts. Your system's `ssh` carries everything, so your
`~/.ssh/config`, keys and agent apply, and no port is opened anywhere.

```sh
hive --remote me@gpu-box agent list              # any command, on that host
hive --remote me@gpu-box                         # the multiplexer, showing that host
hive machine add gpu me@gpu-box                  # save it under a name
hive --machine gpu agent start --kind claude --env api
hive machine status                              # every saved machine at once
```

## How it connects

`hive --remote <host>` runs `ssh <host> hive proxy`. On the remote machine,
`hive proxy` starts the daemon if it is not running and bridges ssh's input
and output to the daemon's socket. Each command then talks to the remote
daemon exactly as it would to a local one.

- Commands that use the daemon (`agent`, `pane`, `tab`, `env`, `ps`,
  `terminal`, `layout`, `worktree`, `events`, `api`, `mcp`, the multiplexer)
  run here and talk to the remote daemon. Screens are drawn on this machine.
- Commands about a machine itself (`daemon …`, `status`, `stop`, `config …`,
  `integration …`, `session …`, `gc`, `update`) run on the remote machine as a
  whole (`ssh <host> hive <command>`).
- `machine …`, `version` and `completion` stay here.

Paths in arguments (`--cwd`, `--repo`) are paths on the remote machine.

Connections to a host share one SSH connection (ControlMaster), kept for 10
minutes after the last use. The control sockets live in `~/.hive/ssh/`. A
dead link is noticed within about 45 seconds (ServerAliveInterval 15,
ServerAliveCountMax 3). `hive machine reconnect <name>` drops a stuck
connection and makes a new one.

## Installing and updating hive there

The first time you reach a machine without hive, or with an older one, Hive
asks before changing anything:

```
hive: hive is not installed on gpu-box; copy this hive (v0.9.0) there? [y/N]
```

When the remote system matches this one (OS and architecture), this very
binary is copied to `~/.local/bin/hive` there, or over the hive already
installed. Otherwise the release installer runs there. An older daemon there
is restarted on the new binary, and its agents keep running (they run under
shims). `--yes` answers the question for scripts. Without a terminal and
without `--yes`, nothing is installed. A non-standard install location is set
per machine with `hive machine add … --hive-path`.

## Saved machines

```sh
hive machine add <name> <host> [--session s] [--hive-path p] [--no-check]
hive machine list
hive machine rename <name> <new-name>
hive machine disable <name>     # keep it, but do not connect (the UI skips it)
hive machine enable <name>
hive machine remove <name>      # forget it; its daemon and agents keep running
hive machine status [name…]     # reachable? round trip, version, agents
hive machine reconnect <name>
```

Machines are kept in `~/.hive/machines.json`, shared by every local session.
`--machine <name>` works with every command, like `--remote`.

## In the multiplexer

The sidebar has a **Machines** panel when any machine is saved and enabled.
It shows the machine on screen (▣), then every other machine with its agents
and their states (◆ blocked, ● working, ✓ done), or ✗ when it cannot be
reached. Picking a machine or one of its agents shows that machine's
workspace, at that agent. The machine you came from joins the list, so
picking it brings you back. The window title names the machine shown.

## Bandwidth

A busy agent repaints its screen many times a second. Over a slow link,
Hive keeps the stream small:

- **Compression.** Remote clients get the screen stream deflate-compressed;
  each frame is flushed whole, and later frames compress against earlier
  ones.
- **Pacing by round trip.** Every 10 seconds the client measures its round
  trip and reports it. The daemon sends that client at most one frame per
  round trip (between 8 ms and 250 ms). Changes meanwhile merge into the
  next frame, so nothing is lost; it just arrives in fewer, larger steps.
- **A budget.** `remote.frame_budget_kb` (48 KiB/s by default) caps what one
  client is sent across all the panes it shows.

A busy Claude Code-like pane costs about 14 MB an hour this way (the target
is under 50 MB/h). `cmd/hive/e2e_m7_test.go` measures it.

## Clipboard

When a program in a pane copies text (OSC 52, as Neovim, tmux and many CLIs
do), the copy reaches the clipboard of the machine you are sitting at, also
when the agent runs on another machine. The multiplexer sends it through
your terminal (OSC 52) and, when you are not on SSH yourself, through the
system clipboard tool, as `ui.clipboard` says. Programs cannot read your
clipboard: Hive never answers OSC 52 queries.

## Settings

```toml
[remote]
ssh = "ssh"              # the ssh command line; HIVE_SSH overrides it
frame_budget_kb = 48     # screen updates a remote daemon sends this client, KiB/s (0: no limit)
```
