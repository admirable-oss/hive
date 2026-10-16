# The web observer

Check on your agents from a browser: which are working, which are blocked on
a question, which are done. You can see their screens and follow them live.
The web observer is **read-only**: nothing in it can type into a terminal,
change a layout, or start or stop anything.

```sh
hive web runtime add laptop --all --screens    # 1. allow it to observe this session
hive web serve                                 # 2. run it (http://localhost:8484)
hive web share --runtime laptop --screens      # 3. a one-time sign-in link
```

Open the link in a browser. It signs that browser in, and the access lasts 8
hours unless you choose otherwise. `hive web revoke <id>` ends it at once.

## How it fits together

```
browser ──HTTPS──▶ hive web serve ──observer socket──▶ Hive daemon (this machine)
  (pages, live      (its own process;   └─ ssh … hive proxy --observer ──▶ Hive daemon (buildbox)
   updates)          no control socket)
```

- **A separate process.** The web observer is `hive web serve`, not part of
  any daemon.
- **One runtime, one observer credential.** Each runtime it shows (a session
  on a machine) is reached only through that daemon's
  [observer socket](observers.md), with an observer credential made for it.
  The web observer never holds a control socket, so it cannot ask a daemon
  to do anything but read. A browser can reach even less.
- **Tokens stay on the server.** Runtime tokens live in the web observer's
  state (`~/.hive/web`, mode 0600). Browsers never see them. A browser holds
  only a session cookie, scoped by its link.

## Runtimes

```sh
hive web runtime add <name> (--all | --env <id>… | --pane <id>…) [--screens] [--expires 2160h]
hive --remote buildbox web runtime add buildbox --env ci --screens
hive web runtime list
hive web runtime remove <name>
```

- **Adding a runtime is explicit consent.** Nothing reaches a browser until
  you have run `runtime add` and shared a link.
- **What `runtime add` does.** It makes an observer credential named
  `hive-web:<name>` on the runtime this command reaches: this session,
  `--session`, `--remote` or `--machine`. It keeps the credential's token in
  the web observer's state.
- **What a runtime limits.** Its scope and `--screens` cap what any link can
  show from it.
