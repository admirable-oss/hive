// Package shim keeps agents alive independently of the daemon.
//
// Every agent runs under its own shim: a small `hive __shim` process started
// in a new session, detached from the daemon. The shim owns the agent's PTY,
// its terminal emulator and its logs, and serves them on a unix socket using
// protocol 2. The daemon is just a client: when it stops, crashes or is
// upgraded, the shims and their agents keep running, and the next daemon
// adopts them by connecting to their sockets again (see docs/adr/0006).
//
// Layout, one directory per agent under the daemon's run directory:
//
//	run/<id>/spec.json    what to run (written by the daemon)
//	run/<id>/state.json   running / exited, PIDs, exit status (written by the shim)
//	run/<id>/shim.sock    the shim's socket (0600)
//	run/<id>/shim.log     the shim's own log
//	run/<id>/shim.stderr  the shim's stderr (crashes before logging starts)
//
// The daemon side is Launcher (start and adopt shims) and Remote (one shim's
// agent, usable as a terminal.Session). The shim side is Main.
package shim
