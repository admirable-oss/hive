# Observers

An observer is a program that watches Hive without driving it: a dashboard on
a second screen, a bot that posts to chat when an agent is blocked, the
coming web and mobile observers. It holds an **observer credential** that
says what it may see and until when. It connects to the daemon's **observer
socket**, where every method only reads. There is no method there that types
into a terminal, changes a layout, or starts or stops anything.

```sh
hive observer create dashboard --all                  # prints a token, once
export HIVE_OBSERVER_TOKEN=hvo_…
hive observer status                                  # as the observer
hive observer events                                  # JSON lines until interrupted
go run ./examples/observer-dashboard                  # a live agent board
```

## What an observer can and cannot do

**Can do**, within its scope:
- read the daemon's health;
- read environments, tabs, panes, agents and their states, and sidebar metadata;
- follow events as they happen;
- with screen access, read and watch pane screens and recent output.

**Cannot do:**
- send terminal input;
- prompt agents;
- resize, split, move or close anything;
- start or stop processes or agents;
- create environments;
- make, widen or revoke credentials;
- reach anything outside its scope.

None of these methods exist on the observer socket: a call answers
`unknown_method`. A pane outside its scope answers `not_found`, the same as a
pane that does not exist.

What an observer receives is a set of **views** built for observers. They
leave out everything that can carry secrets:
- environment variables;
- command lines and arguments;
- processes' working directories and PIDs;
- agents' own session IDs.

A process shows only its program's name (`claude`, not `/home/me/bin/claude
--api-key …`).

### API access is not OS isolation

Observer credentials are enforced by the daemon's API. They do not isolate
anything in the operating system:
- **A program running as your user can still open the control socket**
  (`hive.sock`) and do anything you can. A token limits what an honest
  program does with it, and what a leaked token is worth.
- **Only your own user's processes can connect**, to the observer socket as
  to every Hive socket.
- **To hold an observer to its token**, run it somewhere else (another
  machine, container or account) and let it in only through SSH, with a key
  that can do nothing but reach the observer socket:

  ```
  # ~/.ssh/authorized_keys on the machine running Hive
  command="hive proxy --observer",restrict ssh-ed25519 AAAA… dashboard@monitor
  ```

  Whatever that key asks to run, it gets only the observer socket, where
  only a valid token works (see [Remote observers](#remote-observers)).
  Use the full path to `hive` if it is not on the login PATH.
- **Screen access exposes whatever is on the screen.** That includes secrets
  an agent prints. Grant `--screens` only to observers that need it.

## Credentials

```sh
hive observer create <name> (--all | --env <id>… | --pane <id>…) [--screens] [--expires 720h]
hive observer list                 # state, scope, last use, open connections
hive observer show <id>
hive observer revoke <id>          # its open connections end at once
hive observer validate             # check $HIVE_OBSERVER_TOKEN without using it (exit 1 if unusable)
hive observer audit [--credential <id>]
```

Managing credentials needs control of the daemon: `hive.sock`, or `--remote`.
No plugin can manage them, whatever capabilities it holds.

- **Scope.** A credential sees either the whole session (`--all`) or the
  listed environments and panes. Each session is a separate daemon with
  separate credentials, so a token always belongs to one session.
  - An environment brings its tabs, panes, agents and processes.
  - A pane alone brings its process and agent, but not its tab or
    environment.
  - Scopes are fixed when the credential is made. Nothing a client sends can
    widen them. A second `observe.auth` on the same connection is refused.
- **Screens.** Without `--screens`, an observer sees metadata and events
  only. With it, it can also read screens, scrollback and recent output in
  its scope.
- **Expiry.** Every credential expires. The default is 30 days, and the range
  is 1 minute to 365 days. An expired credential's connections end at its
  expiry.
- **Revocation.** `hive observer revoke` ends the credential's open
  connections at once, and refuses new ones. Revoked and expired credentials
  stay listed for 30 days.
- **Tokens.** A token looks like `hvo_<id>_<secret>`. It is printed once, when
  the credential is made. The daemon stores only a SHA-256 hash of the
  secret, in `observers/credentials.json` (mode 0600). Commands that act as
  an observer read the token from `$HIVE_OBSERVER_TOKEN` or `--token-file`
  (`-` reads standard input), never from a flag that other users could read
  in the process list.
- **Audit.** `observers/audit.log` (JSON lines, rotated at 1 MiB with two older
  files kept) records:
  - credentials made and revoked;
  - connections that authenticated;
  - tokens refused (invalid, expired or revoked);
  - requests outside a scope;
  - connections ended, and why.

  It never holds a token. `hive observer audit` reads it.

## Connecting

The observer socket is `observer.sock` next to `hive.sock` in the session's
directory (`hive observer list` prints its path). It is mode 0600, and only
your user's processes may connect, as with every Hive socket. It is off when
`observers.enabled = false`; credentials are kept either way.

The wire protocol is the daemon's protocol 2 (see the README):
1. The client sends `hello`.
2. Its first request is `observe.auth` with the token. Until then, every
   method except `observe.auth` and `observe.schema` answers
   `permission_denied`.
3. A connection that has not authenticated within 10 seconds is closed. So is
   one that presents three bad tokens.

From Go, use `pkg/hiveapi`:

```go
o, err := hiveapi.Observe(ctx, hiveapi.ObserverOptions{}) // $HIVE_OBSERVER_TOKEN, this machine
snap, err := o.Snapshot(ctx)

