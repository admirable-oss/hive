# RFC 0006: Plugin ecosystem 2, richer and safer

- **Status:** Draft
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** plugin API (API 2, beside API 1), registry (schema 2, beside schema 1), wire API (`plugin.*`, `pane.*`, `issue.*`), CLI (`hive plugin publish`, `test`), config (`[plugins]`), on-disk state (installed plugins), workflows (plugin steps and triggers), branches (plugin forges), TUI, desktop app and web observer

## Summary

M26 grows what plugins can do and narrows what a bad one could do:

- **Registry schema 2.** Every release carries its publisher's signature
  and how it was built. Schema 2 is a document of its own, beside the
  schema 1 catalog, which older releases keep reading unchanged.
- **Isolation by default.** A plugin API 2 plugin runs sandboxed on macOS
  and Linux unless you allow otherwise. Its capabilities cover files,
  network and programs, not only calls to Hive. Plugins that need no
  process at all can be WebAssembly.
- **Plugin API 2.** Plugins contribute pane views, forges, issue trackers,
  workflow steps and workflow triggers. Plugin API 1 plugins keep working
  as they do today.
- **Publishing.** `hive plugin publish` tags, checksums, signs and submits
  a release in one step. Listed plugins are tested against each Hive
  release candidate before it ships.
- **Trust.** Every client shows whether a plugin is official, from a
  verified publisher, or community, and whether any of its releases was
  revoked.

## Motivation

Plugin API 1 (M12) and the store (M10, M19) gave Hive a working ecosystem,
with two gaps a reviewer found and plugin authors keep running into.

