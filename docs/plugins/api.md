# Hive plugin API 1

Status: live (M12), 2026-10-15. This is the contract between Hive and its
plugins: what a manifest may declare, what a plugin command receives, what it
may call, how Hive runs it, and what it may add to the UI.
`hive plugin capabilities` prints the parts of it that your hive runs.

## Versions and compatibility

A manifest says which plugin API it is written for:

```toml
api = 1
```

- **Within a version nothing is removed or changed.** Context fields, event
  types and their data, RPC methods and their parameters, manifest keys,
  capabilities and contribution kinds are only ever *added*. Plugins must ignore
  fields they do not know.
- **A hive runs every version listed in its `supported_apis`** (this hive: `[1]`).
  A manifest asking for an API the hive does not run is refused when it is
  validated, installed or loaded, with "needs plugin API N; this hive runs …".
  It is not half-understood: the check comes before keys this hive does not
  know are rejected.
- **Manifests without `api` are unversioned (legacy).** They run as API 1, hold
  every capability (they declare none) and may not contribute UI. `hive plugin
  doctor` and `hive plugin validate` warn about them.
- **Contract tests** hold every supported version to the shared fixtures in
  `sdk/protocol/fixtures/v1/`: the reference manifest, an action context, an
  event context, discovery, and a `plugin.card` request. The TypeScript, Python,
  Rust and Go SDKs test against the same files.

## The manifest (`hive-plugin.toml`)

Every API 1 key, with the reference manifest at
[`sdk/protocol/fixtures/v1/manifest.toml`](../../sdk/protocol/fixtures/v1/manifest.toml):

| Key | Meaning |
|---|---|
| `api` | The plugin API version (1). |
| `id`, `name`, `version`, `description` | Identity. `id` is lower-case letters, digits and dashes. |
| `min_hive_version`, `max_hive_version`, `platforms`, `[[runtime]]` | Where it runs. |
| `isolation = "strict"` | It can run sandboxed (see Isolation). |
| `event_concurrency` | Its event commands running at once, 1–16 (default 4). 1 runs them one after another in event order. |
| `[permissions]` | `required` and `optional` capabilities, and `reasons` (shown when installing). |
| `[[startup]]` | Commands that run while the daemon runs; `restart = true` restarts them. |
| `[[actions]]` | Commands run on demand: `id`, `title`, `command`, `timeout` (default 2m, at most 1h), `async`. |
| `[[events]]` | Commands run for an event type (`on = "worktree.created"`) or prefix (`on = "agent."`), with a `timeout`. |
| `[configuration]`, `[[migrations]]` | Its settings schema and how settings move between schema versions. |
| `[contributes]` | What it adds to the UI (see Contributions). |

## What a command receives

Every command runs in the plugin's directory and receives:

- **On stdin, and in the file `$HIVE_PLUGIN_CONTEXT`:** the context.

  ```json
  {
    "api": 1,
    "plugin": "contract",
    "trigger": "action",
    "action": "show",
    "run": "contract-1",
    "session": "work",
    "environment": "api",
    "tab": "t1",
    "pane": "p1",
    "capabilities": ["hive.read", "hive.write"]
  }
  ```

  `trigger` is `startup`, `action`, `event` or `migration`. An event context
  adds `"event": {"type", "data", "seq", "time", "missed"}`, and `environment`,
  `tab` and `pane` are what the event is about. An action a workflow step
  runs ([workflows](../workflows.md)) adds `"workflow": {"workflow", "run",
  "step", "with", "output"}`: `with` is what the step gives it, and `output`
  (also `$HIVE_OUTPUT`) is a file it may write `name=value` lines to, which
  become the step's outputs.
- **Environment variables:**

  | Variable | Value |
  |---|---|
  | `HIVE_PLUGIN_ID`, `HIVE_PLUGIN_VERSION`, `HIVE_PLUGIN_DIR` | Identity and installed directory. |
  | `HIVE_PLUGIN_API` | The API version it runs under. |
  | `HIVE_PLUGIN_CAPABILITIES` | Comma-separated capabilities it holds now. |
  | `HIVE_PLUGIN_DATA` | Persistent data, kept across updates and uninstalls. |
  | `HIVE_PLUGIN_CONFIG`, `HIVE_PLUGIN_CONFIG_DIR` | Its settings file and directory. |
  | `HIVE_PLUGIN_CACHE` | Disposable cache, removed on uninstall. |
  | `HIVE_SOCKET_PATH`, `HIVE_PLUGIN_SOCKET` | Its own daemon socket: SDKs use the first, the `hive` command the second. |
  | `HIVE_BIN` | The `hive` binary, for scripts. |

