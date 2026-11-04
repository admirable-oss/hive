# RFC 0010: Editor integrations

- **Status:** Proposed. Open for comment; the council decides after the
  discussion period ([process](README.md#process)).
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`agent.files`, `branch.hunk`, the `agent.files` event, `Branch.reviews`), CLI (`hive editor serve | lsp | info`), a new frozen protocol (`api/v1/editor.txt`), the desktop app's renderer (shared, unchanged in behaviour), the release workflow, the threat model

## Summary

M31 brings Hive into VS Code, the JetBrains IDEs, Neovim and Zed:

- **`hive editor serve`**, one bridge every extension speaks: JSON-RPC 2.0
  on standard input and output, over `pkg/hiveapi`, as language servers
  are run. The extensions share its tested implementation and stay thin.
  Its protocol is frozen in `api/v1/editor.txt`.
- **`hive editor lsp`**, the same bridge as a language server, for editors
  whose extensions can only start language servers (Zed, and Helix,
  Sublime Text or Emacs without an extension of their own): gate results as
  diagnostics, agents' edits as hints, and code actions that send code to
  an agent.
- **In the editor:** agents and tasks with live states, blocked ones
  first; an agent's terminal in a panel, drawn from frames by the desktop
  app's renderer; a selection, a file, a diagnostic or a failing test sent
  to an agent with its path and lines; `path:line` in an agent's output
  opening that file.
- **Review where you edit:** a branch's diff in the editor's diff view,
  comments sent back as prompts, gate results as diagnostics, and hunks
  accepted or rejected one by one, through git in the agent's worktree.
- **Agents' edits, visible:** files an agent is changing are marked, with
  the agent and its task, and the editor warns before you edit one.
- Nothing new to trust: the bridge is a client of the local daemon, on its
  socket, like the CLI.

## Motivation

Most people read diffs and write code in an editor, not a terminal. Today
they switch to `hive ui` or the desktop app to see what agents do, copy
paths and line numbers into prompts by hand, and read `hive branch diff` in
a terminal instead of their editor's diff view. Two people editing the same
file at once is a conflict nobody sees coming: an agent writing a file you
also have open loses your change or its own.

Every editor has its own extension language (TypeScript, Kotlin, Lua, Rust
compiled to WebAssembly). Four clients of the daemon's full API would be
four implementations of agent ordering, prompt formatting, diff parsing,
hunk IDs, link detection and frame decoding, each with its own bugs. A
language server solves the same problem for language tooling: one server,
thin clients.

## Proposal

### The bridge

`hive editor serve` reads JSON-RPC 2.0 messages, one per line, from
standard input and writes them to standard output, as `hive mcp` does. It
connects to the daemon as every `hive` command does (inside a Hive pane,
that pane's session; elsewhere `--session`, `--remote` or `--machine`), and
starts the daemon when none runs. It opens no listener of its own.

An extension starts it when the editor opens a folder and stops it by
closing its standard input. Closing the editor stops the bridge, never an
agent: everything it starts runs in the daemon.

The first request is `initialize`:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
  "client":{"name":"vscode","version":"1.9.0"},
  "folders":["/home/me/src/shop"]}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{
  "protocol":1,"server":{"name":"hive","version":"1.12.0"},
  "daemon":{"version":"1.12.0","api_level":19},
  "missing":[]}}
