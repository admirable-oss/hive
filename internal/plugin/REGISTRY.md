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
The current manifest has no structured per-plugin settings schema or
user-config directory declaration. Plugins receive a persistent
`HIVE_PLUGIN_DATA` directory and can declare required executables. Structured
configuration, data-location declarations, and version-to-version migrations
remain future contract work, so plugins must perform their own backwards
compatible data handling today.