## Calling Hive

A plugin reaches the daemon only on **its own socket**. The SDKs and the `hive`
command find it from the environment above. Every call there is checked
against the plugin's capabilities. The connection's welcome lists only the
methods it may call, and anything else is refused with `permission_denied`,
naming the capability it needs.

| Capability | Lets it | Enforced by |
|---|---|---|
| `hive.read` | Read the workspace: environments, tabs, panes, agents, screens, events. | The daemon |
| `hive.write` | Change the workspace: tabs, panes, layouts, names, metadata, worktrees. | The daemon |
| `hive.input` | Type into terminals and prompt agents. | The daemon |
| `hive.process` | Start and stop processes and agents; remove environments and worktrees. | The daemon |
| `hive.admin` | Run other plugins' actions, restore sessions, debug the daemon. | The daemon |
| `filesystem.write` | Write outside its own data, config and cache directories. | Strict isolation only |
| `network` | Use the network. | Strict isolation only |
| `process.exec` | Run other programs. | Declared, not enforced |

These methods act on the calling plugin alone and need no capability:

| Method | |
|---|---|
| `plugin.capabilities` | Discovery: API versions, capabilities, events, contribution kinds, isolation modes, and on its socket what it holds and every method it may call. |
| `plugin.card` | Show or clear one of its declared cards. |
| `plugin.status` | Show or clear one of its declared status indicators. |
| `plugin.ready` | Say its startup is done. |
| `plugin.request` | Ask the user for one of its optional capabilities. |
| `plugin.config`, `plugin.config.set` | Read and change its own settings. |
| `api.schema`, `runtime.ping` | Discovery and liveness. |

On its own socket a plugin cannot act as another plugin: a `plugin` parameter
naming someone else is refused. Every daemon method is classified, and a test
fails when a new method has no capability. Until it is classified, no plugin
may call it.

Scripts call the same methods through the CLI: `hive plugin card`, `hive plugin
status`, `hive plugin ready` and `hive plugin request`. These act on
`$HIVE_PLUGIN_ID`.

## Events

The event types of API 1 are listed below; their data is what `hive events`
prints.

`agent.state`, `environment.created`, `environment.git`, `environment.removed`,
`environment.updated`, `layout.updated`, `metadata.changed`, `pane.closed`,
`pane.created`, `pane.focused`, `pane.moved`, `pane.updated`, `plugin.changed`,
`plugin.contributions`, `plugin.permission`, `process.exited`,
`process.output`, `process.recovered`, `process.started`, `session.restored`,
`tab.closed`, `tab.created`, `tab.updated`, `worktree.created`,
`worktree.removed`

**Delivery:**

