# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in Hive, please report it privately through [GitHub Security Advisories](https://github.com/admirable-oss/hive/security/advisories/new).

**Please do not open a public issue for security vulnerabilities.** Private reports give us time to investigate, reproduce, and address the issue before details are disclosed publicly.

We acknowledge reports within three working days, agree on a fix and a disclosure date with you, and credit you in the advisory unless you prefer otherwise. We aim to release a fix within 30 days of a confirmed report, sooner for issues under active exploitation.

When reporting a vulnerability, please include enough information for us to reproduce the issue, including relevant versions (`hive version --json`), configuration, reproduction steps, and any potential impact.

## Supported versions

| Version | Supported |
|---|---|
| Latest 1.x minor release | ✅ fixes and security fixes |
| The 1.x minor release before it | ✅ security fixes |
| Older releases, pre-1.0 releases | ❌ upgrade with `hive update` |

Release candidates (`v1.0.0-rc.N`, the preview channel) get fixes by the next candidate.

## How Hive protects you

Hive runs coding agents as you, with your permissions. It does not sandbox them: an agent can do whatever you can. What Hive does is make sure nobody else can drive your agents, and that what it installs is what was released.

**The daemon and its shims accept only you.**

- Every socket lives in a directory only you can enter (0700) and is itself 0600.
- Each connection's peer is checked by the kernel's credentials: `SO_PEERCRED` on Linux, `LOCAL_PEERCRED` (`getpeereid`) on macOS. A connection from another user is refused and logged, even if a socket path were reachable some other way.
- The daemon listens on no network port. The exception is local discovery, when you turn it on (`discovery.advertise`): then the daemon answers mDNS questions on UDP 5353 with an announcement, and does nothing else on that port.

**Remote machines are reached only through your own ssh.**

- `hive --remote` and saved machines run `ssh <host> hive proxy`, so authentication, host keys, `~/.ssh/config` and agent forwarding are ssh's, as you set them up.
- The remote daemon is as private as the local one.
- Discovering a machine grants nothing. An announcement has a name, an identity ID, the ssh port and the API level. It has no user names, sessions, agents or credentials. Pairing connects over ssh, and has the machine sign a fresh challenge with its identity key (ed25519, kept 0600 in `identity.json`). Pairing refuses a machine that cannot prove the identity it announced, and saves a machine only after you confirm. A paired machine that later proves another identity is not used until you accept the new one (`hive machine trust`). An announcement never moves a paired machine to a new address unless the machine proves its identity there. See [docs/discovery.md](docs/discovery.md).

**Observers read, and only what their credential allows.**

- The observer socket (`observer.sock`) offers only read-only methods, after authentication with an observer token. There is no method there that types into a terminal, changes a layout or starts anything.
- A credential is scoped to the whole session or to listed environments and panes, may or may not read screens, and always expires. Revoking it ends its connections at once. The daemon stores only a hash of each token; tokens are never logged, and every credential made, connection, refusal and out-of-scope request is written to `observers/audit.log`.
- What observers receive leaves out environment variables, command lines, working directories, PIDs and agents' session IDs. Screens, when allowed, show whatever is on them, secrets included.
- This is access control in Hive's API, not isolation by the operating system: the observer socket is as private as the others (only you can connect), and a program running as you could still open the control socket. To hold an observer to its token, run it somewhere else and let it in only through an ssh key restricted to the observer socket (`command="hive proxy --observer",restrict` in `authorized_keys`). See [docs/observers.md](docs/observers.md).

**The web observer shows, and only what you share.**

- `hive web serve` is a process of its own that reaches daemons only through their observer sockets, with credentials you make for it (`hive web runtime add`); it has no way to type, change layouts or start anything, and neither do its pages.
- Browsers sign in with one-time links whose code travels in the URL fragment (never sent to a server or logged) and is exchanged for an HttpOnly, SameSite=Strict cookie. Links and access expire; `hive web revoke` signs browsers out at once. Only hashes of codes and cookies are stored.
- It refuses to serve beyond this machine without TLS (its own, or a proxy's). Pages load nothing from elsewhere (Content-Security-Policy), are never cached, cannot be framed, and keep nothing in the browser but the theme.
- Screens and output history are shown only when both the runtime and the link allow them; history is read on request and audited. The multiplexer shows when someone observes, and which screens are watched. See [docs/web.md](docs/web.md).
- Notifications (Web Push) go from the web observer straight to the browser's push service, signed with its own key and encrypted for the device, so the push service cannot read them; no Hive service relays them. They name an agent at most (or nothing, if you choose), never screen contents, only for agents the link can see, and stop when the link is revoked or expires. The installed app caches only its own code, never pages or screens. See [docs/mobile.md](docs/mobile.md).

**Installing hive elsewhere, plugins and updates need your yes, and updates are verified.**

- `hive update` downloads the release, checks the archive against `checksums.txt`, and checks `checksums.txt` against its ed25519 signature (`checksums.txt.sig`) with the release key built into your binary. An unsigned or wrongly signed release is not installed. Builds from source carry no release key and check only the checksum (they say so).
- `hive --remote` installs or updates hive on another machine only after a yes/no question (or `--yes`).
- `hive plugin install` shows everything a plugin will run, and when, before asking. The commit installed is recorded. Plugins run as you and are not sandboxed: install only plugins you trust, from a tag you have read.
- Agent detection manifests are built into the binary or read from your own `~/.config/hive/agent-detection`. Hive downloads none.

**What agents print is kept private.**

- Output logs, snapshots and pane history are written 0600 in 0700 directories.
- Pane history replay (`session.replay_history`), which keeps screen text, is off by default. Turning it off deletes what it kept.
- Metadata tokens are kept in memory only.
- Programs in panes may set your clipboard (OSC 52), as in most terminals, but never read it: Hive drops clipboard queries.

**Agents start clean.**

- Panes do not inherit the variables of the terminal or the coding agent that started the daemon (`TMUX`, `CLAUDECODE`, `CODEX_SANDBOX*`, `HIVE_*`, …).
- Hive sets only its own variables in them.

**The code is checked.**

- CI runs `gosec` (with the other linters), the race detector, goroutine-leak checks, and fuzzers on every parser of untrusted input: the wire protocol, frames, configuration, and agent and plugin manifests.
- A nightly run fuzzes for 25 hours in all, and runs a chaos soak (daemon and shims killed, the network cut) to check that faults never lose or corrupt state.

## Release signing

Releases are built by the release workflow, which signs `checksums.txt` with the project's ed25519 key (`tools/release-sign`). To verify a download by hand:

```sh
go run ./tools/release-sign verify <public key> checksums.txt
```

The public key is published with each release and built into every release binary (`buildinfo.releaseKey`).
