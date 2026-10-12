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
- No Hive process listens on a network port.

**Remote machines are reached only through your own ssh.**

- `hive --remote` and saved machines run `ssh <host> hive proxy`, so authentication, host keys, `~/.ssh/config` and agent forwarding are ssh's, as you set them up.
- The remote daemon is as private as the local one.

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
