# Teams

A **member** is a person you let into your runtime: a teammate who works
with your agents from their own machine, over ssh, with their own key and a
role you chose. Nothing is hosted: your machine, your `authorized_keys` and
the daemon are all there is. The design is
[RFC 0007](../rfcs/0007-team-collaboration.md).

> **New in 1.8 (M27).** Members, roles, presence, one typist per pane,
> handing work over, team notifications, the member socket, invitations,
> revocation and the audit log. The desktop app and the web observer show
> who is present and who is typing. The design is still a draft
> ([RFC 0007](../rfcs/0007-team-collaboration.md)) until it is accepted.

```sh
hive access invite alice --key alice.pub --role operator --env web
hive access list
hive access audit --member alice
hive access revoke alice           # her connections end at once
```

## How a member gets in

`hive access invite` adds one line for the member's key to your
`~/.ssh/authorized_keys`, inside a block Hive manages:

```
# >>> hive access (managed by hive; do not edit) >>>
command="/opt/homebrew/bin/hive proxy --member m_7f3k20a1c9",restrict ssh-ed25519 AAAA… hive:alice
# <<< hive access <<<
```

It shows the key's fingerprint and asks first (`--yes` answers). Lines
outside the block are never changed.

- **The key is the identity.** ssh checks the key, and the forced command,
  which only you can write, says which member it is. `hive proxy --member`
  connects to the daemon's member socket and tells it who this is before
  relaying anything; nothing the member's side sends can change that. There
  is no token to hand over or leak.
- **The key reaches nothing else.** `restrict` turns off port, agent and X11
  forwarding and the terminal. Whatever the member's ssh asks to run, it
  gets the proxy; the proxy serves only clients that asked for `hive proxy`
  and refuses logins and every other command.
- **Teammates use Hive as usual**: `hive --remote you@buildbox`, or a saved
  machine (`hive machine add`). Commands that run on the machine as a whole
  (`hive config`, `hive daemon`, `hive update`) are yours alone, and their
  key refuses them.
- **Your own key stays yours.** Inviting a key that already logs in to your
  account without restriction is refused: ssh uses the first line that
  matches, so the member would not be held to anything. Remove that line
  first.
- **Keys installed by someone else.** `--print` leaves `authorized_keys`
  alone and prints the line, for machines whose keys come from configuration
  management. `hive access doctor` reports lines that are missing, changed or
  left over, and keys that also log in unrestricted; `--fix` rewrites Hive's
  block.

`[access] authorized_keys` names another file, and `[access] enabled =
false` turns the member socket off (members are kept).

## Roles

A member has a role per environment, or for all of them (`--all`, those
made later included). When two grants cover an environment, the higher one
counts. You, on your own socket, are always the runtime's owner.

| Role | May |
|---|---|
| `observer` | read environments, tabs, panes, agents, processes, tasks, branches, events and screens |
| `operator` | also prompt agents and send keys to them, type into panes running an agent, approve tasks and plans, comment on branches; never start a process or type into a shell |
| `owner` | run the environment as you would: start and stop agents and processes, split, close and arrange panes, run and cancel plans, push branches and open pull requests |

```sh
hive access grant alice --env api --role observer
hive access grant alice --env api --role none     # take it away
```

The daemon checks the role on every request, against what the request acts
on: a pane, tab, agent, process, plan or branch belongs to an environment,
and the member needs the role there. Grants changed or revoked apply to the
next request, on connections already open too.

- **Only their environments exist.** Lists, the API snapshot and the event
  stream hold only what is in the member's environments. Asking for
  something elsewhere answers `not_found`, as for something that does not
  exist.
- **No secrets below owner.** Observers and operators receive records
  without environment variables, command arguments, working directories,
  process IDs or agents' session IDs; a command shows only its program's
  name. Screens are shown as they are.
- **Operators type only into agents.** Prompting, keys, pasted text and
  terminal input go only to panes where Hive sees a running agent; a pane
  whose agent exits stops taking an operator's input. A terminal view an
  observer opens drops whatever it types.
- **Their name, not their word.** Approvals, branch comments and workflow
  runs record the member's name, whatever their client says.
- **What spans environments** (workflows, insights, issues, plugins,
  worktrees, who observes) needs the role in every environment: a grant
  with `--all`. So do events that name no environment, creating and
  removing environments, and plans placed nowhere yet.
- **Never a member's**, whatever the role: managing members and observers,
  configuration, plugins (installing, settings, running their commands),
  updates, the daemon, `runtime.gc`, sessions' save and restore.
- **A member's focus is their own.** Moving focus (`pane.focus`,
  `tab.focus`, `agent.focus`) moves where they look, kept in presence; it
  never moves yours or anyone else's.

