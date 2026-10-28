# Publishing a plugin

From Hive 1.7, `hive plugin publish` makes a registry release in one step,
and `hive plugin test` checks a plugin against any Hive release. The
registry format is in
[REGISTRY.md](../../internal/plugin/REGISTRY.md#schema-2-hive-17); how
listings are reviewed is in [governance.md](governance.md).

## Before the first release

1. **A signing key.** Releases are signed with an ed25519 SSH key, the
   default of `ssh-keygen`:

   ```sh
   ssh-keygen -t ed25519 -f ~/.ssh/hive-plugin -C "acme plugin releases"
   ```

   Its public half (`~/.ssh/hive-plugin.pub`) goes in your publisher record
   in the registry's `registry/publishers.json`, through the pull request
   that submits your listing. Official and verified releases must be
   signed; community plugins may publish unsigned (`--unsigned`), and Hive
   then warns whoever installs them.
2. **A listing.** Submit `registry/submissions/<id>.json` as
   [governance.md](governance.md) describes, with `"publisher": "<your
   publisher id>"` in its listing. Releases are added to it by `publish
   --submit`.
3. **Tests (optional).** A `[test]` table runs your own checks, with
   `HIVE_BIN` and `HIVE_HOME` pointing at the Hive under test:

   ```toml
   min_hive_version = "1.7.0"   # Hive before 1.7 rejects the [test] key

   [test]
   command = ["./test.sh"]
   timeout = "5m"               # default 10m, at most 2h
   ```

## Publishing a release

Bump `version` in `hive-plugin.toml`, commit, then:

```sh
hive plugin publish --key ~/.ssh/hive-plugin --dry-run   # everything but tag, push and submit
hive plugin publish --key ~/.ssh/hive-plugin --push --out release.json
```

`publish`:

1. checks the manifest, and refuses uncommitted changes or a tag that
   already points at another commit (releases are immutable: bump the
   version);
2. runs `hive plugin test` against this Hive (`--skip-tests` skips it);
3. tags the commit `v<version>`, or `<id>/v<version>` for a plugin in a
   subdirectory, so plugins sharing a repository tag apart (`--ref`
   chooses another tag ending in the version), and pushes it with `--push`
   (`--git-remote`, default `origin`);
4. computes the `git archive` checksum the registry pins;
5. signs the release statement with `ssh-keygen -Y sign`. `--key` is a
   private key file, or a public key file whose private half is in
   `ssh-agent`; without it, git's `user.signingkey` is used (a path, or
   `key::ssh-ed25519 …`);
6. records how it was built: in GitHub Actions the repository, workflow
   and run; anywhere else, `local`;
7. prints the release record (`--json`, or `--out <file>`).

`--submit --registry <checkout of the registry repository>` appends the
release to your submission and opens the pull request with `gh`.

## From GitHub Actions

```yaml
on:
  push:
    tags-ignore: ['**']
    branches: [main]
    paths: [hive-plugin.toml]
jobs:
  publish:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # push the release tag
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: admirable-oss/hive/.github/actions/plugin-publish@v1.7.0
        with:
          signing-key: ${{ secrets.HIVE_PLUGIN_SIGNING_KEY }}   # the private key file's contents
          registry-token: ${{ secrets.HIVE_REGISTRY_TOKEN }}     # optional: open the registry PR
```

The action installs Hive, publishes with the key from the secret (written
to a private temporary file and removed afterwards), and records the run as
the release's provenance.

## Testing against Hive releases

```sh
hive plugin test                      # this plugin, this Hive
hive plugin test --hive 1.7.0-rc.1    # a release, downloaded and checked against its signature
hive plugin test --hive ./bin/hive    # a build of your own
hive plugin test pr-status --json     # a catalog plugin's newest release
```

It validates, installs (links) and checks the plugin with `hive plugin
doctor` in a scratch `HIVE_HOME`, runs the `[test]` command, prints each
check, and exits 1 on a failure.

The registry runs it for every listed plugin against each Hive release
candidate (the `plugin-compat` workflow). The results appear in the
catalog as each release's `tested`, and in `hive plugin info`. A failing
official plugin blocks the Hive release; a failing community plugin is
recorded and reported on its repository.
