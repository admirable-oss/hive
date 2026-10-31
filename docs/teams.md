# Teams

A **member** is a person you let into your runtime: a teammate who works
with your agents from their own machine, over ssh, with their own key and a
role you chose. Nothing is hosted: your machine, your `authorized_keys` and
the daemon are all there is. The design is
[RFC 0007](../rfcs/0007-team-collaboration.md).

> **In progress (M27).** Members, the member socket, invitations,
> revocation and the audit log work today. Roles are stored but not yet
> enforced: until they are, a member can connect and see who they are
> (`hive access whoami`) and nothing else. Presence, one-at-a-time input,
> handing work over and team notifications follow.

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
| `observer` | read environments, agents, tasks, events and screens |
| `operator` | also prompt agents and send input to agent panes, approve tasks and plans; never start a process or type into a shell |
| `owner` | run the environment as you would: start and stop agents and processes, change layouts, push branches |

Managing members, configuration, plugins, observers, updates and the daemon
are never a member's, whatever their role.

```sh
hive access grant alice --env api --role observer
hive access grant alice --env api --role none     # take it away
```

**A role is not a sandbox.** An operator cannot start processes through
Hive, but the agents they prompt run as you and can do anything you can.
Let operators direct only agents you would let them direct.

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
- refusals (an unknown, expired or revoked member) and denials (a call the
  member may not make).

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
