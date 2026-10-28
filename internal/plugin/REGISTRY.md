# Hive plugin registry format

The CLI reads a UTF-8 JSON document from
`https://hive.admir-saheta.com/plugins/registry.json`. Set
`HIVE_PLUGIN_REGISTRY_URL` to point at a development or self-hosted registry.
The response is limited to 4 MiB and must use schema version `1`.

Each catalog record has a stable plugin ID, maintainer and source links,
compatibility metadata, and one or more immutable releases. A release includes
its display version, a Git ref, the full 40-character commit expected at that
ref, and a SHA-256 digest of `git archive --format=tar <commit>`. The CLI checks
the manifest ID and version, verifies the source commit and archive digest, and
rejects source archives larger than 64 MiB before showing the install preview.
If a tag is moved after publication, the registry entry fails closed.

The top-level shape is:

```json
{
  "schema": 1,
  "plugins": [
    {
      "id": "pr-status",
      "name": "Pull request status",
      "description": "Shows pull request state in Hive.",
      "screenshots": ["https://example.org/pr-status.png"],
      "installation": "Install by ID with `hive plugin install pr-status`.",
      "configuration": "Set `provider` to the Git hosting provider in the plugin settings.",
      "categories": ["developer-tools", "notifications"],
      "maintainers": [{"name": "Example Maintainer", "url": "https://example.org"}],
      "license": "MIT",
      "repository": "https://github.com/example/hive-pr-status",
      "documentation": "https://example.org/hive-pr-status",
      "issues": "https://github.com/example/hive-pr-status/issues",
      "platforms": ["darwin", "linux"],
      "min_hive_version": "1.0.0",
      "plugin_api": "1",
      "official": false,
      "verified": false,
      "releases": [{
        "version": "1.0.0",
        "ref": "v1.0.0",
        "commit": "0123456789abcdef0123456789abcdef01234567",
        "checksum": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "subdir": "plugins/official/pr-status",
        "changelog": "Initial release.",
        "published_at": "2026-10-14T00:00:00Z"
      }]
    }
  ]
}
```

The record above is illustrative and is not a published Hive listing. Release
records are append-only: publishing a new version adds a record rather than
changing the source revision or timestamp of an existing release. `subdir` is
optional and selects a package directory from a monorepo; the checksum still
covers the full `git archive` at the pinned commit. Public listing changes are
validated by the `plugin-submissions` GitHub workflow. Changes to official or
verified listings require the `plugin-registry-trusted-review` environment
approval configured on the Hive repositories. The contribution process,
submission states, review history, reports, appeals, and withdrawal rules are
documented in [`docs/plugins/governance.md`](../../docs/plugins/governance.md).

`hive plugin search [query] [--category name]` searches IDs, names,
descriptions, and categories. `hive plugin info <id>` shows catalog metadata
when the ID is not installed. `hive plugin install <id>` resolves the newest
release, displays the repository, pinned revision, and manifest commands, and
asks before installation. Git URLs and paths continue to use the existing
installation flow; use `./path` when a directory name could also be a plugin
ID. `hive plugin link` remains the development workflow.

`hive plugin update [id]` updates one catalog install or all catalog installs.
It verifies the installed version, commit, and checksum against a release still
present in the registry, then fetches the newest immutable release and checks
its archive checksum and manifest before asking for confirmation. An update
retains the prior installed release; `hive plugin rollback <id>` restores it.
Only the most recently replaced release is guaranteed to be retained. Plugin
data is stored separately and is not moved, reset, or removed by update or
rollback. Interrupted swaps are journaled and repaired on the next store
access; malformed journals fail closed for manual repair.

Catalog commands require the registry. Installed plugins, Git installation,
local linking, and runtime operation do not depend on registry availability.
An update needs the registry to verify both the installed release record and
the candidate release, so it fails with an actionable registry error while
offline without changing the active plugin.

The current package transport uses Git's archive stream rather than extracting
an arbitrary downloaded archive. Registry responses are capped at 4 MiB and
the generated source archive is capped at 64 MiB. These caps apply before
install approval; incomplete fetches are discarded and never become active.

## Schema 2 (Hive 1.7)

Schema 2 is a document of its own, `registry-2.json`, beside `registry.json`.
Hive 1.7 and later read it first and fall back to `registry.json` only when
the registry answers 404 (or 410) for it; any other failure is an error, so
nothing between Hive and the registry can make it read the catalog without
signatures. Earlier releases never read `registry-2.json`, and
`registry.json` gains no fields. A self-hosted registry can stay on schema 1.
`HIVE_PLUGIN_REGISTRY_URL` may also point at a `registry-2.json` directly.

Schema 2 adds:

- **`publishers`**: who publishes, with an `id`, a `name`, an optional HTTPS
  `url`, a `tier` (`official`, `verified` or `community`) and the ed25519
  SSH `keys` their releases are signed with (`key`, an authorized_keys
  line; `fingerprint`, checked against the key when present; `added_at`).
- **`publisher`** on each listing, naming one of them. A listing's tier is
  its publisher's; `official` and `verified` are derived from it.
- Per release: **`plugin_api`** and **`min_hive_version`** (a release this
  hive cannot run is never picked), **`signature`**, **`provenance`**
  (`builder`, and HTTPS `repository`, `run` and `attestation` links, with
  the `workflow` path) and **`tested`** (`hive`, `result` pass or fail, `at`,
  an optional HTTPS `report`).
- **Per-plugin release refs**: a ref may end in the version
  (`pr-status/v1.4.0`), so plugins sharing a repository tag their releases
  apart.

