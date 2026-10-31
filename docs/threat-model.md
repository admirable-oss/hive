# Threat model

What Hive protects, from whom, where its trust boundaries are, and what it
does not try to do. [SECURITY.md](../SECURITY.md) describes the protections
and how to report a vulnerability. [docs/security-review.md](security-review.md)
records the last review against this model.

## What Hive is

Hive runs coding agents for one user, as that user. A daemon owns the
workspace. A shim per agent owns its terminal and outlives the daemon.
Clients (the CLI, the TUI, MCP, the desktop app, `pkg/hiveapi` programs)
drive the daemon over a local socket. Observers read through a separate
socket. Members, people the user lets in (M27), reach a third socket through
ssh. Other machines are reached through the user's ssh.

## Assets

| Asset | Why it matters |
|---|---|
| **Control of agents** (typing into them, starting processes, changing layouts) | Equal to running commands as the user |
| **Terminal contents and output logs** | Agents print source code, tokens, keys and customer data |
| **The release signing key and the release pipeline** | Whoever controls them can ship code that every `hive update` installs |
| **This runtime's identity key** (`identity.json`) | Lets a machine pose as this runtime to machines that paired with it |
| **Observer tokens, web sign-in links and cookies** | Read access to screens and agent activity |
| **Members and Hive's block in `authorized_keys`** | Decide who else reaches the runtime, and with which role |
| **Plugin releases and the registry** | Plugins run as the user |
| **Saved machines and their pinned identities** | Decide where ssh connections go and which runtime is trusted |

## Who we defend against

| Adversary | In scope |
|---|---|
| **Another local user** on a shared machine | Yes. They cannot connect to the sockets or read state. |
| **Someone on the local network** | Yes. Discovery grants nothing; announcements carry no secrets; a spoofed announcement cannot redirect a paired machine. |
| **A network attacker between you and GitHub or the registry** | Yes. Releases are signed; plugin releases are pinned by commit and checksum; registry links must be HTTPS. |
| **A compromised registry listing or plugin maintainer** | Partly. Releases are immutable and pinned, permission changes are shown before an update, and releases can be revoked. From 1.7, schema 2 releases carry their publisher's SSH signature: a stolen repository token alone cannot publish an official or verified release, an update cannot change publisher, and a stolen signing key is revoked from the time it was distrusted. A malicious release you choose to install runs as you. |
| **An observer (a dashboard, a person with a share link)** | Yes. It reads only what its credential allows and cannot control anything. |
| **A member (a teammate let in with `hive access`)** | Yes. Their key reaches only the member socket, the daemon decides who they are from the key's forced command, and they cannot manage access. Until roles are enforced (RFC 0007, step 2) a member can do nothing but see who they are. An operator's prompts run as you, through your agents. |
| **A malicious website open in your browser** | Yes, for the web observer: CSP, SameSite=Strict cookies, no framing, codes in URL fragments. |
| **The agents themselves** | No. Agents run as you, unsandboxed, by design. See non-goals. |
| **Malware already running as you** | No. It can read your files and open your sockets, as it could without Hive. |
| **A compromised ssh host or a key you trust** | No. Hive relies on ssh for remote authentication. |

## Trust boundaries

1. **The local control socket.** Only the user may connect: 0600 sockets
   in 0700 directories, and every connection's peer is checked by kernel
   credentials (`SO_PEERCRED`, `LOCAL_PEERCRED`, the peer SID on Windows).
   Everything past this boundary has the user's authority.
2. **The observer socket.** Same file permissions. Past it, a token decides
   what can be read, and no method can change anything. This is access
   control in Hive's API, not operating-system isolation: a program running
   as the user can open the control socket instead. To hold an observer to
   its token, run it as someone else and let it in through a restricted ssh
   key.
3. **The member socket.** Same file permissions. A member's key in
   `authorized_keys`, inside Hive's block, is forced to run
   `hive proxy --member <id>` with every forwarding and the terminal off;
   the proxy binds the connection to that member before relaying, and
   serves only clients asking for `hive proxy`. The daemon re-checks the
   member before every request, and revocation ends its connections at
   once. As for observers, this is access control in the API: a program
   running as the user can claim any member, and the boundary holds only
   when the restricted key is the member's only way into the account.
   Inviting a key that already logs in unrestricted is refused.
4. **ssh.** Remote machines run `hive proxy` through the user's ssh;
   authentication, host keys and forwarding are ssh's. The remote daemon
   applies boundary 1 to that connection. The daemon itself goes through
   this boundary only for a plan's tasks placed on a saved machine
   (`task.run` there). It never goes to machines that are disabled or whose
   identity changed, and it uses the same ssh command and connection
   sharing as `--machine`.
