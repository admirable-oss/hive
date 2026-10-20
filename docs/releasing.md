# Releasing Hive

How a release is cut, what must pass before it is published, how the
self-hosted runner for the long gates is set up, and how anyone can verify
a release. The policy (release kinds, support, the checklist) is in
[GOVERNANCE.md](../GOVERNANCE.md#releases).

## What a release contains

The release workflow (`.github/workflows/release.yml`) builds with
goreleaser, from a tag on `main`, and publishes:

| File | What it is |
|---|---|
| `hive_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) | The binary, `LICENSE` and `README.md`, for darwin and linux on amd64 and arm64, and windows on amd64 and arm64 |
| `*.sbom.json` | An SPDX SBOM per archive (syft) |
| `checksums.txt` | SHA-256 of every archive and SBOM |
| `checksums.txt.sig` | An ed25519 signature of `checksums.txt` with the release key; `hive update` checks it |
| `checksums.txt.sshsig` | The same key's signature in OpenSSH's format; `install.sh` and `install.ps1` check it with `ssh-keygen -Y verify` |
| Build provenance | A SLSA attestation per archive, stored by GitHub (`gh attestation verify`) |

The release key's public half is built into every release binary
(`buildinfo.releaseKey`) and embedded in the installers. The private half is
the `HIVE_RELEASE_SIGNING_KEY` secret, and an offline copy the maintainers
hold. A new key means old binaries reject new releases, so it is never
rotated casually.

Builds are reproducible: the same commit builds the same bytes. CI checks
this on every change (`make reproducible`): it builds the archives twice
from copies made at different times and compares them.

## Cutting a release

1. Make sure `main` is green, and the release checklist in
   [GOVERNANCE.md](../GOVERNANCE.md#releases) is done: the compatibility
   matrix row, `CHANGELOG.md`, and for a minor release the security review.
2. Run the nightly checks on the commit you will tag. Use a self-hosted
   runner and a 72-hour chaos soak for a minor or major release:

   ```sh
   gh workflow run nightly.yml --ref main                                  # a patch release
   gh workflow run nightly.yml --ref main -f chaos=72h -f runner=hive-soak  # a minor or major release
   ```

3. Tag that commit with an **annotated** tag and push it:

   ```sh
   git tag -a v1.1.0 -m "v1.1.0: Trusted Distribution"
   git push origin v1.1.0
   ```

   A lightweight tag is refused. The official plugins' release tags in this
   repository are lightweight, and must never publish hive.

## The release gate

The first job of the release workflow (`.github/scripts/release-gate.sh`)
checks the tagged commit before anything is built:

| Tag | Needs, on the tagged commit |
|---|---|
| A release candidate (`v1.1.0-rc.1`) | `ci` succeeded |
| A patch release (`v1.0.10`) | `ci` and a `nightly` run succeeded |
| A minor or major release (`v1.1.0`, `v2.0.0`) | `ci`, and a `nightly` run with a chaos soak of 72 hours or more succeeded |

Nightly runs are named `nightly (chaos <duration>)`, which is how the gate
finds the soak's length. When the gate fails, it says what to run. Fix that,
then delete and push the tag again; nothing was published.

## The self-hosted runner

Hosted runners stop a job after 6 hours, so the 72-hour chaos soak, long
multi-machine soaks, and the cross-user connection test need a machine of
the project's own.

- **Machine:** Linux (amd64), 8 cores, 16 GB of memory, 50 GB of disk, with
  Go, git and OpenSSH installed. Nothing else runs on it.
- **Accounts:** the runner's user, and a second, unprivileged user (`nobody`
  will do) for the cross-user test. The runner's user may run `sudo -u
  nobody` without a password, and nothing else as root.
- **Registration:** add it to `admirable-oss/hive` (Settings → Actions →
  Runners) with the label `hive-soak`, as a service that starts on boot.
  Restrict it to this repository and to `workflow_dispatch` runs of
  `nightly.yml`: a public repository must never run pull requests on a
  self-hosted runner.
- **Use:** `gh workflow run nightly.yml --ref <ref> -f runner=hive-soak -f
  chaos=72h -f soak=8h`.

## Verifying a release

Anyone can check a release without trusting this repository's word for it.

```sh
# The binary you run is the release: download it, verify it, compare.
hive version --verify

# A download, by hand: the signature, then the checksums.
curl -fsSLO https://github.com/admirable-oss/hive/releases/download/v1.1.0/checksums.txt
curl -fsSLO https://github.com/admirable-oss/hive/releases/download/v1.1.0/checksums.txt.sshsig
printf '%s\n' "$(go run ./tools/release-sign allowed-signers <release public key>)" > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I hive-release -n hive-release -s checksums.txt.sshsig < checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing

# Where it was built: the provenance GitHub recorded for the archive.
gh attestation verify hive_1.1.0_linux_amd64.tar.gz -R admirable-oss/hive

# That it is reproducible: build the tag yourself and compare checksums.
git checkout v1.1.0 && make reproducible
```
