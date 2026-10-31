# RFC 0007: Team collaboration, over ssh

- **Status:** Draft
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`access.*`, `presence.*`, `pane.claim`, `pane.release`, `agent.handover`, `task.assign`, a member socket), CLI (`hive access`, `hive proxy --member`), Go SDK, config (`[access]`), on-disk state (`access/`, agent and task records), events, the TUI, desktop app and web observer, the threat model

## Summary

M27 lets a teammate work in your runtime, with a role you chose, and
without a hosted service:

- **Members.** `hive access invite` lets a person in through ssh, with a
  key that reaches only a new **member socket**. The daemon knows who
  every member connection belongs to, and nothing the client sends can
  change that.
- **Roles per environment.** `observer` reads, `operator` prompts agents
  and approves tasks but starts nothing, and `owner` runs the environment
  as you would. The daemon checks the role on every request.
- **Shared workspaces.** Everyone in an environment sees who else is there
  and where they are looking. A pane takes input from one person at a
  time, and everyone sees who holds it. Agents and tasks can be handed
  over to someone else.
- **Audit.** Every action a member takes is recorded: who, what, when and
  from where.
- **Notifications for teams.** Blocked and done go to whoever owns the
  agent or task, with a fallback when they are away.
- **Revocation** ends a member's connections at once.

## Motivation

Hive runs agents for one user, as that user. Others can watch (observers,
M14 and M15), but nobody else can help. Teams that share a build box or a
cloud VM today do one of these:

- **Share the account.** Everyone logs in as the agents' user and has full
  control. Nobody knows who typed what, and two people typing into one
  agent garble its prompt.
- **Share the screen.** One person drives while the others watch a video
  call. The reviewer who should approve a plan has to ask the driver to
  press the key.
- **Run separate runtimes.** Work is split by machine, not by task. Handing
  an agent to a colleague means stopping it and starting again over there.

The pieces are already in place. Observer credentials prove a scope can be
enforced by the daemon. The restricted ssh key
(`command="hive proxy --observer",restrict`) proves that ssh can hold a
remote party to one socket. The plugin capability table
(`internal/plugin/plugin.api.go`) already sorts every daemon method by what
it can do. What is missing is a person with an identity on the control
API, a role per environment, someone who holds a pane's input, and an
owner for agents and tasks.

## Proposal

### Members

A **member** is a person you let into this runtime. A member has a name,
an ssh public key, a role per environment and an expiry.

```sh
hive access invite alice --key ~/alice.pub --role operator --env web
hive access invite bob --key bob.pub --role observer --all --expires 720h
hive access grant alice --env api --role observer
hive access list                   # members, roles, expiry, last seen, connections
hive access show alice
hive access revoke alice           # her connections end at once
hive access audit [--member alice] [--since 24h]
```

`hive access invite` adds one line to `~/.ssh/authorized_keys`, inside a
block that Hive manages:

```
# >>> hive access (managed by hive; do not edit) >>>
command="hive proxy --member m_7f3k2",restrict ssh-ed25519 AAAA… alice
# <<< hive access <<<
```

It shows the line and asks first. `--print` prints the line and leaves the
file alone, for hosts whose keys come from configuration management.
`hive access revoke` removes the line. `hive access doctor` reports
members whose line is missing or changed, and lines with no member.

The teammate needs nothing new. They add your machine as usual
(`hive machine add buildbox you@buildbox`, or `--remote`). Whatever their
side asks ssh to run, the key gets `hive proxy --member m_7f3k2`. The proxy
connects to `member.sock` and binds the connection to that member before
it relays anything. It also passes on where the connection comes from
(`SSH_CONNECTION`). The client never sees or chooses that binding. Their
TUI, CLI, MCP server and desktop app work unchanged. What their role does
not allow answers `permission_denied`, and the welcome lists only the
methods they may call.