- **Remote runtimes** are reached the way `hive --remote` reaches them: SSH,
  then `hive proxy --observer` there. To hold the web observer's machine to
  read access only, give it an SSH key restricted to the observer socket
  (see [observers](observers.md#api-access-is-not-os-isolation)).
- **Removing a runtime** disconnects it at once. It also revokes the
  runtime's credential when this command can reach that daemon; otherwise
  it prints the command that does.

## Sharing and access

```sh
hive web share [--runtime <name>]… [--env <id>… | --pane <id>…] [--screens] [--history]
               [--expires 8h] [--link-expires 15m] [--uses 1] [--label "…"]
hive web grants              # what is shared, and the browsers signed in with it
hive web revoke <id>         # a grant (and all its browsers), or one browser session
hive web audit [--grant <id>]
```

### The link

- **One-time code, kept out of URLs.** A share link is
  `https://…/login#hwc_…`. The code sits in the URL *fragment*, which
  browsers never send to a server, so it reaches no access log, proxy or
  `Referer` header.
- **Exchanged for a cookie.** The sign-in page posts the code, the server
  swaps it for a session cookie, and the code leaves the address bar.
- **Short-lived and limited.** The code works for `--link-expires` (15
  minutes by default), and for `--uses` browsers (one by default).

### The session

- **Cookie.** The cookie is HttpOnly and SameSite=Strict. Under HTTPS it is
  also Secure, as `__Host-hive_web`.
- **Hashes only.** The server stores a hash of each code and cookie, never
  the values.
- **Lifetime.** Access lasts `--expires` (8 hours by default, 30 days at
  most) and cannot be extended. Share again instead.

### What a link sees

A link sees the intersection of the runtime's scope and its own `--env` or
`--pane` scope.

| The link allows | The viewer sees |
|-----------------|-----------------|
| (default) | Runtimes, environments, tabs, panes, agents and their states, reported metadata, activity |
| `--screens` | Also each pane's current screen, live |
| `--history` | Also output history (scrollback) on request, one read at a time, each one audited |

### Revocation

`hive web revoke` ends access immediately:
- open pages are sent to the sign-in page, which says access was revoked;
- every later request is refused.

Expiry works the same way, at the moment of expiry.

### Who sees that they are observed

- **The multiplexer.** While any observer is connected, its tab bar shows
  `◉ observed by …`. A pane whose screen someone is watching shows
  `[watched]`.
- **The web observer.** It connects to a runtime only while someone views
  it, plus a minute afterwards. So the marks mean someone is looking now.
- **The viewer.** Every page shows a Read-only badge. The Access page shows
  what the link allows and when it ends.

### Audit

`~/.hive/web/audit.log` (JSON lines, rotated) records:
- links shared;
- sign-ins, refused links and sign-outs;
- output history read;
- revocations.

It names the grant, the client's address and its user agent, and never a
code or a cookie. Each daemon's own observer audit log (`hive observer
audit`) also records the web observer's connections.

## On phones

The web observer is an installable app on phones, with an Attention view,
notifications (Web Push) and deep links to agents. See [Hive on your
phone](mobile.md).

## The app

- **Agents.** Every agent the link can see, across runtimes. Agents that
  need someone come first (blocked, then done). Search narrows the list as
  you type: press `/`, and `Esc` clears it. You can also filter by state.
  The filter stays in the URL, so a filtered view can be bookmarked.
- **Runtime.** Its connection, version, uptime and last synchronisation;
  environments with their git state and agent counts; agents; recent
  activity.
- **Environment.** Directory, worktree, git and reported metadata, plus
  tabs with their panes, and activity.
- **Pane.** The agent's state, why, and since when, plus its program and
  metadata. With `--screens`, the screen live. With `--history`, a button
  that reads recent output.

States follow the agents' own detection: working, blocked, done, idle,
exited, or unknown when Hive cannot tell.

### Live updates

Each page holds one event stream (server-sent events), and the server sends
only the parts of the page that changed:
- **Bounded rate.** At most about 7 updates a second per page. A pane's
  screen updates at most 4 times a second.
- **Ordered.** Every part carries a version, and an older part never
  replaces a newer one.
- **Connection state.** The indicator in the header shows Live, Connecting,
  Reconnecting or Offline, with the time of the last synchronisation. It
  shows *stale* when the stream has been silent for 45 seconds; the server
  pings every 15.
- **Showing last-known data.** While a runtime is unreachable, its pages
  keep showing the last known state, marked as data from a given time. When
  the runtime is back, the web observer takes a fresh snapshot and continues
  from it, including after a daemon restart.
- **Hidden tabs.** A tab hidden for 30 seconds closes its stream. It catches
  up when shown again, after a laptop wakes, or when the network comes back.

### Accessibility and themes

- **Keyboard.** Every page works from the keyboard: a skip link, visible
  focus, and landmarks. Updates keep your focus and your scroll position in
  a screen.
- **Screen readers.** The connection state is a polite live region.
- **Themes.** Light, dark, or following the system (the Theme button). Both
  themes meet WCAG AA contrast.
- **Without JavaScript.** Every page renders without JavaScript. Only live
  updates need it.

## Privacy

- **No telemetry.** The web observer collects no usage data.
- **Nothing written to disk.** It writes no terminal contents. Screens and
  output pass through its memory to the browser.
- **Nothing kept in the browser** but the theme choice. Pages are sent with
  `Cache-Control: no-store`.
- **Screens and history are opt-in, twice.** The runtime must allow them,
  and the link must too. Output history is read only when a viewer presses
  the button, and every read is audited.
- **No outside resources.** Pages load nothing from elsewhere: scripts and
  styles are built into hive, and the Content-Security-Policy forbids
  anything else.

## Deployment

### Configuration

```toml
[web]
listen = "127.0.0.1:8484"   # this machine only; see below for the network
public_url = ""             # where browsers reach it, for share links
tls_cert = ""               # a certificate and key to serve HTTPS
tls_key = ""
behind_proxy = false        # a reverse proxy in front terminates TLS
push = true                 # notifications on phones and browsers (see mobile.md)
push_contact = ""
push_allow_any_endpoint = false
```

Flags of `hive web serve` (`--listen`, `--public-url`, `--tls-cert`,
`--tls-key`, `--behind-proxy`) override these settings. Its log is
`~/.hive/web/web.log`.

### TLS and authentication requirements

- **Plain HTTP on loopback only.** On `127.0.0.1`, `::1` or `localhost`, the
  web observer may use plain HTTP.
- **Beyond loopback, TLS is required.** It refuses to start on any other
  address without one of:
  - its own TLS (`--tls-cert`, `--tls-key`; TLS 1.2 or newer, with HSTS);
  - a reverse proxy that terminates TLS (`--behind-proxy`). Set
    `--public-url https://…` too, so links and origin checks use the
    proxy's address.
- **Authentication is built in.** It is the share link and its session. A
  proxy may add its own (SSO, client certificates) on top.

### Reverse proxies

Caddy:

```
hive.example.com {
	reverse_proxy 127.0.0.1:8484
}
```

nginx: event streams must not be buffered, and must be allowed to stay open:

```
location / {
	proxy_pass http://127.0.0.1:8484;
	proxy_set_header Host $host;
	proxy_set_header X-Forwarded-For $remote_addr;
	proxy_http_version 1.1;
	proxy_buffering off;          # the app also sends X-Accel-Buffering: no
	proxy_read_timeout 1h;
}
```

Run `hive web serve --listen 127.0.0.1:8484 --behind-proxy --public-url
https://hive.example.com`. With `--behind-proxy`, the audit log names
clients by `X-Forwarded-For`. Let only the proxy reach the web observer:
keep it on loopback, or firewall it.

### Network patterns

- **Your machine only.** Use the defaults, and open the link here.
- **Your phone on the same network, or a tailnet.** Use TLS: Tailscale can
  provide a certificate (`tailscale cert`), or put a proxy in front.
- **A shared server for a team.** Run the web observer there, behind a
  proxy. Add runtimes from developers' machines with `--remote`, using SSH
  keys restricted to the observer socket. Each runtime's own credential,
  scope and expiry still apply. Remove a runtime, or revoke its credential
  on its machine, to cut it off.

### Running it as a service

A systemd user unit:

```ini
[Unit]
Description=Hive web observer

[Service]
ExecStart=%h/.local/bin/hive web serve
Restart=on-failure

[Install]
WantedBy=default.target
```

### Health checks

- `GET /healthz` answers `200 {"status":"ok"}` while the process serves.
- `GET /readyz` answers `200` when the web observer has runtimes and can
  read its state, and `503` otherwise (no runtimes, unreadable state). It
  includes counts of runtimes, live connections and open pages.

Neither needs sign-in, and neither names a runtime.

`hive web status` shows each runtime's connection, its last
synchronisation, and the last error.

### Troubleshooting

- **`serving beyond this machine needs TLS`**: see the requirements above.
- **The page says *Access refused* for a runtime**: its observer credential
  was revoked or expired. Run `hive web runtime add <name> …` again.
- **The page stays on *Reconnecting***: the runtime's daemon is down or
  unreachable. Check `hive web status`, and try `hive --remote <host> status`
  for remote ones.
- **Updates arrive late or in bursts behind a proxy**: the proxy is
  buffering. Turn buffering off, as shown above.
- **A link says it is invalid**: links work once, briefly. Make another
  with `hive web share`.
- **`the web observer is not running`**: `share`, `grants`, `revoke`,
  `audit` and `status` talk to a running server, through
  `~/.hive/web/web.sock`.

## Compatibility

- **Browsers:** current Chrome, Edge, Firefox and Safari (Safari 16.4 or
  newer), on desktop and mobile. The app needs EventSource, `<template>` and
  `CSS.escape`, with no polyfills.
- **Runtimes:** Hive daemons at observer API level 10 or newer (1.0.5).
  Daemons before 1.0.6 send agent states without their environment; the web
  observer keeps each agent where its last snapshot placed it.
- **Changes:** the web observer's URLs are stable within 1.x, so bookmarks
  keep working. Its pages and their fragments are not an API: program
  against the [observer API](observers.md) instead.

## Limits

| What | Limit |
|------|-------|
| Pages open per browser session | 12 |
| Live screen watches per runtime | 6 (more are refreshed every 2 s instead) |
| Browsers per link | 1 to 20 |
| Access per link | 1 minute to 30 days |
| Output history per read | 200 lines |
| Activity kept per runtime | the last 200 events seen |
| Sign-in attempts | 10 a second, in bursts of up to 20 |

Measured in the test suite (`TestManyViewersAndASlowOne`): with 19 open
dashboards and one connection that has stopped reading, a new agent reaches
every dashboard in about 0.3 s at the median and about 1 s at p95. That
time includes the daemon detecting the agent. A stalled viewer holds no one
else back.