**What a plugin could do to you.** A plugin runs as you. Unless it asks
for strict isolation itself, it can read your files, run programs and
reach the network; its declared `filesystem.write`, `network` and
`process.exec` are shown to you, not enforced
([docs/plugins/api.md](../docs/plugins/api.md#isolation-and-the-security-boundary)).
The registry pins a commit and a checksum, so a moved tag is caught, but
nothing ties a release to the person who published it. A stolen
repository token can publish a release that looks like any other.

**What a plugin cannot do for you.** Contributions are cards, status
items, actions, palette entries and links. The things people ask for need
more:

- a pane that shows a dashboard, a review or a queue, not a terminal;
- GitHub Enterprise, Gitea, Bitbucket or Azure DevOps for branches and
  pull requests (M23 left `branch.Forges` open for this);
- Linear, Jira or GitHub issues as the source of a task;
- workflow steps with typed inputs and outputs, and triggers from outside
  Hive (M24 has only built-in triggers, and `plugin/action` steps that
  take free text).

**What publishing costs.** A release today is a tag, a `git archive`
checksum computed by hand and a pull request to the registry. Nothing
checks a plugin against the next Hive release before it ships; users find
out.

## Proposal

### Plugin API 2

A manifest declares `api = 2`. Hive supports API 1 and API 2 side by side
(`SupportedAPIs = [1, 2]`); everything API 1 has, API 2 has. API 2 changes
four things:

1. **Sandboxed by default** (see *Isolation*). An API 2 plugin that needs
   to run unconfined declares `isolation = "none"` with a reason, and you
   approve it like a capability.
2. **Capabilities are enforced outside Hive**, with finer scopes:

   ```toml
   [permissions]
   required = ["hive.read", "network"]
   optional = ["filesystem.read:workspace"]

   [permissions.network]
   hosts = ["api.linear.app:443"]

   [permissions.process]
   exec = ["git", "gh"]
   ```

   - `filesystem.read` is new: without it, a sandboxed plugin reads only
     its package, its data, config and cache, and system libraries.
     `filesystem.read:workspace` adds the directories of the
     environments it is shown.
   - `filesystem.write` and `filesystem.write:workspace` likewise.
   - `network` takes a list of `host:port`. An empty list means none.
   - `process.exec` takes a list of programs, found on `PATH` when the
     plugin starts.
3. **New contributions:** views, forges, trackers, steps and triggers,
   below.
4. **Typed calls.** Hive calls a contribution's command with one JSON
   document on stdin and reads one from stdout. API 1 actions keep their
   arguments and environment.

Plugin API 1 plugins run exactly as before, including under `[plugins]
isolation = "auto"`. The API 1 contract fixtures stay, and API 2 gets its
own in `sdk/protocol/fixtures/v2/`.

### Isolation

| | macOS | Linux | Windows |
|---|---|---|---|
| Sandbox | `sandbox-exec` (as today) | `bwrap` plus Landlock (5.13 and later) | none in 1.7 (see *Open questions*) |
| Files | profile rules | read-only binds, Landlock read and write rules | — |
| Network | denied; allowed hosts through Hive's proxy | `--unshare-net`; allowed hosts through Hive's proxy | — |
| Programs | `process-exec` limited to the plugin's runtime and `exec` list | Landlock `EXECUTE` limited the same way | — |
| Hive socket | the plugin's own only | the plugin's own only | — |

- **The network proxy.** A plugin with `network` hosts gets
  `HTTPS_PROXY` and `HTTP_PROXY` pointing at a proxy the daemon runs on a
  socket only that plugin can reach. The proxy allows `CONNECT` to the
  listed `host:port` and nothing else, and logs what it refused. It sees
  only host names, never the traffic inside TLS. A plugin granted plain
  `network` (no list) keeps direct network access, as today.
- **WebAssembly.** A `[[runtime]]` with `kind = "wasm"` runs a WASI module
  in the daemon, through [wazero](https://wazero.io) (pure Go, no cgo).
  The module sees its data directory and nothing else. It calls Hive
  through one host function with the same capability checks as the
  socket, and has no network and no processes. A Wasm plugin runs on every
  platform, Windows included, and needs no sandbox tool.

**`[plugins] isolation`** keeps its values and gains one:

| Value | API 1 plugins | API 2 plugins |
|---|---|---|
| `auto` (default) | sandboxed if they declare `strict` and a sandbox exists | sandboxed where a sandbox exists; unconfined only with an approved `isolation = "none"` |
| `standard` | never sandboxed | as `auto` (a setting cannot silently loosen API 2) |
| `strict` | always sandboxed; refused where none exists | always sandboxed; refused where none exists |
| `api2` (new) | refused | as `auto` |

Where no sandbox exists (Windows, or Linux without `bwrap`), an API 2
plugin that is not Wasm runs only after you approve it unconfined, once
per release, with the reason shown.

### Richer contributions

**Pane views.** A plugin draws a pane from data, never from code a client
runs:

```toml
[[contributes.views]]
id = "review-queue"
title = "Review queue"
command = ["./bin/queue", "view"]   # optional: called to draw it on open
```

- A view is a **view document**: a tree of at most 2,000 nodes of a few
  kinds: `text` (with a style: plain, muted, bold, code, ok, warn, error),
  `row`, `column`, `list`, `table`, `progress`, `badge`, `divider`,
  `button` (runs one of the plugin's actions) and `link`. No HTML, no
  scripts, no images in 1.7. The TUI draws it in cells, the web and the
  desktop app as their own widgets.
- The plugin sets a view with `plugin.view.set {view, doc}` and clears it
  with `plugin.view.clear`. `hive.write` is enough.
- A pane can show a view instead of a terminal: `hive pane new --view
  <plugin>/<view>`, and a layout's pane takes `view = "<plugin>/<view>"`.
  Such a pane has no process; it saves and restores like any pane.
- Buttons call the plugin's `[[actions]]` with `{view, node}`, so every
  click is something the plugin already declared.

**Forges.** A plugin forge is the M23 `branch.Forge` interface, as
commands:

```toml
[[contributes.forges]]
name = "gitea"
hosts = ["git.example.org", "*.gitea.io"]
command = ["./bin/forge"]           # gets {op, ...} on stdin
```

The daemon registers it in `branch.Forges` under `plugin:<id>/<name>`, and
calls the command for `pr.create`, `pr.view`, `pr.checks`, `pr.merge` and
`pr.comments`. A repository's forge is chosen as today: configuration
first, then a plugin forge whose `hosts` match the remote, then the
built-in ones. A plugin forge needs `network` for its hosts; `branch.push`
and `branch.pr` stay closed to plugins as callers.

**Issue trackers.** New, and small:

```toml
[[contributes.trackers]]
name = "linear"
command = ["./bin/linear"]          # ops: get, search, comment, close
```

- Wire: `issue.get {ref}`, `issue.search {tracker?, query, limit?}`,
  `issue.comment {ref, body}`, `issue.close {ref, comment?}`. A ref is
  `<tracker>:<key>`, such as `linear:HIV-42` or `github:owner/repo#42`.
  GitHub issues are built in, through the GitHub forge.
- `hive plan create --from-issue linear:HIV-42` starts a plan from an
  issue. A task can carry an `issue` ref; its pull request links it, and
  the task closes it when it merges if the plan says so.

**Workflow steps.**

```toml
[[contributes.steps]]
name = "deploy-preview"
command = ["./bin/deploy"]
timeout = "10m"

[contributes.steps.inputs]
environment = { type = "string", required = true }
wait = { type = "boolean", default = true }

[contributes.steps.outputs]
url = { type = "string" }
```

A workflow uses it as `uses = "<plugin>/deploy-preview"` with `with = {
... }`. Inputs are checked when the workflow loads; outputs become
`steps.<id>.outputs.*` for later steps. The run log shows the step like a
built-in one. `plugin/action` steps keep working.

**Workflow triggers.** A plugin declares a trigger and fires it:

```toml
[[contributes.triggers]]
name = "deploy-finished"
```

A workflow names it in `on`, with a filter on its payload:

```toml
[on.plugin]
trigger = "vercel/deploy-finished"
filter = { state = "error" }
```

The plugin calls `workflow.trigger {name, payload}` (`hive.write`). Hive
runs the workflows that name it, under the same trust rules as M24: a
shared workflow runs only once you trusted it.

### Registry schema 2

Schema 2 is a new document, `registry-2.json`, beside `registry.json`
(schema 1) on the same site. Hive 1.7 reads schema 2 and falls back to
schema 1 when schema 2 is missing (404); a registry that serves neither
fails as today. Self-hosted registries can stay on schema 1.

```json
{
  "schema": 2,
  "publishers": [{
    "id": "acme",
    "name": "Acme Inc.",
    "url": "https://acme.example",
    "tier": "verified",
    "keys": [{
      "key": "ssh-ed25519 AAAAC3Nza...",
      "fingerprint": "SHA256:3b6...",
      "added_at": "2026-11-02T00:00:00Z"
    }]
  }],
  "plugins": [{
    "id": "acme-deploy",
    "publisher": "acme",
    "name": "Acme deploy",
    "...": "every schema 1 listing field but official and verified",
    "releases": [{
      "version": "1.4.0",
      "ref": "v1.4.0",
      "commit": "0123456789abcdef0123456789abcdef01234567",
      "checksum": "sha256:aaaa...",
      "plugin_api": 2,
      "min_hive_version": "1.7.0",
      "published_at": "2026-11-10T09:00:00Z",
      "signature": "-----BEGIN SSH SIGNATURE-----\n...",
      "provenance": {
        "builder": "github-actions",
        "repository": "https://github.com/acme/hive-deploy",
        "workflow": ".github/workflows/release.yml",
        "run": "https://github.com/acme/hive-deploy/actions/runs/123",
        "attestation": "https://github.com/acme/hive-deploy/attestations/456"
      },
      "tested": [{"hive": "1.7.0", "result": "pass", "at": "2026-11-10T10:00:00Z"}]
    }]
  }]
}
```

- **Tiers.** A publisher is `official` (the Hive maintainers), `verified`
  (a reviewer confirmed who they are, as for `verified` today) or
  `community`. A listing's tier is its publisher's. Schema 1's
  `official` and `verified` are derived from it.
- **Signatures.** A release's signature is an SSH signature
  (`ssh-keygen -Y sign`, namespace `hive-plugin-release`) over the
  release statement: the canonical JSON of `{id, version, ref, commit,
  checksum, plugin_api, published_at}`. Hive verifies it against the
  publisher's keys before the install preview. The key a release was
  signed with is kept with the installed plugin.
- **Continuity.** An update must be signed by a key of the same
  publisher. A publisher changing keys adds the new one through a
  registry pull request signed with an old one, or through a reviewer.
- **Key revocation.** `revocations.json` gains `keys` (fingerprint,
  reason, and the time from which signatures are distrusted). Releases
  signed by that key after that time are revoked; earlier ones are not.
  Releases before 1.7 ignore the new field (they read revocations with
  unknown fields ignored, from 1.0.10).
- **Provenance** is recorded and shown, and linked to the builder's own
  attestation. Hive does not verify a GitHub attestation itself in 1.7;
  the registry's submission check does.
- **Unsigned releases.** Schema 2 lists them, for community plugins that
  have not moved yet, with no `signature`. `[plugins] require_signature`
  decides what Hive installs:
  - `official-verified` (default): official and verified plugins must be
    signed; community ones may not be, with a warning;
  - `all`: every release must be signed;
  - `none`: nothing is checked (development registries).
- **Schema 1 stays.** Every API 1 release is still published to
  `registry.json` with schema 1 fields only, so every release since 1.0.0
  keeps working. API 2 releases are published only in schema 2: older
  releases could not run them.

### Publishing

`hive plugin publish` runs in the plugin's repository:

1. Validates the manifest, and refuses a dirty tree or an existing tag
   pointing elsewhere.
2. Runs the plugin's tests (`[test] command`, new and optional) and
   `hive plugin validate`.
3. Tags `v<version>`, and pushes the tag when `--push` is given.
4. Computes the `git archive` checksum the registry will check.
5. Signs the release statement with your SSH key (`--key`, or
   `user.signingkey` from Git, through `ssh-agent` when the key is there).
6. Records provenance: in GitHub Actions, from the run's environment;
   locally, as `builder = "local"`.
7. Writes the release record, and with `--submit` opens the registry pull
   request through `gh`. `--dry-run` does everything but tag, push and
   submit.

A reusable GitHub Action (`admirable-oss/hive-plugin-publish`) runs it on
a tag, with the signing key from a secret.

**Compatibility automation.** When a Hive release candidate is tagged,
the registry's `plugin-compat` workflow installs each listed plugin's
newest release into that candidate in a sandbox, runs `hive plugin
validate`, `doctor` and the plugin's `[test] command`, and writes the
result into `tested`. A failing official plugin blocks the release. A
failing community plugin opens an issue on its repository, and its
listing shows the result. Locally, `hive plugin test --hive 1.7.0-rc.1`
runs the same checks against a downloaded release.

### Trust in every client

`plugin.list` and `plugin.info` gain `tier`, `publisher`, `signed_by`,
`provenance`, `sandbox` (`none`, `sandbox-exec`, `bwrap`, `wasm`) and
`revoked` (with the reason and advisory). The install records them;
`update` and `doctor` refresh them.

- **CLI:** `list`, `info`, `install` and `update` show the tier and the
  signer. A revoked release is shown in red, everywhere it appears.
- **TUI:** a Plugins view in the palette lists them with a tier badge,
  sandbox state and revocations, and opens a plugin's details.
- **Desktop:** a Plugins window with the same.
- **Web observer:** read-only, with `observe.plugins` for credentials that
  see the runtime.
- Each badge says what it means: *official* (maintained by Hive's
  maintainers), *verified publisher* (identity reviewed), *community*
  (automated checks only).

### Wire API (API level 16)

| Method | Params | Result |
|---|---|---|
| `plugin.view.set` | `{view, doc}` | `{}` |
| `plugin.view.clear` | `{view}` | `{}` |
| `plugin.views` | `{}` | `[]View` (every plugin's views and their documents) |
| `issue.get` | `{ref}` | `Issue` |
| `issue.search` | `{tracker?, query, limit?}` | `[]Issue` |
| `issue.comment` | `{ref, body}` | `{}` |
| `issue.close` | `{ref, comment?}` | `Issue` |
| `workflow.trigger` | `{name, payload}` | `{runs: []string}` |

- **Fields:** `pane.Spec.view`; `plugin.Status` gains the trust fields;
  `task` gains `issue`.
- **Events:** `plugin.view` (a view changed), `issue.changed`.
- **Plugins:** `hive.read` for `plugin.views`, `issue.get`,
  `issue.search`; `hive.write` for `plugin.view.*`, `issue.comment` and
  `workflow.trigger`; **closed:** `issue.close`, so a plugin cannot close
  another tracker's work.

### Configuration

```toml
[plugins]
isolation = "auto"                     # auto | standard | strict | api2
require_signature = "official-verified" # official-verified | all | none
```

## Compatibility

- **Plugins.** API 1 and API 0 plugins run as before; nothing they rely
  on changes. API 2 needs Hive 1.7, and earlier releases refuse it
  (`unsupported plugin API`) as they refuse unknown APIs today.
- **Registry.** `registry.json` keeps schema 1 and gains no fields, as
  [docs/compatibility.md](../docs/compatibility.md#plugins) requires.
  `registry-2.json` is new; earlier releases never read it.
  `revocations.json` gains `keys`, which 1.0.10 and later ignore.
- **Wire API.** Only additions; the API level is 16. An older client
  with a newer daemon sees terminal panes only (a view pane lists as a
  pane with no process). A newer client with an older daemon gets
  `unknown_method` for `plugin.view*`, `issue.*` and `workflow.trigger`,
  and shows no views or trust details.
- **On-disk state.** The installed-plugin record gains `tier`,
  `publisher`, `signed_by` and `provenance` (unknown fields are kept by
  earlier releases' readers). Layouts gain `view`; an earlier release
  restoring such a pane starts a shell in its place.
- **Config.** `isolation = "api2"` and `require_signature` are new; an
  earlier release reports them as unknown keys, as for every new key.

## Security and privacy

- **The sandbox is the boundary for API 2.** The threat model's plugin
  section ([docs/threat-model.md](../docs/threat-model.md)) moves from
  "install only plugins you trust" to "an API 2 plugin is confined to
  what you approved, on macOS and Linux". API 1 plugins keep the old
  wording. Reads remain wider than writes: a sandboxed plugin can read
  system files and its own package.
- **The proxy** sees which hosts a plugin connects to, never the content.
  Refusals are logged in the plugin's log, which stays local.
- **View documents** are data. Clients escape every string; a `link` must
  be `https:` and is opened only by a person's click; a `button` runs only
  the plugin's declared actions.
- **Signatures** prove which key published a release, not that the code
  is safe. A stolen signing key is answered by key revocation; the
  publisher's tier does not change what a plugin is allowed to do.
- **Forges and trackers** receive the data of the operations they serve
  (a pull request's title and body, an issue's comment), and nothing
  else. They need `network` for their hosts like any plugin.

## Testing

- **Contract:** API 2 fixtures in `sdk/protocol/fixtures/v2/`, checked by
  `TestAPIContract` alongside API 1's, which stay unchanged.
- **Sandbox:** on macOS and Linux CI, a hostile test plugin tries to
  write outside its directories, read `~/.ssh`, reach a host it did not
  list, run a program it did not list and reach the daemon's main socket;
  each must fail, and the same plugin with the grants must succeed.
- **Wasm:** a module calls Hive within and beyond its capabilities, on
  every platform including Windows.
- **Registry:** schema 2 parsing, signature verification with good, bad,
  revoked and rotated keys; fallback to schema 1; a pre-1.7 release
  (`TestM19UpgradeFromEarlierReleases`) reading the published schema 1
  catalog unchanged.
- **Contributions:** e2e for a view pane (set, draw in the TUI, click a
  button, restore), a plugin forge creating a pull request against a fake
  forge, a tracker starting a plan, and a workflow using a plugin step
  and a plugin trigger.
- **Publish:** `hive plugin publish --dry-run` on a fixture repository,
  and a full publish against a local registry.
- **Acceptance:** a community plugin published with signed provenance,
  installed sandboxed by default on macOS and Linux, contributing a view
  and a workflow step, tested against a release candidate; and the API 1
  official plugins still working.

## Alternatives

- **Sigstore (keyless) signatures** instead of SSH keys. They tie a
  release to a CI identity with no key to steal, but need Rekor and
  Fulcio at install time, which a self-hosted or offline registry cannot
  assume. SSH signatures need nothing but the catalog, and Hive's own
  releases already use them. Sigstore can be added as a second kind of
  signature later.
- **Changing schema 1 in place.** Releases before 1.0.10 reject unknown
  fields, so the catalog they read must not change.
- **HTML or a webview for pane views.** Rich, but it brings a browser
  engine's attack surface into the TUI's world and cannot draw in cells.
  A small document model draws everywhere and runs no plugin code in
  clients.
- **Making API 1 plugins sandboxed by default.** Existing plugins that
  reach the network or write files would break silently on upgrade. API 2
  is where the default changes, and `strict` already sandboxes API 1 for
  those who want it.
- **A network allowlist in the sandbox itself.** `sandbox-exec` filters
  addresses, not names, and `bwrap` cannot filter at all; a proxy works
  the same on both.

## Open questions

1. **Windows.** AppContainer or a restricted token for API 2 plugins, in
   1.7 or later? Until then, only Wasm plugins are confined there.
2. **Reads.** Should `filesystem.read` be required for the home
   directory too, making reads as narrow as writes? It breaks tools that
   read their own config.
3. **Views and images.** Kitty and iTerm images in the TUI make images
   possible; is it worth the size and parsing risk in 1.7?
4. **Publisher keys for official plugins.** One key for all official
   plugins, held like Hive's release key, or one per maintainer?
5. **Compatibility results.** Should a failing community plugin be hidden
   from search for that release, or only marked?

## Delivery

Each step is a pull request that links this RFC:

1. Trust data: `plugin.Status` and the installed record gain tier,
   publisher and revocation; every client shows them.
2. Registry schema 2 reading, signatures, key revocation and
   `require_signature`.
3. `hive plugin publish` and `hive plugin test`, the publish action and
   the `plugin-compat` workflow.
4. Plugin API 2 manifests, scoped capabilities, the sandbox by default,
   Landlock and the network proxy.
5. The Wasm runtime.
6. Views, forges, trackers, steps and triggers.

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