Every method's role is listed in [api/v1/roles.txt](../api/v1/roles.txt).

**A role is not a sandbox.** An operator cannot start processes through
Hive, but the agents they prompt run as you and can do anything you can.
Let operators direct only agents you would let them direct.

## Working together

**Presence.** Everyone connected says where they look: the multiplexer
tells the daemon as focus moves (`presence.set`). The tab bar shows who
else is in the environment on screen (`◇ with alice, bob`), and a pane
shows who is looking at it (`[alice]`). `hive presence` lists everyone,
their client, the environment, tab and pane they look at, and who is
typing where. A connection's presence ends with it.

**One person types at a time.** Each pane takes input from one person:

- Typing into a pane nobody holds takes it. While someone else holds it,
  your input is refused (`conflict`, naming them) and nothing is written;
  in the multiplexer the pane shows `[✎ alice]`, and what you type there
  is dropped.
- The holder keeps it until they stop typing for `[access] input_idle`
  (10 seconds by default), release it (`hive pane release`), or lose
  access. Leaving counts as no longer typing.
- `hive pane claim <pane>` takes it on purpose; `--force` takes it from
  whoever holds it, yours on your own runtime, a member's with the owner
  role in its environment.
- A prompt (`hive agent prompt`) holds the pane while it is written, so it
  never mixes with someone else's keys.
- The holder is a person, not a connection: your TUI, desktop app and
  scripts never refuse each other, so working alone is unchanged.

## Handing work over

Agents, plans and tasks belong to someone: the member who started them, or
you. What a member starts (a tab, a pane, an agent, a plan) is theirs, and
a task's agent belongs to the task's owner. Agents and plans from before
belong to you. `hive agent get` and `hive task get` show the owner.

```sh
hive agent handover <agent> alice       # its task and its pane's input go too
hive task assign <plan> fix --to alice  # one task, and its agent
hive task assign <plan> --to owner      # the plan and its unfinished tasks, back to you
```

- Work goes only to an operator or owner of its environment (`owner` is
  you).
- An operator hands over only what is theirs. An owner of the environment,
  and you, hand over anything in it.
- The task's log records who assigned it to whom, and the member's call is
  in the audit log.
- Events a member's request caused carry an `actor` naming them, so
  clients and workflows can tell who did what.

## Notifications

The daemon decides who is told that work needs someone, so a team is not
told everything twice and a member is not told about work that is not
theirs. When an agent is blocked or done, a task waits for approval or a
task fails, the daemon publishes a `notify` event addressed to:

- the work's **owner**, when they have a client connected and may still
  see it;
- otherwise, the **fallback**, `[access] notify_fallback`: members' names,
  `owner` (you) and `operators` (every operator and owner of the work's
  environment). It is `["owner"]` by default. When the fallback names
  nobody who may see the work, you are told.

```toml
[access]
notify_fallback = ["operators", "owner"]
```

Each client shows only what is addressed to its person, as its `[notify]`
settings say: the multiplexer and the desktop app follow `notify` events
when the daemon sends them (`access.whoami` says so), and decide for
themselves against an older daemon. A failed task notifies where `done`
does. Web Push keeps notifying everyone a link lets see the work.

## Expiry and revocation

Every member expires: 90 days by default, from 1 hour to 365 days
(`--expires`). `hive access renew alice` extends it from now.

`hive access revoke alice` ends her open connections at once, removes her
line from `authorized_keys`, and refuses her from then on, even if the line
is put back. Revoked and expired members stay listed for 30 days. Whatever
an agent did on a member's prompt stays done.

## Audit

`access/audit.log` (JSON lines, private, rotated at 4 MiB with four older
files kept) records:

- invitations, role changes, renewals and revocations;
- changes to Hive's block in `authorized_keys`;
- every connection, with where it came from (`SSH_CONNECTION`) and the
  client it says it is, and why it ended;
- every call a member makes that changes something (`action`), with where
  it acted and its outcome (`ok`, or the error's code);
- refusals (an unknown, expired or revoked member) and denials (a call the
  member may not make, or something outside their environments).

It never holds what a member types. `hive access audit [--member alice]`
reads it.

## Limits

- **Access control in the API, not the operating system.** As with
  [observers](observers.md#api-access-is-not-os-isolation), a program
  running as you can open the member socket and claim any member, or open
  the control socket instead. The boundary holds when a member's restricted
  key is their only way into the account: a teammate with your password, an
  unrestricted key or `sudo` to your account is you.
- **macOS and Linux hosts.** Windows is not a remote host yet; Windows
  clients can be members of a macOS or Linux runtime.
- **One session.** A member belongs to the session they were invited to.
