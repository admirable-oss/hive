# ADR 0008: Remote machines over the system's ssh and `hive proxy`

- Status: Accepted
- Date: 2026-10-11

## Context

M7 lets the CLI, the multiplexer and scripts drive Hive on other machines.
Something has to carry the protocol between machines, authenticate the
user, and get hive onto machines that do not have it.

## Decision

1. **The system's `ssh` is the transport.** `client.Dial` runs
   `ssh <host> hive proxy`. On the remote side, `hive proxy` starts the
   daemon when needed and bridges stdin and stdout to its unix socket. The
   client's end is one side of a socket pair whose other side is ssh's stdin
   and stdout, so it is a real `net.Conn` with deadlines. Protocol 2 runs over
   it unchanged.
2. **No `golang.org/x/crypto/ssh`.** The system's ssh already knows the
   user's `~/.ssh/config` (aliases, ProxyJump, ports), keys, agents,
   hardware tokens, known hosts and Kerberos. Reimplementing that would cost
   a dependency and still miss cases. Connection sharing (ControlMaster,
   sockets in `<HIVE_HOME>/ssh`) makes repeated commands cost a channel, not
   a handshake. Keepalives (15 s × 3) notice a dead link.
3. **The remote daemon is never exposed on a port.** Only the user who can
   ssh in can reach it, through their own account, as with the local socket.
4. **Commands either dial or run there.** Commands that use the daemon dial
   it and run here (the UI draws locally, so typing is local). Commands about
   a machine itself (daemon management, configuration, integrations, update)
   run on it whole, as `ssh <host> hive <command>`.
5. **Install with consent.** A missing or older remote hive is replaced
   after a yes/no question (`--yes` in scripts). The question offers this
   binary when OS and architecture match, else the release installer. The
   swap is atomic, so shims there keep their binary, and the daemon restarts
   on the new one with agents kept.
6. **Bandwidth is the server's job, steered by the client.** A remote client
   offers `frames/deflate` and reports its round trip and byte budget
   (`client.report`). The daemon paces frames per client, and merges what
   changed meanwhile (`vt.Coalescer` and the PTY's own coalescing), so a
   slow link never makes a screen wrong, only less frequent.

## Costs

- The remote side needs a POSIX shell and `uname`. Every supported OS has
  them.
- One ssh process per connection on the client. With connection sharing
  that is cheap, and the multiplexer keeps one connection per machine.
- Without an ssh binary (Windows without OpenSSH) there is no remote
  support. Windows is post-1.0.