- **Identity is the key.** ssh proves who the member is, and the forced
  command in `authorized_keys`, which only you can write, says which
  member that key is. There is no bearer token to leak.
- **`restrict`** turns off port, agent and X11 forwarding and the pty, so
  the key cannot reach `hive.sock` or a shell.
- **Expiry.** Every member expires. The default is 90 days, and the range
  is 1 hour to 365 days. `hive access renew alice --expires 2160h` extends
  it. An expired member's connections end at expiry.
- **Storage.** `access/members.json` (mode 0600, next to `observers/`)
  holds members, their keys' fingerprints, grants, expiry and state.
  Revoked and expired members stay listed for 30 days.
- **Managing access** (`access.invite|grant|revoke|renew`) needs the
  control socket, that is you. It is closed to members of every role and
  to plugins.

### Roles

A grant gives a member one role in a scope: the whole runtime (`--all`)
or the listed environments. Scopes work as for observers: an environment
brings its tabs, panes, agents, processes, tasks and branches. When two
grants cover an environment, the higher role applies. You, on the control
socket, are always the runtime's owner.

| | observer | operator | owner |
|---|---|---|---|
| See environments, panes, agents, tasks, events in scope | ✓ | ✓ | ✓ |
| Read screens and output | ✓ | ✓ | ✓ |
| Prompt agents, send keys and input to **agent** panes | | ✓ | ✓ |
| Approve or reject tasks and plans; answer workflow approvals | | ✓ | ✓ |
| Comment on branches and issues | | ✓ | ✓ |
| Take a pane's input, hand over agents and tasks | | ✓ | ✓ |
| Input to **shell** panes (no agent) | | | ✓ |
| Start, stop and restart agents, processes, tasks, plans and workflows | | | ✓ |
| Split, close, move panes; create tabs (each starts a process) | | | ✓ |
| Push branches, open and merge pull requests | | | ✓ |
| Environment variables, command lines, working directories | | | ✓ |
| Create or delete environments | | | `--all` only |
| Config, plugins, updates, observers, members, the daemon | | | |

The last row stays with you. A member never manages access, never
installs plugins and never stops the daemon.

**Operators start no processes.** That rule covers more than
`process.start`. Splitting a pane starts a shell, and typing into a shell
pane runs anything, so operators may type only into panes where Hive sees
an agent. A pane whose agent exits stops taking an operator's input.

**Below owner, records are observer views.** Observers and operators
receive environments, processes and agents without environment
variables, command lines, working directories or PIDs, exactly as
observer credentials do. Screens are shown as they are.

**How it is enforced.** A table maps every control method to the lowest
role that may call it, the way the plugin capability table does today.
A method not in the table is refused, and a test fails when a new method
is left out. The member socket wraps the router as `scopedHandler` does
for plugins. It also checks the target's environment against the
member's grants on every request, so a grant changed or revoked applies to
the next request, not the next connection.

**What a role is not.** An operator cannot start processes through Hive,
but the agents they prompt run as you and can do anything you can. A role
limits what a teammate can ask Hive to do. It is not a sandbox for the
agents. Give operators runtimes whose agents you would let them direct.

### Shared workspaces

**Presence.** Each connection reports where it is looking:

- `presence.set {environment, tab, pane}` is sent by clients when focus
  moves;
- `presence.list` returns everyone connected: member (or `owner`),
  client, where they look, whether they hold input, and since when;
- a `presence.changed` event carries each change.

Observer credentials appear in presence as they do in `observer.activity`
today. The TUI shows initials on the borders of panes others are looking
at, and the tab bar lists who is in the environment. The desktop app and
web observer show the same.

**Focus is per person.** Today one focus is shared by every client
(`pane.focused`). For you it stays that way. A member's focus is their own
and is never written to the session, so Bob looking around never moves
Alice's or your view.

**One person types at a time.** Each pane has an **input holder**:

- Input from someone who does not hold the pane takes it, if it is free.
- While someone else holds it, input answers `conflict` with the holder's
  name, and nothing is written.
