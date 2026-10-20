# Upgrading and recovery

How to upgrade Hive on each install path, what happens when an upgrade
fails, how to go back, and how to recover when a state file is damaged.
What stays compatible between versions is in
[docs/compatibility.md](compatibility.md).

Agents keep running through all of it. They run under their shims, which
are separate processes, so upgrading, rolling back or restarting the
daemon never stops them.

## Before you upgrade

```sh
hive version            # this binary, and the daemon it talks to
hive update --check     # is there a newer release on your channel?
```

Back up `~/.hive` (`%LOCALAPPDATA%\Hive` on Windows) and your configuration
file (`hive config path`) if you want a way back across a release that
changes a file's schema. Within 1.0.x no schema changes; the
[backup section](operations.md#backups) of the operations guide lists what
to keep.

## Upgrading

### `hive update` (install.sh, install.ps1, release archives)

```sh
hive update                      # the latest release on your channel
hive update --channel preview    # release candidates too (or update.channel)
hive update --version v1.0.10    # a given release
```

`hive update`:

1. downloads the release archive for this system, `checksums.txt` and
   `checksums.txt.sig`;
2. checks the signature against the release key built into your binary,
   then the archive against its checksum. An unsigned, wrongly signed or
   altered release is not installed. A hive built from source has no key
   and checks only the checksum; it says so.
3. writes the new binary beside the old one and runs `hive version` with it.
   If that fails, nothing changes.
4. swaps it in, keeping the old binary as `hive.old` (`hive.exe.old`);
5. restarts every running daemon on it, keeping the agents.

### Homebrew

```sh
brew upgrade hive-agents         # the formula
brew upgrade --cask hive         # the cask
```

`hive update` refuses to replace a binary Homebrew installed, and names the
command that upgrades it. Run `hive daemon restart` afterwards, or let the
next command replace the older daemon.

### Nix

Update the flake input (`nix profile upgrade`, or your system configuration).
Nix builds Hive with the Go toolchain of your nixpkgs. Standard-library
security fixes reach you when nixpkgs updates Go; release archives are
built with the toolchain `go.mod` names.

### Windows

`hive update` works the same way. The running `hive.exe` is renamed to
`hive.exe.old` (Windows cannot overwrite a running program), and the
installer can be run again at any time:

```powershell
$env:HIVE_VERSION = 'v1.0.10'; irm https://raw.githubusercontent.com/admirable-oss/hive/main/distribution/install.ps1 | iex
```

### The desktop app (preview)

The preview has no in-app updates yet. Install the new package over the old
one. The app bundles its own `hive`, at the app's version. Quitting and
reinstalling the app leaves agents running.

### Installers

`install.sh` (`HIVE_VERSION`, `HIVE_CHANNEL`, `HIVE_INSTALL_DIR`) and
`install.ps1` check the archive against `checksums.txt`. They cannot check
the signature, so to verify a download made outside `hive update`:

```sh
curl -fsSLO https://github.com/admirable-oss/hive/releases/download/v1.0.10/checksums.txt
curl -fsSLO https://github.com/admirable-oss/hive/releases/download/v1.0.10/checksums.txt.sig
go run ./tools/release-sign verify <release public key> checksums.txt   # from a checkout
shasum -a 256 -c checksums.txt --ignore-missing
```

The public key is published with each release, and `hive update` uses the
copy built into your binary. Once one verified hive is installed, every
`hive update` after it checks signatures by itself.

## When an upgrade fails

| What fails | What Hive does | What you see |
|---|---|---|
| Download, signature or checksum | Installs nothing | The reason ("not signed by the hive release key", "does not match its checksum"); the old binary and daemon are untouched |
| The new binary does not run here | Removes it, installs nothing | "the new hive does not run here; keeping vX" |
| Swapping the binary in | Puts the old binary back | The error; `hive` is never missing or half written |
| The new daemon does not start (`v1.0.10`) | Puts the old binary back and starts the daemons on it again | "rolled back to vX and restarted its daemons (agents kept running)"; the new binary is kept as `hive.old` to look at |
| Restarting with `--no-restart` | Nothing more to do | Daemons stay on the old binary until `hive daemon restart` |

`hive update --from <file>` installs a binary or release archive you already
have. It checks neither signature nor checksum, so verify the file first
(above).

## Going back

```sh
hive update --rollback
```

This puts back the binary the last update replaced (`hive.old`), keeping
the newer one in its place so the rollback can itself be undone. Then it
restarts the daemons on it. Agents keep running. It works on macOS, Linux
and Windows.

`--rollback` needs the previous binary, which the release doing the update
keeps from `v1.0.4` (M13) on. An update performed by an earlier release,
such as `v1.0.0-rc.1`, kept nothing. Go back from it by installing the
earlier release again:

```sh
hive update --version v1.0.0-rc.1 --force       # or --from <earlier binary>
HIVE_VERSION=v1.0.0-rc.1 sh install.sh          # or with the installer
```

**State after going back.** Within 1.0.x, every state file keeps schema 1,
so the earlier release reads everything the later one wrote. (The upgrade
test checks this.) After a future release that changes a schema, the
earlier release refuses the changed files rather than misreading them, and
names each one. Restore those files from the backup you made before
upgrading, or upgrade again.

## Recovering damaged state

A crash cannot leave a torn file: state is written to a temporary file,
synced and renamed into place. Disks, editors and manual changes still can
damage one. What Hive does, and what to do:

| File (in `HIVE_HOME`) | Hive does | You do |
|---|---|---|
| `config.toml` (`hive config path`) | Clients warn and use defaults; **the daemon does not start**; a bad value only warns | `hive config validate` names the line; fix it, or move the file aside |
| `session.json` | Moves it to `session.json.corrupt-<time>` and uses the newest readable snapshot in `snapshots/` | Nothing. `hive gc` removes old `.corrupt-*` files |
| `environments/<env>/layout.json` | Moves it aside and rebuilds the tabs from the snapshot | Nothing |
| `environments/<env>/environment.json` | Leaves that environment out | Restore it from a backup, or remove the directory and recreate the environment |
| `plugins/registry.json` (installed plugins) | Refuses plugin commands, naming the file | Restore it from a backup. Otherwise move it aside and reinstall the plugins; their settings and data in `plugins/.config` and `plugins/.data` are kept |
| `plugins/.transaction.json` | Finishes or undoes an interrupted update on the next plugin command; stops with "manual repair required" if the file is damaged | Run `hive plugin doctor`; restore `plugins/` from a backup, or remove the transaction file once the active release is whole |
| `observers/credentials.json` | Refuses observer commands and connections, naming the file | Restore it, or move it aside: every observer credential is then invalid. Create new ones (`hive observer create`) |
| `machines.json` | Refuses machine commands, naming the file | Restore it, or move it aside and add the machines again (`hive machine add` or `pair`); pairing verifies each identity again |
| `identity.json` | Refuses to load it, and never replaces it on its own | Restore it from a backup. Moving it aside makes a new identity, which every machine that paired with this one holds back until you accept it there (`hive machine trust`) |
| `web/access.json`, `web/runtimes.json`, `web/push.json` | Refuses web commands, naming the file | Restore them, or move them aside: share links and sign-ins end, and runtimes are added again (`hive web runtime add`) |

A file that a newer hive wrote, in a schema this one does not know, is
refused the same way and left untouched. Use the newer hive, or restore a
backup.

If a broken binary leaves the daemon down, agents still run under their
shims. Any working `hive` (the previous one, `hive.old`, or a fresh install)
starts a daemon that attaches to them again: `hive ps list` shows them.

## Removing Hive

```sh
hive daemon uninstall        # the login service, if you installed one
hive stop                    # the daemon and every agent it manages
rm "$(command -v hive)"      # or: brew uninstall hive-agents / --cask hive
rm -rf ~/.hive               # your data: sessions, logs, plugins, credentials
```

On Windows, run `install.ps1 -Uninstall` (add `-RemoveData` to delete
`%LOCALAPPDATA%\Hive` too).