```

`protocol` is the editor protocol's version. Version 1 can gain methods,
notifications and optional fields in 1.x; a change of meaning needs
version 2. `missing` names the features the connected daemon is too old
for (`files`, `hunks`), which the editor hides. `folders` are the editor's
workspace folders: lists default to what concerns them.

### Requests

| Method | Params | Result |
|---|---|---|
| `initialize` | `{client, folders}` | `{protocol, server, daemon, missing}` |
| `ping` | `{}` | `{}` |
| `editor.schema` | `{}` | the protocol's schema, as `api.schema` describes the daemon's |
| `agent.list` | `{folder, all}` | `{agents}`: blocked first, then working, done, idle, exited |
| `agent.kinds` | `{}` | `{kinds}`: each agent kind and whether it is installed |
| `agent.start` | `{kind, folder, isolate, worktree, base, name, prompt}` | `{agent}` |
| `agent.prompt` | `{id, prompt}` | `{outcome}` |
| `agent.keys` | `{id, keys}` | `{}`: answers a blocked agent (`y`, `Enter`, `Escape`, …) |
| `agent.stop` | `{id}` | `{}` |
| `agent.read` | `{id, lines}` | `{lines, links}`: its screen as text, for editors without a panel |
| `task.list` | `{folder}` | `{plans}`: plans with their tasks, attention first |
| `task.approve` | `{plan, task, deny, note}` | `{}` |
| `terminal.open` | `{agent, cols, rows}` | `{view, cols, rows}`, then `terminal.screen` notifications |
| `terminal.input` | `{view, data}` | `{}` |
| `terminal.resize` | `{view, cols, rows}` | `{}` |
| `terminal.close` | `{view}` | `{}`: the agent keeps running |
| `link.resolve` | `{agent, text}` | `{links}`: `path:line[:col]` in text, as files on disk |
| `review.branches` | `{folder}` | `{branches}`: the branches of the folders' repositories |
| `review.diff` | `{ref, context}` | `Review`: files, each with its hunks and their verdicts |
| `review.file` | `{ref, path, side}` | the file at the branch's base (`base`) or as it is (`branch`) |
| `review.hunk` | `{ref, path, hunk, action, force}` | the file's `Review` after accepting or rejecting the hunk |
| `review.comment` | `{ref, path, line, body, send}` | `{branch}` |
| `review.send` | `{ref}` | `{branch}` |
| `review.ready` | `{ref}` | `{branch}`: the gates run |
| `review.diagnostics` | `{ref, folder}` | `{diagnostics}`: gate failures at their file and line |
| `file.list` | `{folder}` | `{files}`: files agents are changing |
| `file.check` | `{path}` | `{editing}`: the agents changing that file, if any |

A **prompt** is text and attachments, which the bridge turns into one
prompt, so every editor sends the same thing:

```json
{"text":"Why does this panic on an empty cart?",
 "attachments":[
   {"kind":"selection","path":"/home/me/src/shop/cart.go","start_line":40,"end_line":52,"text":"func Total(…"},
   {"kind":"diagnostic","path":"/home/me/src/shop/cart.go","start_line":44,"severity":"error","message":"index out of range","source":"go vet"},
   {"kind":"test","name":"TestTotalEmpty","path":"/home/me/src/shop/cart_test.go","start_line":12,"output":"--- FAIL: …"}]}