- The holder keeps it until they release it (`pane.release`), stop typing
  for `[access] input_idle` (10 seconds by default), or lose access.
  Leaving counts as no longer typing: a command is a connection of its
  own, so a claim made by one outlasts it.
- `pane.claim {pane, force}` takes it on purpose. `force` is for an owner
  in that environment, and the previous holder is told.
- An `agent.prompt` holds the pane for the whole write, so a prompt never
  mixes with someone else's keys.
- A `pane.floor` event carries every change, and the pane's border shows
  `✎ alice`.

The holder is a person, not a connection. Your own TUI, desktop app and
scripts never conflict with each other, so working alone is unchanged.

**Handing over.** Agents and tasks gain an `owner`: the member who started
them, or `owner` for you. Existing ones belong to you.

- `agent.handover {id, to}` gives an agent to another member, who must
  be an operator or owner in its environment. Its pane's input and its
  task go with it.
- `task.assign {id, task, to}` does the same for a task and its agent,
  and, without `task`, for the plan and its unfinished tasks.
- Operators may hand over only what they own. Owners may hand over
  anything in their environments.
- Both are audited and shown in the agent's and task's details.

`Approval.by` is filled by the daemon from the member's identity. A member
cannot approve under someone else's name. Your own approvals keep the
name your client sends, as today.

### Audit

`access/audit.log` (JSON lines, rotated at 4 MiB with four older files
kept) records every request a member makes that changes something, every
refusal, and the access changes:

```json
{"time":"2026-11-02T10:14:03Z","member":"m_7f3k2","name":"alice","role":"operator","from":"203.0.113.7:51122","client":"hive-tui/1.8.0","action":"agent.prompt","target":{"environment":"web","agent":"a_19"},"outcome":"ok","detail":{"bytes":142}}
```

- **Access:** invited, granted, renewed, revoked, expired; connected,
  disconnected and why.
- **Actions:** every call that is not a read, with its target and outcome
  (`ok`, `permission_denied`, `conflict`, an error code). Reads of screens
  are recorded once per pane per connection, not per frame.
- **Input contents are not recorded.** Prompts and keys are recorded by
  size. `[access] audit_input = true` records prompt text, for teams that
  need it. Terminal input never is, since it may hold passwords.
- `hive access audit` reads it. `access.audit` serves it on the control
  socket only.
- Events caused by a member carry an `actor`, so clients and workflows
  can tell who did what without reading the log.

What you do on your own control socket is not audited. It never was,
and you can always read the files anyway.

### Notifications for teams

Today every TUI decides for itself what to notify. M27 moves the choice of
who is notified into the daemon:

- When an agent becomes blocked or done, or a task needs approval or
  fails, the daemon publishes `notify {kind, agent|task, to}`.
- `to` (a list of people) is the agent's or task's owner, if they have a
  client connected and their grant still covers it.
- Otherwise it is the fallback: `[access] notify_fallback`, a list of
  members, `owner` (the default) or `operators` of that environment, and
  `fallback` is set. When the fallback names nobody who may see the work,
  it is the runtime's own user.
- Each client shows only the notifications addressed to the person it
  belongs to, using the `[notify]` settings it has today. A client of an
  older daemon keeps deciding for itself.

Web Push, for the web observer, keeps its own scope-based fan-out. Tying a
browser to a member is left to M29.

### Local-first

All of this works between two machines and ssh. Nothing calls out, and no
account exists anywhere but `access/members.json`. The hosted service
(M29) may later register runtimes and map an organization's people to
members. It will use these roles, not replace them, and a runtime keeps
working with it switched off.

### API

- **API level 17.** Only additions.
- **Control socket only (you):** `access.invite`, `access.list`,
  `access.get`, `access.grant`, `access.renew`, `access.revoke`,
  `access.audit`.
