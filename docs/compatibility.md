# Compatibility matrix

What works with what across the 1.0 release train, what is deprecated, and
how state moves between versions. The promise itself, what 1.x will never
break, is the
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/);
this page records how the releases so far keep it. It is reviewed for every
release (see [GOVERNANCE.md](../GOVERNANCE.md#releases)).

## Releases and surfaces

Each milestone of the 1.0 train is planned as a `v1.0.x` release
([ROADMAP.md](../ROADMAP.md)). **Tagged so far:** `v1.0.0-rc.1`. M10 to M18
are merged on `main` but not tagged yet. Until they are, "1.0.x" below means
`main` at that milestone. The upgrade test (next section) runs from every
tag that exists.

| Release | Milestone | API level | Plugin API | Observer API | Discovery | Added surfaces |
|---|---|---|---|---|---|---|
| `v1.0.0-rc.1` (tagged) | M9 | 9 | 0 (manifests without `api`) | — | — | `debug.pprof`, `debug.stats`. The API is frozen in `api/v1/`. |
| `v1.0.1` | M10 | 9 | 0 | — | — | `hive plugin search / info / install <id> / update / rollback / validate / init`; registry schema 1 |
| `v1.0.2` | M12 | 9 | **1** (and 0) | — | — | Plugin API 1: capabilities, contributions, health, settings, `plugin.*` RPC |
| `v1.0.3` | M11 | 9 | 1 | — | — | Kitty graphics, image paste (`terminal.*` additions), `[images]` |
| `v1.0.4` | M13 | 9 | 1 | — | — | Windows (amd64); `hive update --rollback` on every system |
| `v1.0.5` | M14 | **10** | 1 | **1** | — | `observer.*`, the observer socket (`observe.*`), `api/v1/observer.txt` |
| `v1.0.6` | M15 | **11** | 1 | 1 | — | `observer.activity`; `hive web` |
| `v1.0.7` | M16 | 11 | 1 | 1 | — | Web Push and the installable app (web observer only) |
| `v1.0.8` | M17 | 11 | 1 | 1 | **1** | `hive machine discover / pair / trust / identity`, `[discovery]` |
| `v1.0.9` | M18 | 11 | 1 | 1 | 1 | `pkg/hiveapi` frames, input, status and layout (additions to `hiveapi.txt`); the desktop app (preview) |
| `v1.0.10` | M19 | 11 | 1 | 1 | 1 | Plugin release revocations (`revocations.json`); `hive plugin doctor` registry check |

**API level** (`protocol.APILevel`, in `hive version --json` and
`runtime.status`) goes up whenever the daemon gains a method or event. A
release that adds no wire surface keeps its level.

**The wire protocol** is protocol 2 (a multiplexed connection, with a
`hello`/`welcome` handshake) on every 1.x release. Protocol 1 requests (one
per connection) are still answered.

## Clients and daemons of different versions

A client is the CLI, the TUI, `hive mcp`, the desktop app, or a program using
`pkg/hiveapi`.

| Client | Daemon | What happens |
|---|---|---|
| Same API level | | Works. |
| Newer (higher API level) | Older | The client replaces the daemon on its next command when no agent can be lost (agents run under shims, so this is the usual case). Otherwise it says why, and `hive daemon restart` does it. The desktop app does the same. |
| Older (lower API level) | Newer | Works. Every method and field an older client uses still exists (the `api/v1/` freeze). The older client **does not** replace a newer daemon. |
| Any | A method it does not know | `unknown_method`, so a client can tell "too old" from "failed". |
| Remote (`--remote`, `--machine`) | Older remote | `hive --remote` offers to install or update hive there (after asking). |

**How it is tested.** `TestM19UpgradeFromEarlierReleases` (`cmd/hive`) builds
each earlier release and starts its daemon, an agent and a tab. It upgrades
with that release's own `hive update`, checks that the agent, its screen
and the tabs are still there, and runs the earlier CLI against the newer
daemon. Then it goes back. The nightly workflow runs it from every earlier
`v1.*` tag on Linux and macOS:

```sh
HIVE_UPGRADE_FROM="v1.0.0-rc.1" go test -run TestM19UpgradeFromEarlierReleases ./cmd/hive
make upgrade FROM=v1.0.0-rc.1      # the same
```

The other tests behind this table:

- `TestE2E_AnOlderDaemonIsReplacedWhenNoAgentCanBeLost` and
  `TestE2E_AnOlderDaemonWithAgentsAtRiskIsLeftAlone` (newer client, older
  daemon);
- `TestServe_RejectsOtherProtocolVersionsButKeepsServing` and
  `TestMux_Protocol1StillWorks` (protocol versions);
- the `Frozen` tests (removed or changed surfaces).

## Plugins

| Plugin declares | Runs on |
|---|---|
| no `api` (plugin API 0) | Every 1.x release. It holds every capability and needs no approval; `hive plugin doctor` warns about it (deprecated, see below). |
| `api = 1` | `v1.0.2` and later. Earlier releases refuse it (`unsupported plugin API`). |
| `min_hive_version` / `max_hive_version` | Enforced on install, update and load, by the CLI and the registry listing. |

Plugin API 1's contract is checked by `TestAPIContract` against the fixtures
in `sdk/protocol/fixtures/v1/` for every API version in `SupportedAPIs`.

**The registry.** The catalog (`registry.json`) is schema 1. Releases before
`v1.0.10` reject a catalog with fields they do not know. So the published
catalog **must not add fields** while any release before `v1.0.10` is
supported. From `v1.0.10`, unknown fields are ignored. Revocations are a
separate document (`revocations.json`, beside the catalog) for the same
reason: earlier releases never read it, and the catalog they read is
unchanged. See [docs/plugins/ecosystem.md](plugins/ecosystem.md#revoking-a-release).

## On-disk state

Every state file below is schema 1 in every 1.0.x release, so a rollback
within 1.0.x reads what a later 1.0.x wrote. `TestM19UpgradeFromEarlierReleases`
checks this both ways.

| File (in `HIVE_HOME`) | Schema | A newer schema | Unreadable |
|---|---|---|---|
| `session.json`, `snapshots/` | 1 | skipped (an older snapshot is used) | moved to `*.corrupt-<time>`; the newest readable snapshot is used |
| `environments/<env>/environment.json` | 1 (schema 0 migrated on read) | — | that environment is skipped |
| `environments/<env>/layout.json` | 1 | — | moved aside and rebuilt from the snapshot |
| `plugins/registry.json` | 1 | refused, file left as is (`v1.0.10`) | refused with its path |
| `plugins/.transaction.json` | 1 | refused | refused: "manual repair required" |
| `plugins/.config/<id>/settings.json` | the plugin's `configuration.version` | migrated by the plugin's declared steps | reported by `hive plugin doctor` |
| `observers/credentials.json` | 1 | refused, file left as is (`v1.0.10`) | refused with its path |
| `machines.json` | 1 | refused, file left as is (`v1.0.10`) | refused with its path |
| `identity.json` | 1 | refused, file left as is (`v1.0.10`) | refused (`ErrBadIdentity`); never replaced by a new identity |
| `web/access.json`, `web/runtimes.json`, `web/push.json` | 1 | refused, file left as is (`v1.0.10`) | refused |

"Refused" means the command fails, names the file, and does not write it,
so a newer hive's data is never overwritten by an older one. Releases
before `v1.0.10` do not have this check for the files marked `v1.0.10`. It
has not mattered yet, because no schema has changed.

**When a schema changes** (a 1.x minor release, never a patch), the release:

1. reads the old schema and migrates it on read, writing the new one;
2. raises the number, so older releases refuse the file instead of
   misreading it;
3. says so in the changelog and in this table.

[docs/upgrading.md](upgrading.md#going-back) covers going back across such a
change.

## Deprecated

A deprecated surface keeps working for the rest of 1.x and warns when used.
It is removed only in 2.0, whose release notes list each removal and its
migration.

| Surface | Since | Replacement | How it warns |
|---|---|---|---|
| Config key `terminal.history_kb` | `v0.3` | `terminal.scrollback_mb` | `hive config validate` and every load: "is no longer used" |
| Plugin manifests without `api` (plugin API 0) | `v1.0.2` | `api = 1` with declared `[permissions]` | `hive plugin install`, `info` and `doctor` warn that it holds every capability |
| Registry listings marked `deprecated` or `archived` | `v1.0.1` | the listing's own replacement | from `v1.0.10`, `hive plugin install` and `update` warn, and `doctor` reports it |

No CLI command, alias or flag is deprecated. The aliases (`env`, `ps`, `ls`,
`rm`, `wt`, `server`, …) are permanent. Protocol 1 requests (one request per
connection, as `nc` sends them) are still answered and are not deprecated.

## Migration paths

- **Configuration.** Keys are never renamed silently. A replaced key is still
  read and warns, naming its replacement. `hive config validate` lists
  every warning. `hive config import-herdr` migrates from herdr.
- **Plugin manifests.** API 0 manifests keep loading. To move to API 1, add
  `api = 1` and declare `[permissions]`; `hive plugin validate` checks the
  result. See [docs/plugins/api.md](plugins/api.md).
- **Plugin settings.** A plugin declares `configuration.version` and
  migration steps. Hive runs them in order on update, and keeps the previous
  settings for rollback.
- **On-disk state.** Migrated on read, as above. Nothing to do by hand.