```

Paths become relative to the agent's directory when they are inside it,
so an isolated agent reads `cart.go:40-52` in its own worktree. A file
attachment (`kind: file`) carries its path, and its text only when the
editor has unsaved changes. Attachments are capped at 256 KiB in all; the
bridge cuts longer ones and says so in the prompt.

### Notifications

| Notification | Params | When |
|---|---|---|
| `agent.changed` | `{agent}` | an agent's state, name or task changes |
| `task.changed` | `{plan}` | a plan or one of its tasks changes |
| `branch.changed` | `{branch}` | a branch's files, gates, comments or reviews change |
| `file.changed` | `{files}` | the files agents are changing in the editor's folders (all of them, debounced) |
| `terminal.screen` | `{view, update, links}` | a terminal's lines changed |
| `terminal.ended` | `{view, reason}` | the view closed (the agent exited, or the daemon went away) |
| `notify` | `{kind, message, agent, plan, task, ref}` | an agent blocked or finished, a task waits for approval, gates finished |
| `daemon.state` | `{connected}` | the daemon went away, or came back |

`notify` carries what to open: the editor shows a notification whose
button opens the agent's terminal (or the review, for gates).

The bridge re-subscribes when the daemon restarts, sends `daemon.state`,
and the editor reloads its lists. A slow editor never holds the daemon up:
screens are coalesced per view (the latest lines win), and events past the
bridge's queue are dropped with a reload asked for.

### Terminals in the editor

`terminal.open` opens a view of the agent's terminal (`terminal.frames`),
applies its frames to a screen and sends `ScreenUpdate`s, the JSON the
desktop app draws: lines as runs of styled text, the cursor and the
terminal's modes. The encoder moves from the desktop app into
`internal/termview`, so both send the same updates.

The drawing code moves too. The desktop app's framework-free parts (the
screen model, colours, key and mouse encoding, and how a run is styled)
become `sdk/termview`, a TypeScript module with no dependencies. The
desktop app imports it, unchanged in behaviour, and its tests still pass.
`sdk/termview` also has a panel, a small page that draws a terminal into
any web view and types into it: VS Code's webview and the JetBrains IDEs'
JCEF browser load the same file. Neovim draws the same updates into a
buffer with highlights; Zed reads agents' screens through
`hive editor lsp` and opens a full terminal with `hive agent attach`.

`links` in each update are the `path:line[:col]` references the bridge
found in the changed lines and resolved against the agent's directory:
only paths that exist are links. Clicking one opens the file at the line.
OSC 8 hyperlinks an agent prints are links as well.

### Review

`review.diff` reads `branch.diff` and parses its patch into files and
hunks. A hunk's ID is a hash of its file and its lines (not its position),
so it stays the same while other hunks change, and is computed by
`internal/git` for the bridge and the daemon alike. Each hunk carries the
verdict recorded for it, if any.

The editor shows each file in its own diff view: `review.file` gives the
base side, and the branch side is the file in the agent's worktree, which
the person can open, read and edit like any other.

- **Comments** go to `branch.comment` with the file and line, and are sent
  to the agent as one prompt (`send`), as `hive branch comment --send`
  does.
- **Gate results** become diagnostics: `review.diagnostics` reads each
  failed gate's output for `file:line[:col]: message` (Go, TypeScript,
  Rust, Python and most compilers and linters print this) and returns the
  ones in the branch's files. A gate whose output names no line is one
  diagnostic on the branch.
- **Hunks** are accepted or rejected with the new daemon method
  `branch.hunk`:

| Method | Params | Result |
|---|---|---|
| `branch.hunk` | `{ref, path, hunk, action, context, force, by}` | `HunkResult`: the branch and the verdict |

`action: "reject"` takes the hunk out of the agent's worktree: the daemon
reads the branch's diff of that file again, finds the hunk by its ID, and
applies it in reverse with `git apply -R` in the worktree. A hunk that
changed since the editor read it is not found, and the request fails with
`conflict`, so a person never rejects what they did not see. The daemon
refuses while an agent works or waits on a person in the worktree, as
`branch.update` does, unless `force` is set; the editor asks first.
`action: "accept"` records that a person accepted the hunk and changes no
file. Verdicts are kept on the branch (`Branch.reviews`, at most 500,
the oldest dropped), so every editor and `hive branch get` see them.

Neither action touches the person's own branch. Bringing an agent's work
there stays the person's merge (`hive branch update`, a pull request, or
git), as M23 decided.

### Agents' edits

The new daemon method `agent.files` lists the files agents are changing,
and the `agent.files` event says when that list changes for a directory:

| Method | Params | Result |
|---|---|---|
| `agent.files` | `{environment_id, dir}` | `{files}`: path, its worktree, agent, kind, name, pane, state, since, last |

While an agent works, the daemon reads its worktree's changed files (`git
diff --name-only HEAD` and the untracked ones) every second and notes each
file's size and modification time. A file that appears, changes or
disappears while an agent works there is attributed to that agent (to
each of them, when several work in the same worktree). An attribution
lasts while the file differs from `HEAD` and the agent runs; it is dropped
when the file is clean again or the agent exits. Nothing is polled while no
agent works, and the list is not stored: a restarted daemon starts it
again.

The bridge joins it with tasks, and the editor marks those files in its
file tree and tab bar (the agent's name, its kind and its task), and
warns before the person changes one while the agent works or waits on
them: VS Code asks on the first edit and offers to undo it, JetBrains asks
before the file becomes writable, Neovim asks on the first change.

A person editing a file in the same worktree while an agent works there
may see their own change attributed to the agent. Hive cannot tell two
writers apart from the file alone; the warning is cautious by design.

### `hive editor lsp`

The same bridge as a language server (LSP 3.17, `Content-Length`
framing): `initialize` with the workspace folders, `publishDiagnostics`
for gate failures (errors) and files agents are changing (one hint on the
first line naming the agent), code actions on a selection or diagnostic
(`Hive: ask <agent>`, `Hive: start <kind> on this`), and
`window/showMessage` for blocked agents. `workspace/executeCommand` runs
`hive.prompt`, `hive.start` and `hive.agents`. The Zed extension starts it
for every language, offers `hive mcp` as a context server, and adds
`/hive-agents` and `/hive-diff` slash commands.

### Extensions

| Editor | Where | Language | Released to |
|---|---|---|---|
| VS Code (and VSCodium, Cursor, Windsurf) | `editors/vscode` | TypeScript | VS Code Marketplace, Open VSX |
| IntelliJ IDEA, GoLand, PyCharm, WebStorm, … (2024.2 and newer) | `editors/jetbrains` | Kotlin | JetBrains Marketplace |
| Neovim 0.10 and newer | `editors/nvim` | Lua | its own repository, by tag |
| Zed | `editors/zed` | Rust (WebAssembly) | Zed's extension registry |

Each finds `hive` on the `PATH` (or the path set in its settings), checks
that it speaks protocol 1, and says how to install or update Hive when it
does not.

### Distribution

The release workflow builds every extension from the tag, with the tag's
version: the `.vsix`, the JetBrains plugin's zip, the Neovim plugin's
tarball and the Zed extension's source archive. They are attached to the
GitHub release beside Hive's archives, listed in a signed
`editors-checksums.txt`, and attested with build provenance (M20).
Publishing to the marketplaces runs from the same job, after the
attestation, when the repository's `HIVE_EDITORS_RELEASE` variable is set;
the Neovim plugin's repository is updated by tag, and the Zed registry by a
pull request.

## Compatibility

Additions only.

- API level 19 adds `agent.files`, the `agent.files` event and
  `branch.hunk`. `Branch` gains `reviews`, which older clients ignore.
- `branches.json` keeps schema 1: `reviews` is a new optional field, which
  an older daemon reading the file drops (losing the verdicts, not the
  branch).
- A bridge connected to a daemon below level 19 reports `files` and
  `hunks` as `missing`, and the editor hides those features; everything
  else works with level 13 (branches) and newer.
- The editor protocol has its own version (1), frozen in
  `api/v1/editor.txt` like the daemon's API: methods, notifications and
  fields may be added, never removed or changed in 1.x. Extensions check
  it at `initialize`.
- The desktop app draws exactly what it drew: the move to `sdk/termview`
  and `internal/termview` keeps its types, and its tests and the encoder's
  tests pin the format.

## Security and privacy

- The bridge is a client of the local daemon over its socket, which checks
  the caller's user ID (peer credentials) as for every client. It opens no
  network listener and adds no token, so it reaches nothing a `hive`
  command on the same account cannot.
- An extension runs the bridge with the person's own account, from the
  `hive` they installed. Extensions do not download binaries.
- `agent.files` reveals only paths inside environments, to clients that can
  already list them. It needs `hive:read` from plugins, and the owner role
  from members.
- `branch.hunk` changes files in an agent's worktree and nowhere else. It
  needs `hive:process` from plugins (like `branch.update`) and the owner
  role from members. It never touches the person's branch.
- Attachments are what the person chose to send, and go only to the agent
  they chose. The bridge reads no file the editor did not name.
- Links in an agent's output are opened only when they resolve to existing
  files, and only by a person's click; `file:` and `http(s):` OSC 8 links
  are opened by the editor's own handler, which asks for anything else.
- Webviews load only the extension's own files, under a content security
  policy that allows no network.

[docs/threat-model.md](../docs/threat-model.md) gains a row for editor
extensions.

## Testing

- Unit tests for the bridge against a daemon in the test: ordering,
  prompts and attachments, links, the diff parser and hunk IDs (fuzzed),
  gate output parsing, coalescing screens, reconnecting.
- Unit tests for `branch.hunk` (accept, reject, a changed hunk refused, a
  working agent refused, new and deleted files) and `agent.files`
  (attribution, two agents, cleaning, exiting).
- `TestEditorAPIFrozen` freezes the editor protocol, and the existing
  frozen tests freeze the new daemon methods.
- `TestM31EditorBridge`: the acceptance through `hive editor serve` and a
  real daemon: an agent started from a selection, its terminal followed,
  its diff reviewed hunk by hunk with a comment sent back, a warning for a
  file it changes, and every agent still running once the bridge exits.
- Each extension's own tests: VS Code's with its test runner against a
  fake bridge, the JetBrains plugin's with the IntelliJ Platform's test
  framework, Neovim's headless with a fake bridge, Zed's with `cargo test`.
- CI builds every extension on each change to `editors/`, `sdk/termview`
  or the bridge.

## Alternatives

- **Each extension speaks the daemon's API.** Four implementations of the
  same logic, and four clients that break at every API change.
- **The bridge as a local HTTP or WebSocket server.** It would be a network
  listener with a token to guard, which `hive editor serve` avoids; stdio
  is how editors already run language servers.
- **Only `hive editor lsp`.** LSP has no place for terminals, side panels or
  hunk-by-hunk review, so the richer editors would lose most of M31.
- **xterm.js in the webviews.** It would draw differently from the desktop
  app, and decode VT a second time, when the daemon already sends frames.
- **Staging accepted hunks.** Accepting would then change the agent's index
  under it, and committed hunks could not be staged at all. A recorded
  verdict says the same thing to every client and touches nothing.

## Open questions

1. Should a rejected hunk tell the agent, as a prompt, what was taken out?
   (M32's undo raises the same question.)
2. Should the bridge offer the person's own unsaved buffers to agents that
   ask for them, through the editor?

## Delivery

Each step is a pull request that links this RFC:

1. `agent.files`, `branch.hunk` and API level 19; `internal/termview` and
   `sdk/termview`, with the desktop app on them; `hive editor serve` and
   `hive editor lsp`, frozen; the acceptance test.
2. The VS Code extension.
3. The JetBrains plugin and the Neovim plugin.
4. The Zed extension, `docs/editors.md`, and the release workflow.

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