```json
{
  "schema": 2,
  "publishers": [{
    "id": "acme", "name": "Acme Inc.", "url": "https://acme.example", "tier": "verified",
    "keys": [{"key": "ssh-ed25519 AAAAC3Nza...", "fingerprint": "SHA256:3b6...", "added_at": "2026-11-02T00:00:00Z"}]
  }],
  "plugins": [{
    "id": "acme-deploy", "publisher": "acme", "name": "Acme deploy",
    "repository": "https://github.com/acme/hive-deploy", "license": "MIT",
    "maintainers": [{"name": "Acme"}],
    "releases": [{
      "version": "1.4.0", "ref": "acme-deploy/v1.4.0",
      "commit": "0123456789abcdef0123456789abcdef01234567",
      "checksum": "sha256:aaaa...", "plugin_api": 1, "min_hive_version": "1.7.0",
      "published_at": "2026-11-10T09:00:00Z",
      "signature": "-----BEGIN SSH SIGNATURE-----\n...\n-----END SSH SIGNATURE-----\n",
      "provenance": {"builder": "github-actions", "repository": "https://github.com/acme/hive-deploy", "workflow": ".github/workflows/release.yml", "run": "https://github.com/acme/hive-deploy/actions/runs/123"},
      "tested": [{"hive": "1.7.0", "result": "pass", "at": "2026-11-10T10:00:00Z"}]
    }]
  }]
}
```

**Signatures.** A release's signature is an SSH signature in the
`hive-plugin-release` namespace over its *release statement*: the JSON of
`{checksum, commit, id, plugin_api, published_at, ref, version}` with keys in
that (sorted) order, no spaces, the checksum and commit in lower case and
`published_at` in UTC to the second (RFC 3339):

```sh
printf '%s' '{"checksum":"sha256:…","commit":"0123…","id":"acme-deploy","plugin_api":1,"published_at":"2026-11-10T09:00:00Z","ref":"acme-deploy/v1.4.0","version":"1.4.0"}' |
  ssh-keygen -Y sign -n hive-plugin-release -f ~/.ssh/id_ed25519
```

Hive checks it against the listing's publisher's keys when it reads the
catalog. A release with no signature is listed as unsigned; one whose
signature does not verify is never installed (unless `[plugins]
require_signature = "none"`). `[plugins] require_signature` decides what
installs: `official-verified` (the default) needs official and verified
plugins signed and warns about unsigned community ones, `all` needs every
release signed (and refuses a schema 1 catalog, which has no signatures), and
`none` checks nothing. An update must keep the installed plugin's publisher,
and a signed release is never followed by an unsigned one.

**Key revocations.** `revocations.json` gains `keys`: `fingerprint`,
`reason`, an optional HTTPS `advisory`, and `since`. Every listed release
signed by that key and published at or after `since` is revoked; earlier
ones are not. Hive before 1.7 ignores the field.

```json
{"schema": 1, "releases": [], "keys": [{"fingerprint": "SHA256:3b6...", "reason": "the key leaked", "since": "2026-11-15T00:00:00Z"}]}
```

**Tooling.** `tools/plugin-registry` builds `registry-2.json` from the
registry directory (`official-listings.json`, `official-releases.json`,
`official-signatures.json`, `publishers.json`, `compat-results.json` and the
approved submissions that name a publisher), checks it with the same parser
Hive uses (`plugin.ParseCatalog`), signs official releases, and records
compatibility results. `verify` runs on every registry pull request.
Publishing a release is `hive plugin publish`
([docs/plugins/publishing.md](../../docs/plugins/publishing.md)).

## Manifest configuration and migration contract

Manifests may bound compatibility with both `min_hive_version` and
`max_hive_version`. The range is inclusive; a reversed range is invalid.
`data_directory` selects a relative subdirectory beneath Hive's persistent
per-plugin data root. Empty or `.` keeps the existing root unchanged. Absolute
paths, parent traversal, and backslashes are rejected.

`[configuration]` declares a versioned settings file, stored outside the
plugin package and separate from plugin data. Hive exposes it to every plugin
command as `HIVE_PLUGIN_CONFIG`, with its containing directory in
`HIVE_PLUGIN_CONFIG_DIR`. The file uses this shape:

```json
{
  "schema_version": 1,
  "values": { "endpoint": "https://example.test" }
}
```

Settings support `string`, `boolean`, `integer`, and `number`, with optional
defaults and required values. Hive writes default settings with private file
permissions and validates types, required values, and undeclared keys before
running plugin commands. Settings are plaintext; credentials and secrets must
be stored with an external secret manager instead.

```toml
data_directory = "state"

[configuration]
version = 2
directory = "profile"

[[configuration.settings]]
name = "endpoint"
type = "string"
description = "Service base URL"
default = "https://example.test"
required = true

[[configuration.settings]]
name = "enabled"
type = "boolean"
default = true

[[migrations]]
from = 1
to = 2
command = ["./migrate-settings.sh"]
```

Each migration advances exactly one schema version, and every version step
after version 1 must be declared. Hive stages the settings file, runs the hook
with `HIVE_PLUGIN_CONFIG` pointing at the staged file, and commits it only
after the command succeeds and leaves valid JSON. The hook must leave
`schema_version` unchanged; Hive advances it after success. Hooks also receive
`HIVE_PLUGIN_CONFIG_FROM_VERSION` and `HIVE_PLUGIN_CONFIG_TO_VERSION`. A failed
hook leaves the prior settings file intact and prevents that plugin from
starting. Hooks should be idempotent because an interrupted process may be
retried. Keep previous-version setting keys readable while a rollback release
is retained; the older release ignores unknown keys from a newer schema.

`HIVE_PLUGIN_DATA` remains a separate persistent directory and is never
automatically deleted or moved during updates or rollback. Migration hooks
must not destructively rewrite plugin data: the current transactional guarantee
covers the settings file, not arbitrary plugin-owned data. Plugins should keep
data formats backward-compatible across the retained rollback window.