// Or keep a view current across reconnects and missed events:
err = hiveapi.Follow(ctx, hiveapi.ObserverOptions{Host: "buildbox"}, func(u hiveapi.ObserverUpdate) error {
	if u.Snapshot != nil { /* replace your model */ }
	if u.Event != nil { /* apply one event */ }
	return nil
})
```

### Remote observers

`hive proxy --observer` bridges standard input and output to the observer
socket. Running it over SSH is how observers reach another machine:

```sh
hive --remote buildbox observer create dash --all     # made on buildbox
HIVE_OBSERVER_TOKEN=hvo_… hive --remote buildbox observer status
```

`hiveapi.ObserverOptions{Host: "buildbox"}` does the same from Go. The token
is checked by the remote daemon, exactly as for a local observer. A token
from one machine means nothing to another. With the restricted key above,
the observer's machine can reach the observer socket and nothing else.

## Methods

The observer API is frozen in [`api/v1/observer.txt`](../api/v1/observer.txt).
Its JSON Schema is [`api/v1/observer-schema.json`](../api/v1/observer-schema.json),
and `observe.schema` returns the same document.

| Method | Params | Result |
|--------|--------|--------|
| `observe.auth` | `token` | the credential; the event stream's `epoch` and `seq` |
| `observe.schema` | – | the observer API's schema |
| `observe.status` | – | version, session, start time, API level, `epoch`/`seq`, the credential, counts in scope |
| `observe.snapshot` | – | `epoch`, `seq`, and the `environments`, `tabs`, `panes`, `agents` and `metadata` in scope |
| `observe.pane` | `pane` | one pane, with its agent and metadata |
| `observe.screen` | `pane`, `ansi`, `scrollback` (≤ 1000) | the screen as lines; cursor, size, title, `alt_screen` (needs screens) |
| `observe.output` | `pane`, `lines` (default 100, ≤ 1000), `ansi` | the newest lines of output (needs screens) |
| `observe.history` | `epoch`, `since`, `limit` (default 100, ≤ 1000) | a page of events after `since` (see below) |
| `observe.events` | `epoch`, `since` (optional) | a pipe: one event per line |
| `observe.watch` | `pane`, `ansi`, `interval_ms` (100 to 10000, default 250) | a pipe: screen updates (needs screens) |

Errors use the protocol's codes:
- `permission_denied`: not authenticated, a token that is invalid, expired or
  revoked, or no screens permission.
- `not_found`: not in scope, or does not exist.
- `unknown_method`: anything that would change state.
- `unavailable`: a limit was reached (see below).

### Screens

`observe.screen` returns one string per row of the screen:
- trailing blanks are removed;
- with `ansi`, SGR styling is kept;
- the cursor is 0-based;
- `scrollback` lines come before the screen, oldest first.

`observe.watch` sends a **keyframe** first, then updates that list only the
rows that changed:

```json
{"keyframe":true,"width":80,"height":24,"cursor":{"x":2,"y":3,"visible":true},"lines":[{"y":0,"text":"$ make test"},…]}
{"width":80,"height":24,"cursor":{"x":0,"y":5,"visible":true},"lines":[{"y":4,"text":"ok  ./..."}]}
```

A row not listed is unchanged. A row past the end of its text is blank. A
size change comes as a keyframe. Updates come at most once per
`interval_ms`, and changes made in between are merged into the next one. A
slow observer therefore gets fewer updates, never a wrong screen.
`ScreenUpdate.Apply` (Go) keeps a screen's lines current. A watch ends when
the pane's program exits or the pane leaves the observer's scope.

## Events

Every event has a `seq`, a `type`, a `time` and `data`. The data is an
observer view:

| Type | Data |
|------|------|
| `environment.created`, `environment.updated` | Environment |
| `environment.removed` | `{"id"}` |
| `environment.git` | `{"id", "git"}` |
| `tab.created`, `tab.closed`, `tab.updated`, `layout.updated` | Tab (with its pane IDs) |
| `pane.created`, `pane.closed`, `pane.moved`, `pane.focused`, `pane.updated` | Pane |
| `process.started`, `process.exited`, `process.recovered` | Process (program name, status, exit code, pane) |
| `process.output` | `{"id", "pane_id"}`: a screen changed (at most once a second per agent) |
| `agent.state` | Agent (state, why, since, `state_seq`, `completion_seq`) |
| `metadata.changed` | Metadata (title and tokens of a pane or environment) |
| `worktree.created`, `worktree.removed` | `{"path", "repo", "branch", "environment_id"}` |
| `observer.resync` | `{"reason", "epoch"}`: take a new snapshot |

Other daemon events (plugins' state, permission requests, session restores)
are not sent to observers.

### Sequence numbers, history and resynchronising

- **Epochs.** `seq` numbers the daemon's events. They are comparable only
  within an **epoch**, which is one daemon run. A restarted daemon starts a
  new epoch.
- **Gaps.** An observer sees gaps in `seq`: events out of its scope, and
  events not sent to observers at all. A gap never means a loss.
- **Loss.** A loss is always told by an `observer.resync` event.
- **History.** The daemon keeps the last `observers.history_events` events
  (default 2048) for observers to catch up with.

**Starting.** Open `observe.events` without `since`; its result's `seq` is
where live events start. Then take `observe.snapshot`. The snapshot's own
`seq` was read before its state, so:
- events with a `seq` up to the snapshot's are already in it;
- later ones may be in it too.

Apply events by ID, so applying one twice changes nothing.

**Reconnecting.** Open `observe.events` with the last `epoch` and `seq` you
applied. Retained events after it are replayed first, then live ones follow.
The stream starts with `observer.resync` instead when:
- the position is older than the history, or
- the epoch is not the daemon's current one.

In that case, take a new snapshot and continue with the events after it.

**Falling behind.** Each stream queues up to 256 events for its reader. A
reader that falls further behind loses its queue, and its next event is
`observer.resync`.

**History pages.** `observe.history` pages through the same history:
- `events` lists up to `limit` events in scope after `since`;
- `next` is the `since` for the following page;
- `more` says there is one;
- `last` is the newest position.

When `resync` is set, `events` is empty: snapshot instead.

`hiveapi.Follow` (and `observer.Follow`) does all of this. It reconnects with
growing backoff, from half a second up to 30 seconds. It stops only when its
context ends, when the token stops working (revoked or expired), or when
your function returns an error.

## Limits

Observers cannot crowd out the people and agents using Hive:
- **Separate listener.** Observers have their own socket and their own
  connections.
- **Connection limits.**
  - At most `observers.max_connections` open connections in all (default 32).
  - At most 8 per credential.
  - At most 8 open streams on one connection.
- **Request limits.**
  - At most 8 requests in flight on one connection.
  - About 20 requests a second, in bursts of up to 40.

  Over these limits, a request answers `unavailable`.
- **Bounded reads.** Screens, output, scrollback and history pages have the
  maxima listed above.
- **Events never wait for observers.** A slow observer loses events (and
  resyncs) instead of slowing the daemon.
- **Stalled connections end.** A connection that stops reading for
  30 seconds is closed. So is one that does not authenticate within
  10 seconds.

## Configuration

```toml
[observers]
enabled = true          # serve the observer socket
history_events = 2048   # events kept for catching up (64 to 100000)
max_connections = 32    # open observer connections, all credentials together
```

## Compatibility

The observer API follows Hive's
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/).
Within 1.x it only grows:
- methods, fields and event types are added, never removed or changed in
  meaning;
- `api/v1/observer.txt` holds what is promised, and the tests fail on a
  breaking change.

Clients should therefore:
- ignore unknown fields and event types;
- branch on error codes, not messages.

## Examples

- [`examples/observer-dashboard`](../examples/observer-dashboard/main.go): a
  live board of every agent's state, local or over SSH (`-host`).
- [`examples/observer-notifier`](../examples/observer-notifier/main.go): posts
  to a webhook when an agent is blocked or done, with the end of its screen.
  Each state change is announced once, across reconnects.

## Troubleshooting

- **`no observer token`**: set `HIVE_OBSERVER_TOKEN`, or pass `--token-file`.
- **`invalid, expired or revoked token`**: run `hive observer validate`
  (with control of the daemon) to see which. Tokens belong to one session on
  one machine.
- **`connect to the observer socket: … no such file`**: the daemon is not
  running (observers never start it), or `observers.enabled = false`.
- **`this credential does not allow screen contents`**: make a credential
  with `--screens`.
- **`unavailable: too many …`**: a limit above. Reuse one connection for
  several streams, and slow down polling. Better, follow events instead of
  polling.
