# Plugin ecosystem health

Policies for plugins that stop being maintained, are compromised,
deprecated or removed. How a release is revoked. What official plugins
promise, and what maintainers should do about compatibility, security and
versioning. Submission and review are in [governance.md](governance.md).
The plugin API is in [api.md](api.md).

## What users can rely on

- **Installed plugins keep working when the registry does not.** An outage
  stops search, install-by-ID and update. It never stops installed, linked
  or Git-installed plugins (`TestM10CatalogBehaviorMatrix`).
- **Releases are immutable.** A release is a commit and a `git archive`
  checksum. Hive checks both, and the manifest's ID and version, before
  installing. A moved tag is refused.
- **An update can be undone.** `hive plugin rollback <id>` restores the
  previous release. A release the daemon cannot load is rolled back on its
  own.
- **Nothing a plugin stored is deleted without asking.** `hive plugin
  uninstall` keeps its settings and data unless you add `--delete-data`.
- **A revoked release is never installed or updated to**, and an installed
  one is reported (below).

## Listing states

A listing's state is shown by `hive plugin search` and `info`, and from
1.0.10 by `install`, `update` and `doctor`.

| State | Means | Hive does |
|---|---|---|
| **official** | Maintained by the Hive maintainers | — |
| **verified** | A human reviewer confirmed the maintainer's identity and the source | — |
| **community** | Listed after automated checks | — |
| **deprecated** | The maintainer recommends a replacement; fixes may still come | `install` and `update` warn; `doctor` warns |
| **archived** | No longer maintained; no fixes, security fixes included | `install` and `update` warn; `doctor` warns |
| **delisted** | Removed from the catalog (withdrawn, or removed by moderation) | Not found by search or install-by-ID; `update` refuses; `doctor` warns that no fixes will come |
| **revoked** (a release) | The release must not be used | Refused by `install` and `update`; `update` moves off it; `doctor` fails |

## Policies

### Abandoned plugins

A plugin is abandoned when its maintainers do not answer a report or a
compatibility break for 90 days, or say they have stopped.

1. A registry maintainer opens an issue on the plugin's repository and in
   this repository, and records it in the submission history.
2. After 30 more days without an answer, the listing is marked `archived`.
   Its releases stay listed and installable, with a warning.