5. **Discovery and pairing.** An mDNS announcement is a hint. Pairing proves
   the machine holds the identity key (a signed challenge over ssh) and
   needs the user's confirmation. A changed identity is held until accepted.
6. **The web observer.** It holds observer credentials, not control access.
   Browsers authenticate with one-time codes exchanged for cookies, and it
   refuses to serve beyond localhost without TLS.
7. **The desktop webview.** The page reaches Go only through typed methods
   that validate input; it loads only embedded code (CSP), and terminal
   images are re-encoded in Go before it sees them.
8. **Releases and updates.** `checksums.txt` is signed with ed25519 in CI,
   as a raw signature for `hive update` and in OpenSSH's format for the
   installers. `hive update` checks the signature with the key built into
   the running binary, then the archive's checksum, then that the new binary
   runs. Every archive has an SBOM and a build provenance attestation, and
   builds are reproducible.
9. **Plugins.** Installed only after a preview and a yes. A registry release
   is pinned to a commit and an archive checksum and checked against the
   manifest. Plugin API 1 enforces declared capabilities on the plugin's
   socket (Hive API calls). Filesystem, network and process access are
   enforced only under strict isolation (`sandbox-exec` on macOS, `bwrap` on
   Linux).

## Non-goals and limits

- **Agents are not sandboxed.** An agent can do anything you can. Hive keeps
  others from driving your agents; it does not keep your agents from your
  files. Use a dedicated account or a VM for agents you do not trust.
- **Plugin API 2 plugins are confined to what you approved, on macOS and
  Linux.** From 1.7 an API 2 plugin runs sandboxed by default: it reads
  only its own files and the system's, writes only its own directories,
  reaches only the hosts it lists (through a proxy that sees host names,
  not content), runs only the programs it lists, and reaches only its own
  Hive socket, unless you grant more. Reads are wider than writes: the
  system's files and its package are readable. It runs unconfined only
  when you approve `unconfined`, per release (`isolation = "none"`, or a
  system with no sandbox such as Windows).
- **Plugin views are data.** A view document is checked by the daemon and
  drawn by clients as text: no markup, scripts or images. A link must be
  `https:` and opens only on a person's click; a button runs only one of
  the plugin's own declared actions, and plugins cannot click. A plugin's
  workflow step gets only the inputs the workflow gives it, and its
  outputs are checked against their declared types; a plugin fires only
  its own triggers, and shared workflows still run only once trusted.
- **Plugin forges and trackers see what they serve.** A forge gets the
  pull request it opens (branch, title, body, the worktree's path); a
  tracker gets the issue operation asked of it. Both need `network` for
  their hosts. A plugin cannot push, open a pull request or close an
  issue itself.
- **Plugin API 1 plugins run as you.** Their capabilities limit what they
  may ask *Hive* to do. They are not isolation unless strict isolation is
  on and available, and it is not available on Windows. A plugin without
  `api` (API 0) holds every capability. Install plugins you trust, from a
  release you have read.
- **Screens are shown as they are.** An observer allowed to read screens sees
  whatever is on them, secrets included. There is no redaction of terminal
  contents.
- **Environment variables are stored in plain text** in process and
  environment records (`HIVE_HOME`, private to you), because Hive needs them
  to restart agents. Do not put long-lived secrets in `hive env create -e`;
  let agents read them from your keychain or credential helpers.
- **The installer script itself is trusted on first use.** From v1.1.0,
  `install.sh` and `install.ps1` verify the release's signature with the
  key embedded in them, but the script is fetched from GitHub over TLS, so
  a GitHub compromise could serve a different script with a different key.
  Read the script before running it, or verify a download by hand
  ([docs/releasing.md](releasing.md#verifying-a-release)).
  `HIVE_ALLOW_UNVERIFIED=1` falls back to the checksum where `ssh-keygen`
  is missing.
- **Builds from source carry no release key**, so their `hive update` checks
  only checksums, and says so.
- **The desktop app is a preview**: its packages are not signed yet and it
  has no verified in-app update ([docs/desktop.md](desktop.md)).

## Assumptions

- The user's account, ssh keys and machines are not compromised.
- The operating system enforces file permissions and reports peer
  credentials correctly.
- The release signing key is held only by the release workflow (a GitHub
  Actions secret) and the maintainers' offline copy.
- GitHub serves the repository and releases. A GitHub compromise is
  mitigated for updates by the signature, not for first installs.