- **Everyone, members included:** `access.whoami` (identity, grants, the
  methods allowed), `presence.set`, `presence.list`, `pane.claim`,
  `pane.release`, `agent.handover`, `task.assign`, each subject to the
  role table.
- **Events:** `presence.changed`, `pane.floor`, `access.changed` (members
  and grants; to you only), `notify`. Every event gains an optional
  `actor`.
- **Fields:** `agent.owner`, `task.owner`, `plan.owner`.
- **Errors:** `conflict` for a pane someone else holds, naming the
  holder; `permission_denied` for a role.
- **Plugins:** `access.*` closed; `presence.list` needs `hive.read`;
  `pane.claim` and `pane.release` need `hive.input`; `agent.handover` and
  `task.assign` need `hive.process`. A plugin acts for you, never as a
  member.
- **Go SDK:** `hiveapi` gains `Access`, `Presence`, `Claim`, `Release`,
  `Handover`, `Assign`, and `Client.WhoAmI`.

### Configuration

```toml
[access]
enabled = true                   # member.sock; members are kept either way
input_idle = "10s"               # how long a silent holder keeps a pane
notify_fallback = ["owner"]      # member names, "owner", "operators"
audit_input = false              # record prompt text in the audit log
max_connections = 32             # across all members
```

## Compatibility

- **Wire API.** Only additions, at API level 17. An older client used by
  a member connects and works within the role; what is refused answers
  `permission_denied`, which every client already shows. It shows no
  presence and no holder, and a refused keystroke is reported as an error.
  A newer client with an older daemon gets `unknown_method` for the new
  methods and hides presence, holders and handover.
- **Solo use is unchanged.** Without members there is no member socket
  traffic, focus stays shared, and input never conflicts.
- **Focus.** `pane.focused` keeps its meaning for you. Members' focus is
  only in presence.
- **Notifications.** A client of a 1.8 daemon follows `notify` events; an
  older client keeps its own rules and may notify about agents it does not
  own, as today.
- **On-disk state.** `access/` is new; earlier releases ignore it, so a
  downgrade leaves members unable to connect (no member socket), which
  fails closed. Agent and task records gain `owner`, which earlier
  releases keep as unknown fields. The managed block in `authorized_keys`
  points to `hive proxy --member`, which earlier releases reject.
- **Plugins.** Nothing changes for existing plugins. New methods are
  classified, and `access.*` is closed.
- **Platforms.** Members need a macOS or Linux host, since Windows is not
  a remote host yet. Windows clients can be members of one.

## Security and privacy

The threat model changes in three places
([docs/threat-model.md](../docs/threat-model.md)):

- **What Hive is.** "Runs coding agents for one user" becomes "for one
  user, who may let others in with a role". Agents still run as that user.
- **A new adversary: a member.** In scope. A member is held to their role
  and scope by the daemon. They cannot manage access, widen a grant, reach
  `hive.sock`, start processes as an operator or observer, read records'
  secrets below owner, or approve as someone else.
- **A new trust boundary: the member socket.** Same file permissions as
  every Hive socket. The member is decided by the forced command in
  `authorized_keys`, which ssh authenticated. As with observers, this is
  access control in the API: a program running as you can open
  `member.sock` and claim any member, but it could open `hive.sock`
  instead. The boundary holds only if the member's key is the only way
  they reach the account. A teammate with a password, an unrestricted key
  or `sudo` to your account is you.

Limits to state plainly:

- **Operators direct agents that run as you.** A role is not a sandbox
  (see *Roles*).
- **Screens are shown as they are**, to every role, as with observers.
- **Revocation** removes the key's line and ends connections at once.
  Whatever an agent did on a member's prompt stays done.
- **The audit log** names members and the addresses they came from. It
  stays on the machine, is private to you, and holds no input unless
  `audit_input` is on.

## Testing

- **The role table:** every control method is classified, a new one fails
  the test until it is, and each role's allowed set is frozen in
  `api/v1/roles.txt`.