- **At most once.** Each plugin has a queue of 256 events. When it is full
  (the plugin's handlers are slower than its events), events are dropped for
  that plugin and counted in its health; they are never waited for. The next
  event it gets says `"missed": true`, as does the first one after the daemon
  itself lost events. Re-read the state you follow when you see it.
- **In order.** Events reach a plugin in `seq` order. With `event_concurrency
  = 1` each handler also finishes before the next starts. With more, up to that
  many run at once.
- **Bounded.** Each handler runs until its `timeout` (default 2m). A handler
  that fails or times out is logged, counted and makes the plugin *degraded*;
  the event is not retried.
- **Unknown events.** Handlers may name event types or prefixes this hive never
  publishes; they never fire. `validate` and `doctor` warn about them.
  `plugin.*` events go to the UI and other clients, never to plugin handlers.

## Actions

An action runs when the user picks it: from the CLI (`hive plugin run`), a key
binding (`[[keys.command]] type = "plugin_action"`), the palette, or a
contextual action.

- **Synchronous actions** (the default) are waited for. The caller gets their
  exit status and output. A run that passes its `timeout` is stopped and
  reported as timed out. If the caller goes away (closes the UI, or Ctrl+C on
  the CLI), the run is stopped.
- **Async actions** (`async = true`) return at once with a run ID. Their
  outcome goes to the plugin's health and log.
- **Cancelling.** Any run can be stopped with `hive plugin cancel <id> <run>`;
  `hive plugin info <id>` lists the runs in progress. A stopped run gets
  SIGTERM, then SIGKILL after 3 s, and so does everything it started (its
  process group).

## Contributions

What a plugin adds to the UI is **declared** in its manifest. Hive draws it
from data and never runs plugin code to draw, so a plugin cannot break the
layout, take keys or draw outside its slot.

```toml
[[contributes.cards]]      # a sidebar card, about the current environment or the focused agent
id = "ci"
title = "CI"
scope = "environment"      # or "agent"

[[contributes.status]]     # a short text on the tab bar
id = "builds"
title = "Builds"

[[contributes.actions]]    # its action, offered where the user is
action = "rerun"
contexts = ["environment", "pane", "agent", "session"]

[[contributes.palette]]    # its action in the command palette (prefix :)
action = "rerun"

[[contributes.links]]      # a fixed http(s) link in the palette
id = "dashboard"
title = "CI dashboard"
url = "https://ci.example.com"
```

- **Cards** show rows a plugin sets at run time with `plugin.card`. A row is a
  label and a text with a tone (`info`, `success`, `warning`, `error`), plus up
  to 3 http(s) links. Limits: 6 rows, labels of 16 characters, texts of 48
  (longer ones are cut, control characters removed). Environment cards show
  under the sidebar's **Plugins** section for the current environment, agent
  cards for the focused agent's pane.
- **Status indicators** show a short text (24 characters) on the tab bar,
  coloured by tone.
- **Content expires.** A card or indicator lasts its TTL (default 30 minutes,
  at most 7 days) unless set again, and goes when its plugin stops. A crashed
  plugin leaves nothing stale.
- **The palette** (prefix `:` or `P`) lists the contextual actions that apply
  where the user is, palette entries, links (declared, and on visible cards),
  and a **Configure** entry for each plugin with settings. Picking an action
  runs it with the user's environment, tab and pane in its context.
- **Settings forms** are generated from `[configuration]`. Each setting is
  edited by its type (booleans toggle), checked against the schema, saved
  through the daemon, and the plugin restarts on its new settings.
- **Narrow screens.** Plugin indicators are the first thing dropped from the
  tab bar: none below 100 columns, at most two below 160. Cards live in the
  sidebar, which narrow screens hide (prefix `b` shows it). Overlays fit the
  screen.
- **Turning contributions off.** `hive plugin contributions` lists each one
  with its key; `[plugins] hidden = ["ci", "ci:status", "ci:card:ci"]` turns
  off a plugin's, a kind's or one contribution.
- **Limits** per plugin: 4 cards, 4 indicators, 16 contextual actions, 16
  palette entries, 8 links.

## Permissions

```toml
[permissions]
required = ["hive.read", "hive.write"]   # it cannot run without these
optional = ["network"]                    # it can use these when the user grants them
[permissions.reasons]
network = "fetch build results"
```

- **Installing** (`hive plugin install`) shows every command a plugin runs,
  the capabilities it requires and may ask for (with reasons), its
  contributions, and whether it can run isolated, then asks. Accepting grants
  its required capabilities.
- **Updating** shows what changed: new required capabilities, optional ones
  that became required, new optional ones, and dropped ones. Accepting grants
  the new required ones, and optional grants it still asks for carry over.
- **Optional capabilities** are granted only by the user: `hive plugin grant
  <id> <capability>`, or `[plugins] allow` for every plugin that asks. A plugin
  can ask at run time (`plugin.request`); the UI shows the request with the
  command that grants it. Asking never grants.
- **`hive plugin revoke`** takes a capability back. Revoking a required one
  stops the plugin until it is granted again.
- **`[plugins] deny`** withholds capabilities from every plugin, whatever was
  granted. A plugin that requires a denied capability does not install or run.
- **Legacy (unversioned) plugins** hold every capability. They do not run
  under a `deny` list that takes one away.

## Isolation and the security boundary

**Hive has no in-process plugins.** Every plugin command is a separate process
started by the daemon, running as you. What the capability model can and
cannot promise follows from that:

- **Standard isolation** (the default for plugins that do not declare
  `isolation = "strict"`). The plugin's process has your user's access to your
  files, network and programs. Its Hive capabilities are enforced on its
  socket, and honest plugins (and anything they run through `hive` or an SDK)
  stay within them. A *malicious* plugin is not contained: it could read your
  files, run programs, or find and use the daemon's own socket like any
  program you run. Declarations of `filesystem.write`, `network` and
  `process.exec` are shown to you, not enforced. **Install only plugins you
  trust; a manifest is a statement, not a sandbox.**
- **Strict isolation.** Hive runs the plugin under the system sandbox:
  `sandbox-exec` on macOS, bubblewrap (`bwrap`) on Linux. It applies to a
  plugin that declares `isolation = "strict"` (with `[plugins] isolation =
  "auto"`, the default), or to every plugin with `isolation = "strict"` in your
  config. A strict plugin:
  - writes only its data, config and cache directories (and `/dev/null`) unless
    granted `filesystem.write`;
  - has no network unless granted `network`;
  - reaches only its own daemon socket. On macOS no other local socket is
    reachable at all. On Linux the daemon's socket is covered, while sockets of
    other programs and sessions in the filesystem remain reachable;
  - sees a minimal environment: Hive's variables, `PATH`, locale and terminal
    variables, with `HOME` and `TMPDIR` in its cache. Tokens and other
    variables from your environment are not passed.

  Reads are not restricted, and the sandbox does not limit CPU or memory.
  Where no sandbox tool is available (Windows, or Linux without `bwrap`),
  `auto` runs plugins standard; with `[plugins] isolation = "strict"` they do
  not run at all (never silently unsandboxed).

## Plugin API 2: sandboxed by default (Hive 1.7)

A manifest with `api = 2` gets everything plugin API 1 has, and runs in the
system sandbox wherever one exists, whatever `[plugins] isolation` says
short of `strict` (a setting never silently loosens API 2). Plugin API 1
plugins run exactly as before. Hive 1.7 runs both (`supported_apis` is
`[1, 2]`); earlier releases refuse API 2 (`unsupported plugin API`).

**What a sandboxed API 2 plugin may touch**, enforced on macOS by
`sandbox-exec` and on Linux by bubblewrap with Landlock (5.13 and later):

| | Without a grant | With it |
|---|---|---|
| Read files | its package, data, config and cache, and the system (`/usr`, `/etc`, `/System`, `/Library`, Homebrew, Nix, …); your home directory and everything else are not there | `filesystem.read`: everything; `filesystem.read:workspace`: also your environments' directories |
| Write files | its data, config and cache | `filesystem.write`: everything; `filesystem.write:workspace`: also your environments' directories |
| Network | none | `network`: the hosts in `[permissions.network] hosts` through its own proxy, or any host when it lists none |
| Programs | its own package, shells, its `[[runtime]]` commands, and `hive` | `process.exec`: the programs in `[permissions.process] exec`, or any program when it lists none |
| Hive's sockets | its own socket only | — |

```toml
api = 2
id = "linear"
version = "1.0.0"
min_hive_version = "1.7.0"

[permissions]
required = ["hive.read", "network", "process.exec"]
optional = ["filesystem.read:workspace"]
[permissions.network]
hosts = ["api.linear.app:443", "*.linear.app:443"]
[permissions.process]
exec = ["git", "gh"]
```

- **Hosts** are `host:port`, a host name or address, or `*.domain` for its
  subdomains; at most 32. The plugin's programs get `HTTPS_PROXY`,
  `HTTP_PROXY` and `ALL_PROXY` pointing at a proxy the daemon runs for this
  plugin alone, with credentials only it has. The proxy allows `CONNECT`
  (and plain `http://` requests) to the listed hosts and nothing else, and
  logs each refusal in the plugin's log. It sees host names, never the
  traffic inside TLS. Programs that ignore proxy variables cannot reach the
  network at all.
- **Programs** are names found on `PATH` when the plugin loads, or absolute
  paths; at most 32. A program that is not found is a warning in `hive
  plugin info`, and cannot run. Scripts may use any shell.
- **`isolation = "none"`** with `isolation_reason` asks to run outside the
  sandbox. It needs the `unconfined` capability, which you approve when you
  install the plugin, with the reason shown, and again for every release.
- **A system without a sandbox** (Windows, or Linux without `bwrap`) runs
  an API 2 plugin only once you grant `unconfined` (`hive plugin grant <id>
  unconfined`), for that release.
- **`[plugins] isolation = "api2"`** runs only API 2 plugins: API 1 plugins
  do not run.
- **`hive plugin capabilities`** describes API 2; on its own socket a plugin
  discovers the capabilities of its own API.

The sandbox does not limit CPU or memory. With Landlock missing (Linux
before 5.13), bubblewrap still confines files, the network and sockets, but
not programs; the plugin's log says so.

### WebAssembly plugins

A plugin that needs no process at all can be WebAssembly: Hive runs its
modules itself, through [wazero](https://wazero.io), the same on every
system, Windows included, with no sandbox tool and no `unconfined`.

```toml
api = 2
id = "counter"
version = "1.0.0"
min_hive_version = "1.7.0"

[permissions]
required = ["hive.write"]

[[runtime]]
kind = "wasm"

[[actions]]
id = "bump"
command = ["./counter.wasm", "bump"]
```

- Every command's program is a `.wasm` module (WASI preview 1) in the
  plugin's package; its arguments follow. Build one with `GOOS=wasip1
  GOARCH=wasm go build`, `cargo build --target wasm32-wasip1`, or any WASI
  toolchain, and publish the built module in the release.
- The module sees its data directory as `/data` and its settings as
  `/config` (`HIVE_PLUGIN_DATA`, `HIVE_PLUGIN_CONFIG`), and nothing else. It
  gets its context on stdin, as every command does; its output goes to the
  plugin's log. It has no network and cannot start programs. Memory is
  bounded (64 MiB), and a command's timeout stops it.
- It calls Hive through the host module `hive`, with the same capability
  checks as a plugin's socket:

  ```go
  //go:wasmimport hive call
  func hiveCall(ptr unsafe.Pointer, size uint32) uint32 // request JSON in; the response's length out
  //go:wasmimport hive response
  func hiveResponse(ptr unsafe.Pointer)                  // copies that response to ptr
  ```

  The request is `{"method": "pane.list", "params": {…}}`; the response is
  `{"result": …}` or `{"error": {"code": "permission_denied", "message":
  …}}`. Call `response` once after each `call`, into a buffer of the
  length `call` returned.
- `hive plugin info` shows `sandbox: wasm`.

### Pane views

A plugin API 2 plugin can draw a pane from data. Clients draw it; they
never run plugin code to do so.

```toml
[[contributes.views]]
id = "queue"
title = "Review queue"
command = ["./bin/queue", "draw"]   # optional: run when a pane opens the view
```

- The plugin sets what a view shows with `plugin.view.set {view, doc}`
  (`hive plugin view queue --doc -`) and empties it with
  `plugin.view.clear {view}`. Both need `hive.write`. A view keeps its
  document until it is set again, cleared, or the plugin stops.
- A document is a tree of nodes, at most 2,000 of them, 32 deep and 256
  KiB:

  ```json
  {"root": {"kind": "column", "children": [
    {"kind": "text", "text": "3 waiting", "style": "bold"},
    {"kind": "table", "columns": ["PR", "State"], "rows": [["#12", "open"]]},
    {"kind": "progress", "text": "CI", "value": 0.6},
    {"kind": "row", "children": [
      {"kind": "button", "id": "approve-12", "text": "Approve", "action": "approve"},
      {"kind": "badge", "text": "2 failing", "style": "error"},
      {"kind": "link", "text": "Open", "url": "https://example.org/12"}]},
    {"kind": "divider"}]}}
  ```

  | Kind | Fields |
  |---|---|
  | `text` | `text`, `style`: plain, muted, bold, code, ok, warn, error |
  | `row`, `column`, `list` | `children` |
  | `table` | `columns`, `rows` (strings) |
  | `progress` | `value` from 0 to 1, `text` |
  | `badge` | `text`, `style`: info, success, warning, error |
  | `divider` | |
  | `button` | `id`, `text`, `action`: one of the plugin's `[[actions]]` |
  | `link` | `text`, `url`: `https://` only |

  Every string is text: no markup, no scripts, no images.
- A pane shows a view with `hive pane split --view <plugin>/<view>` or
  `hive tab create <env> --view <plugin>/<view>`, and a layout's pane with
  `"view": "<plugin>/<view>"`. Such a pane has no process. It is saved and
  restored like any pane, and `hive plugin views <plugin>/<view>` draws the
  view as text.
- Clicking a button (in the TUI, or tab to it and press enter; `hive
  plugin click <plugin>/<view> <button>`) runs the action it names, with
  `view` and `node` in its context. A plugin cannot click: a click is a
  person's.

### Workflow steps

```toml
[[contributes.steps]]
name = "deploy-preview"
command = ["./bin/deploy"]
timeout = "10m"                      # default 10m, at most 24h

[contributes.steps.inputs]
environment = { type = "string", required = true }
wait = { type = "boolean", default = true }

[contributes.steps.outputs]
url = { type = "string" }
```

A workflow uses it as `uses = "<plugin>/deploy-preview"`, with `with = {
… }` (see [workflows](../workflows.md)). Types are `string`, `boolean`,
`integer` and `number`. The command reads one JSON document on stdin:

```json
{"step": "deploy-preview", "inputs": {"environment": "pr-7", "wait": true},
 "workflow": {"workflow": "preview", "run": "3f2a…", "step": "deploy"},
 "environment": "api"}
```

and answers one on stdout, `{"outputs": {"url": "https://…"}, "summary":
"deployed"}`; what it writes to stderr goes to its log. Hive checks the
inputs before it runs the command and the outputs after; a failing
command, an answer that is not JSON, an undeclared output or one of the
wrong type fails the step. `hive workflow show` and `hive workflow
validate` name a workflow's steps that do not fit what is installed.
WebAssembly plugins can contribute steps too.

### Workflow triggers

```toml
[[contributes.triggers]]
name = "deploy-finished"
```

The plugin fires it with `workflow.trigger {name, payload}` (`hive.write`;
`hive plugin trigger deploy-finished --field state=error`), and gets back
the runs it started. Workflows that name it in `[[on]]` with `plugin =
"<plugin>/deploy-finished"` run when their `match` fits the payload; a
shared workflow runs only once it is trusted, as for every trigger.

## Lifecycle and health

| State | Meaning |
|---|---|
| `starting` | Loaded; its startup commands are not ready yet. |
| `ready` | Running. |
| `degraded` | Running, but something failed in the last 5 minutes: a command, a timeout, dropped events. |
| `failed` | It cannot run: its manifest, permissions, settings or sandbox are not right, or its startup command crashed past the restart limit. |
| `disabled` | Turned off by the user. |

- **Readiness.** A plugin with startup commands becomes ready when it calls
  `plugin.ready`, or after its commands have stayed up for 2 seconds. A
  one-shot startup command that exits 0 also counts. A plugin without startup
  commands is ready when loaded.
- **Restart limits.** A startup command with `restart = true` is restarted
  after 2 s, doubling up to 1 minute. If it exits within a minute of starting
  more than `[plugins] restart_limit` times (default 5) within
  `restart_window` (default 10m), it is given up on: the plugin is failed, and
  `hive plugin reload <id>` starts it afresh. A watcher that ran a while and
  stopped is restarted without counting against the limit.
- **Failures stay contained.** One plugin's commands, queue, crashes and
  timeouts never hold up the daemon or another plugin: events are queued
  without waiting, actions run in their own processes with timeouts, and each
  plugin's socket and lifetime are its own.
- **Inspecting.** `hive plugin list` shows each plugin's state.
  `hive plugin info <id>` shows its health (events delivered, failed and
  dropped; actions run, failed and timed out; restarts and crashes; warnings;
  pending capability requests; runs in progress) and what it holds.
  `hive plugin logs <id>` shows what its commands printed. The UI toasts when a
  plugin fails or asks for a capability.
- **`hive plugin doctor [id]`** checks the manifest and API version,
  compatibility, permissions against grants and policy, sandbox availability,
  handlers for events that never fire, settings, and live health. It exits
  non-zero when something is wrong.
- **Reloading.** `hive plugin reload <id>` stops one plugin's commands and
  starts it again from its manifest, grants and settings. Without an id, every
  plugin restarts. Changing settings, grants, or enabled state reloads the
  plugin by itself.

## Configuration and persistent state

| Directory | What | Kept |
|---|---|---|
| `HIVE_PLUGIN_CONFIG_DIR` | Settings (`settings.json`, schema-versioned). | Across updates; deleted only by `uninstall --delete-data`. |
| `HIVE_PLUGIN_DATA` | The plugin's own data. | Across updates; deleted only by `uninstall --delete-data`. |
| `HIVE_PLUGIN_CACHE` | Disposable. | Removed on uninstall; safe to delete any time. |

- **Schemas.** `[configuration]` declares settings with a type (`string`,
  `boolean`, `integer`, `number`), a description, a default and `required`.
  Settings files are plain JSON, so no secrets.
- **Missing settings** start from the defaults.
- **Invalid settings** (a wrong type, or a required one missing) stop the
  plugin with a problem pointing to `hive plugin config <id>`.
- **Obsolete settings** (no longer declared) are kept and reported as
  warnings, so rolling back to the release that used them loses nothing.
- **Migrations.** `[[migrations]]` move settings from one schema version to
  the next. They run before the plugin starts, on a staged copy that replaces
  the settings only when every step succeeds.
- **Editing.** `hive plugin config <id> [--set name=value] [--unset name]`
  (typed by the schema), or the UI's settings form.
- **Backups.** `hive plugin export [id…] -o backup.tar.gz [--data]` saves
  sources, enabled state, grants and settings (and with `--data`, the data
  directories). `hive plugin import backup.tar.gz [--data] [--force]`
  restores them for installed plugins: grants only for capabilities the
  manifest still asks for, settings migrated on the next start. It lists the
  plugins that are missing, with how to install them. Data is not overwritten
  without `--force`, and archive paths are checked.
- **Uninstalling** keeps settings and data unless you pass `--delete-data`.

## SDKs

| Language | Package | API 1 helpers |
|---|---|---|
| TypeScript | `@hive-oss/plugin-sdk` 0.4 | `has`, `setCard`, `setStatus`, `ready`, `requestCapability`, `capabilities` |
| Python | `hive_plugin_sdk` | `has`, `set_card`, `set_status`, `ready`, `request_capability`, `capabilities` |
| Rust | `hive-plugin-sdk` | `PluginContext::has`, `Hive::set_card`, `set_status`, `ready`, `request_capability`, `capabilities` |
| Go | `pkg/pluginsdk` | `Context.Has`, `Plugin.SetCard`, `SetStatus`, `Ready`, `RequestCapability`, `Capabilities` |

The SDKs' old `extend(...)` runtime contribution call never had a daemon
method. It now fails with a pointer to `[contributes]`.

Time-to-live values are nanoseconds on the wire (Go durations, as in
`metadata.report`); the SDK helpers take seconds.

## Reference plugins

- [`examples/plugins/pr-status`](../../examples/plugins/pr-status) puts each
  environment's pull request on a sidebar token and a **Pull request** card,
  counts open PRs on the tab bar, offers **Open this environment's PR** on
  environments, panes and agents, and offers **Refresh** in the palette.
- [`examples/plugins/spawn-reviewer`](../../examples/plugins/spawn-reviewer)
  starts a reviewer agent in every new worktree, or on demand from the
  environment's actions.
- The official [`auto-tab-title`](../../plugins/official/auto-tab-title)
  (strict isolation) and [`hive-explorer`](../../plugins/official/hive-explorer)
  plugins declare their permissions; the explorer is in the palette.

`cmd/hive/e2e_m12_test.go` runs these surfaces end to end through the CLI.