3. Someone who wants to take it over asks through an ownership transfer
   ([governance.md](governance.md#ownership-and-trusted-listings)). Without
   the old maintainers, the maintainer council can approve it after
   recording why.

An archived plugin that breaks with a new Hive release keeps its
`max_hive_version`, so a newer Hive refuses it rather than running it
broken.

### Compromised plugins

A plugin is compromised when a release, its repository or a maintainer's
account is controlled by someone else, or a release does something its
manifest does not declare.

1. **Report it privately** through
   [GitHub Security Advisories](https://github.com/admirable-oss/hive/security/advisories/new),
   not a public issue.
2. A registry maintainer **revokes every affected release** at once (below),
   without waiting for the plugin's maintainers, and delists the plugin if
   no release is safe.
3. The plugin's maintainers, once back in control, publish a fixed release
   under a new version. A revoked version is never reused.
4. An advisory describes what the release did, who is affected, and what to
   do. Users see it in `hive plugin doctor` and `hive plugin update`.

Hive cannot reach into machines. A compromised release that is already
installed keeps its files until the user acts. Revocation makes every
`doctor` and `update` say so, and `update` moves to a safe release.

### Deprecated plugins

A maintainer marks a listing `deprecated` (a pull request to the listing)
and names the replacement in its description. Deprecated plugins stay
installable. A deprecation is not a reason to revoke.

### Removed plugins

A listing is removed (delisted) when its maintainer withdraws it, or
moderation removes it for a policy violation
([governance.md](governance.md#reports-appeals-withdrawal)). The submission
history records why. Its releases stay in the registry's history: nothing
is rewritten. Removal for a security reason comes with revocation; removal
for any other reason does not stop installed copies.

## Revoking a release

Revocations are a document of their own, `registry/revocations.json`,
published beside the catalog (`/plugins/revocations.json` on the website):

```json
{
  "schema": 1,
  "releases": [
    {
      "id": "example-plugin",
      "version": "1.2.0",
      "reason": "sends the GitHub token to a third party",
      "advisory": "https://github.com/owner/example-plugin/security/advisories/GHSA-xxxx",
      "revoked_at": "2026-10-20T12:00:00Z"
    }
  ]
}
```

- **Every field but `advisory` is required.** The advisory must be an HTTPS
  link. Records are only ever added; a revocation is not undone.
- **Hive 1.0.10 and later** read it with the catalog. They refuse revoked
  releases, take the newest release that is not revoked, and report an
  installed revoked release. A registry with no revocations document (404)
  has revoked nothing. One that cannot be read stops installs and updates
  rather than risk a revoked release.
- **Releases before 1.0.10 do not read it.** They read only the catalog, and
  reject fields they do not know, which is why revocations are not a catalog
  field. To protect them too, a release revoked for a security reason is also
  removed from the catalog's `releases` until a fixed release is newer.
  Those releases then cannot install it, and their `update` refuses to
  verify it. The revocation record keeps its history.
- A change to `revocations.json` is a pull request like any registry change.
  For a compromised release, a registry maintainer may merge it without the
  plugin maintainers' approval. The website picks it up through the
  official store sync.

## Official plugins

Official plugins are maintained by the Hive maintainers in this repository
(`plugins/official/`). Each must have, and the registry review checks:

- `maintainers` naming Admirable OSS, and an `issues` link to this
  repository;
- a plugin API (`api = 1`) and a `min_hive_version`, kept current with each
  Hive minor release;
- tests in this repository's CI;
- security reports through this repository's advisories, under
  [SECURITY.md](../../SECURITY.md).

**Checked for 1.0.10:** `auto-tab-title` and `hive-explorer` both name Admirable
OSS as maintainer, link this repository's issues, declare plugin API 1 and
`min_hive_version` 1.0.0. Neither is `verified`, which is reserved for
community plugins a human has reviewed. Their release tags share this
repository's tag namespace
([security review, open item A](../security-review.md#open-items)); they move
to a repository of their own before their next release.

## Registry health

- **Availability.** The catalog and revocations are static files on the Hive
  website. Hive bounds each fetch (15 seconds, 4 MiB) and refuses
  HTTPS-to-HTTP redirects.
- **Integrity.** Every catalog change is validated in CI: the registry
  validator in the website repository, and the submission checks here. Each
  release's commit and checksum are recomputed from the tag.
- **Compatibility failures and failed upgrades.** Hive sends no telemetry,
  so failures are measured where they surface: `hive plugin doctor` (which
  checks compatibility, permissions, settings, health and registry status),
  automatic rollbacks (in the plugin's log), and reports: the Plugin report
  issue template has a category for failed upgrades and compatibility
  breaks. Those reports are reviewed with each Hive release
  ([GOVERNANCE.md](../../GOVERNANCE.md#releases)).

## For plugin maintainers

**Compatibility**

- Declare `api = 1`. API 0 manifests hold every capability and are
  deprecated.
- Set `min_hive_version` to the oldest Hive you test with. Set
  `max_hive_version` only when you know a newer Hive breaks the plugin.
- Plugin API 1 only grows: context fields, events, RPC methods, capabilities
  and contribution kinds are added, never removed or changed. Ignore fields
  and events you do not know.
- When your settings change, raise `configuration.version` and declare the
  migration step. Hive runs the steps in order and keeps the previous
  settings for rollback.

**Versioning**

- Use semantic versioning. A release that needs a new capability, or
  changes what the plugin does with the user's data, is at least a minor
  release. Users see permission changes before they update.
- Tag `v<version>` and never move or delete a tag. The registry pins the
  commit, and a moved tag is refused.
- Never reuse a version, including a revoked one.

**Security disclosures**

- Publish a security policy on your repository (GitHub Security Advisories
  work well), and keep your maintainer contact in the listing current.
- When you learn of a vulnerability, fix it, release a new version, and ask
  for the affected releases to be revoked in the same pull request. Ask the
  Hive maintainers privately (Security Advisories on this repository) when
  it is serious or under attack.
- Protect release access: two-factor authentication on accounts that can
  push tags, and branch protection on the default branch.