- **Enforcement:** for each role and scope, every method is called against
  targets in and out of scope. Out of scope answers `not_found`, as for
  observers. Below the role answers `permission_denied`. Grants changed
  and revoked mid-connection apply to the next request.
- **The input holder:** two writers race on one pane under `-race`; the
  bytes written never interleave between people, idle release and `force`
  work, and a prompt is never split. A fuzz test drives random claims,
  writes, releases and disconnects.
- **ssh end to end:** a test sshd (as the M7 tests use) with the managed
  `authorized_keys` block. The member's key reaches only the member
  socket, forwarding is refused, and a key with no member is refused.
- **Upgrade:** a 1.7 daemon's state opens unchanged, agents and tasks
  belong to the owner, and a downgrade refuses members.
- **Acceptance (`TestM27TeamRuntime`):** two people work in one runtime,
  one as operator. The operator prompts an agent and approves a task, and
  is refused `agent.start`, `process.start`, `pane.split` and input to a
  shell pane. Both type into one pane at once and the input never
  interleaves. Every one of the operator's actions is in the audit log.
  `hive access revoke` ends the operator's connection within a second, and
  the next connection is refused.

## Alternatives

- **Bearer tokens, as for observers.** Tokens leak into shells, chat and
  CI logs, and need a channel of their own to be handed out. A team already
  exchanges ssh keys, ssh already authenticates them, and the forced
  command ties a key to a member with nothing to copy.
- **Separate OS accounts on one machine**, with a group-readable socket
  and peer credentials mapped to members. It isolates people in the
  operating system, which is stronger, but it needs an administrator to
  create accounts and groups, and it works only for people on the same
  machine. It is a natural second way in for the same members later (see
  *Open questions*).
- **Sharing a tmux-style session.** It gives everyone everything, with no
  roles, no audit and garbled input.
- **One input holder per environment.** Simpler, but it blocks a whole
  team while one person types into one pane.
- **Locking input only between members, not for you.** You could still
  garble a member's prompt. The holder applies to everyone, and you can
  take a pane with `force`.
- **Waiting for the hosted service (M29).** It would make teams depend on
  a service. The roadmap puts local-first first, and the hosted service on
  top.

## Open questions

1. **`authorized_keys`.** Should `hive access invite` edit it by default,
   behind a confirmation (the proposal), or only print the line?
2. **Local teammates.** Should 1.8 also accept members by OS account on a
   shared machine (`[access] users`), or leave that for later?
3. **Operators and shell panes.** Is "agent panes only" right, or should a
   grant be able to allow shell input (`--shell`) for trusted operators?
4. **Prompt text in the audit.** Off by default (the proposal), or on,
   since prompts are what a reviewer most wants to see?
5. **Expiry.** Is 90 days the right default for people, against 30 days
   for observer credentials?
6. **Notifications nobody sees.** When neither the owner nor the fallback
   is connected, should the daemon fall back further, to Web Push or a
   workflow trigger (`[[on]] notify`)?

## Delivery

Each step is a pull request that links this RFC:

1. Members and the member socket: `access/members.json`,
   `access.invite|list|get|renew|revoke`, `hive access`, `hive proxy
   --member`, the managed `authorized_keys` block, connections in the
   audit log, revocation that ends connections.
2. Roles: the role table, grants per environment, observer views below
   owner, agent-pane-only input for operators, filtered welcomes,
   `access.whoami`, `Approval.by` from identity, actions in the audit log.
3. Shared workspaces: presence, per-member focus, the input holder, and
   their display in the TUI, desktop app and web observer.
4. Handing over: `owner` on agents, tasks and plans, `agent.handover`,
   `task.assign`, and `actor` on events.
5. Notifications routed by the daemon, with the fallback.
6. The acceptance test, `docs/teams.md` and the threat model; then tag
   `v1.8.0`.

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
