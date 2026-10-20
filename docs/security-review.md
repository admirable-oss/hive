# Security review: v1.0.10

The release-quality review of the 1.0 train (M19), done on 2026-10-20 against
[docs/threat-model.md](threat-model.md). It covers each area M19 names. For
each one it records what was checked and how, what was found, and what
changed. Open items are tracked at the end. The next review is due with the
next minor release, or sooner after a security fix.

**Method.** Reading the code at each trust boundary, and the tests that pin
its behaviour. `govulncheck` on both Go modules, and `npm audit` on the
desktop frontend. The upgrade, revocation and fail-safe tests added in this
review. This was not a third-party audit.

## Findings

### Fixed in this review

| # | Area | Finding | Fix |
|---|---|---|---|
| 1 | Dependencies | `govulncheck` found **10 vulnerabilities in the Go standard library reachable from Hive's code** at go1.26.6. They are in `net/http`, HTTP/2, `crypto/tls`, `mime/multipart`, and `os` on Windows: GO-2026-6603, 6604, 6605, 6607, 6608, 6610, 6611, 6612, 6613 and 6617. They are reachable through the web observer's HTTP server, the registry and update clients, and Web Push. | `toolchain go1.26.9` in both modules' `go.mod`, so CI and release builds use the patched toolchain; `golang.org/x/net` v0.60.0. `govulncheck` now runs on every change, nightly (new advisories arrive without a code change), and in the desktop workflow. Dependabot opens updates for Go modules, npm and Actions. |
| 2 | Plugins | The registry had **no way to revoke a release**. A compromised release could only be removed from the catalog, which also erased its record. | `revocations.json` beside the catalog. Hive refuses to install or update to a revoked release, moves off one on `hive plugin update`, and `hive plugin doctor` fails on one. An unreadable revocations document fails closed. See [docs/plugins/ecosystem.md](plugins/ecosystem.md#revoking-a-release). |
| 3 | Plugins, compatibility | The CLI **rejected catalog fields it did not know**, so the registry schema could never grow without breaking every installed hive. | Unknown fields are ignored from 1.0.10. Publication-side validation stays strict. Revocations live in their own document, so older clients are unaffected. |
| 4 | Plugins | Deprecated and archived listings were **installed without a word**. | `install` and `update` warn; `doctor` reports them. |
| 5 | Updates | When an update's new daemon could not start, **the daemons stayed down** on the new binary. Agents survived under their shims, but nothing drove them. | `hive update` rolls the binary back and restarts the daemons on it (`TestM19UpdateRollsBackWhenTheNewDaemonCannotStart`). |
| 6 | Updates | **Pre-release ordering compared strings**, so `-rc.10` sorted before `-rc.9`, and the preview channel would have offered an older candidate. | Semantic-versioning precedence (`TestNewer`). |
| 7 | Updates | A Homebrew **cask** install was not recognised, so `hive update` would have replaced a binary Homebrew manages. A formula install was told to run `brew upgrade hive`, but the formula is `hive-agents`. | Casks are recognised, and the message names the right command (`TestManaged`). |
| 8 | Rollback | Observer credentials, machines, the identity, installed plugins and web state **ignored their schema number**. After a rollback, an older hive could misread a newer file and overwrite it. | Each refuses a newer schema, names the file and leaves it untouched (`jsonfile.CheckSchema`, one test per store). |
| 9 | Release integrity | **Lightweight tags made for plugin releases (`v0.1.0`, the official plugins' release, and `v0.2.0`) live in this repository, beside hive's own tags.** `make build` stamped builds of `main` as `v0.2.0-28-g…`, because `git describe --tags` picked the nearest one. Plugins requiring hive 1.0 would refuse such a build, and `hive update` would treat it as ancient. `release.yml` runs on any `v*` tag, so a plugin tag pushed by hand would have published hive binaries and moved the Homebrew formula. (Only `v1.0.0-rc.1` was ever released: the plugin tags were pushed by a workflow token, which starts no workflows.) | Builds take their version from annotated tags only (hive's releases are annotated; plugin tags are lightweight), and `release.yml` refuses a lightweight tag. |
| 10 | Tests | `TestShellAndInstall` and `TestM7RemoteInstallsAndRunsCommands` found a hive installed on the developer's machine through `PATH`, and failed. It was a hermeticity bug, not a product bug. | The fake remote logins get the system `PATH` only. |

### Reviewed, no change needed

| Area | What was checked | Result |
|---|---|---|
| Socket permissions, peer authentication | Sockets are 0600 in 0700 directories (`TestRuntime_PIDFileAndSocketPermissions`). `platform.CheckPeer` is applied on the control, observer, plugin and shim sockets and in the web server, with `SO_PEERCRED`, `LOCAL_PEERCRED`, or the peer SID on Windows. | As documented. Refusing *another* user is not tested automatically, because CI has no second account. It is checked by hand before each minor release (see open items). |
| Remote transport | Remote access is only `ssh <host> hive proxy`, with the user's ssh config. The remote daemon applies the same peer check. Discovery pairing proves the identity key over ssh before saving a machine. | As documented. |
| Credential storage | Observer tokens and web codes and cookies are stored as hashes only (`TestStoreKeepsOnlyHashes`). `identity.json` is 0600. No credential is written to logs. | As documented. "Tokens are never logged" is a rule enforced by review, not by code: there is no logging redaction filter. |
| Secret exposure | Observer payloads leave out environment variables, command lines, working directories, PIDs and session IDs (`TestTranslateRedactsAndLocates`). The desktop view model leaves out the same (`TestWorkspaceLeavesSecretsOut`). | As documented. Environment variables given to `hive env create -e` or `ps start -e` are stored in plain text in `HIVE_HOME`, because Hive needs them to restart agents. The [threat model](threat-model.md#non-goals-and-limits) now says so. |
| Plugin integrity and rollback | Releases are pinned to a commit and a `git archive` checksum, and checked against the manifest's ID and version. Updates are transactional, keep the previous release, and roll back when the daemon cannot load the new one. Tests cover outages and wrong checksums. | As documented. Revocation added (finding 2). |
| Observer scopes, expiry, revocation | Scopes cannot be widened by clients. Revocation ends existing connections. Expired credentials are refused. Tests cover all three, from M14. | As documented. |
| Desktop webview and bindings | Strict CSP. One bound service with typed methods that validate input. Links limited to `http`, `https` and `mailto`. The observer window gets its own credential, revoked on close. | As documented. Packages are unsigned and there is no in-app update yet; the app stays a preview until both exist. |
| Release artifact integrity | `release.yml` refuses to publish without the signing key; `checksums.txt` is signed with ed25519. `hive update` verifies the signature, then the checksum. | As documented. Installers check checksums only (open item). |
| Frontend dependencies | `npm audit --omit=dev` on `desktop/frontend` | 0 vulnerabilities; now checked in CI. |

## Open items

| # | Item | Why it is open | Plan |
|---|---|---|---|
| A | **Official plugin release tags share hive's tag namespace.** The official plugins' release is the tag `v0.1.0` in `admirable-oss/hive`. Finding 9 keeps them from becoming hive versions, but a plugin release can never reuse a version hive has (or will have), and two plugins in the repository cannot release the same version separately. | Registry schema 1 requires a release's `ref` to be `v<version>`, so per-plugin tags (`auto-tab-title/v0.1.0`) cannot be expressed. Changing that needs a registry schema change, which older clients reject. | Move the official plugins to their own repository (`admirable-oss/hive-plugins`) before publishing further releases (M10's open item), and never move an existing tag. |
| B | ~~**Installers do not check signatures.**~~ **Closed in M20 (`v1.1.0`).** | `install.sh` and `install.ps1` could not verify a raw ed25519 signature portably. | Releases now carry `checksums.txt.sshsig`, the release key's signature in OpenSSH's format, which the installers check with `ssh-keygen -Y verify` ([docs/upgrading.md](upgrading.md#installers)). |
| C | **Refusing another user's connection is not tested in CI.** | It needs a second account on the runner. | Manual check per minor release (`sudo -u nobody hive status` must be refused). The self-hosted runner ([docs/releasing.md](releasing.md#the-self-hosted-runner)) is specified with that account; automating the test on it is M20 work. |
| D | **Desktop signing, notarization and in-app updates.** | Need Apple and Windows signing identities (M18 open items). | The app stays a preview, and is not covered by the compatibility policy, until they ship. |
| E | **The 72-hour chaos soak** has not run for this release. | Hosted runners stop at 6 hours; it needs a self-hosted runner. | From M20 the release gate refuses a minor or major release without a successful 72-hour soak on the tagged commit ([docs/releasing.md](releasing.md#the-release-gate)). |
| F | **Logging has no redaction filter.** | "Never log terminal content or tokens" is a coding rule (CONTRIBUTING.md), checked in review. | Consider a `slog` handler that drops known-secret keys. |
