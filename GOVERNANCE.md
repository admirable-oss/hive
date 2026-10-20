# Governance

How Hive is run: who decides, how substantial and breaking changes are
proposed, what maintainers and official integrations commit to, and how
releases are supported and retired. How to contribute code is in
[CONTRIBUTING.md](CONTRIBUTING.md). The plugin registry's own process is in
[docs/plugins/governance.md](docs/plugins/governance.md).

## Roles

- **Users** run Hive, report problems and take part in discussions and RFCs.
- **Contributors** send pull requests, review, write plugins and agent
  manifests.
- **Maintainers** (currently the Admirable OSS team) review and merge
  changes, cut releases, triage security reports, and hold the release
  signing key. A contributor becomes a maintainer by invitation, after
  sustained, reviewed work; a maintainer who steps back says so and keeps
  their history.
- **Registry maintainers** review plugin submissions and moderation cases.
  They are maintainers, or contributors the maintainers appoint.
- **The maintainer council** is all maintainers acting together. It decides
  what one maintainer should not decide alone: RFCs, ownership transfers
  without the previous owner, appeals and removals of maintainers.

## How decisions are made

- **Most changes** are decided in their pull request: one maintainer's
  approval, and no unresolved objection from another.
- **Substantial changes** need an accepted RFC first (below).
- **The council** decides by consensus. Where it cannot reach consensus, by
  a majority of maintainers, recorded with the reasons. A maintainer with a
  conflict of interest says so and does not vote.

## RFCs

An RFC is needed for:

- a new wire API area, or any change to the protocol's envelope or
  handshake;
- a new plugin API version, or a new capability or contribution kind in an
  existing one;
- a change to the observer API, the registry's documents, or a state file's
  schema;
- a new dependency that clients or plugins see, or a new network-facing
  service;
- anything that would break the
  [compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/)
  (which means a new major version);
- the roadmap for the next major or minor release.

The process is in [rfcs/README.md](rfcs/README.md). In short: copy the
template, open a pull request, discuss for at least two weeks (one for a
roadmap revision), and the council accepts, rejects or postpones it,
recording why. An accepted RFC is a plan, not a promise to ship. Its
implementation still goes through normal review.

## Breaking changes

Within a major version, Hive does not break the CLI, wire API, Go SDK,
configuration, plugin API, observer API, agent manifests, events,
environment variables or on-disk data (the
[compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/),
pinned by the `api/v1/` files and their tests).

A breaking change:

1. starts as an RFC that names the surface, why it must change, and the
   migration;
2. once accepted, deprecates the old surface in the next minor release. It
   keeps working for the rest of the major version, warns when used, and
   names its replacement;
3. is removed only in the next major version, whose release notes list
   every removal and its migration.

The one exception is a security fix that cannot be made any other way. Its
release notes say plainly what broke and why.

## Releases

Hive follows semantic versioning. Releases are annotated tags (`git tag -a
vX.Y.Z`) on `main`, built and signed by the release workflow. Lightweight
tags are refused by it.

| Release | Contains | Cadence |
|---|---|---|
| Patch (`1.0.x`) | Fixes, and during the 1.0 train the milestones of [ROADMAP.md](ROADMAP.md) | As each is ready |
| Minor (`1.x.0`) | New features, deprecations | When the roadmap's next set is ready |
| Major (`x.0.0`) | Removals, after an RFC | Rarely |
| Release candidate (`-rc.N`) | A minor or major release under test, on the preview channel | Before each minor and major |

**Before a release is tagged**, a maintainer checks and records in the pull
request that tags it:

- [ ] CI is green on macOS, Linux and Windows, including `govulncheck`.
- [ ] The nightly run is green: fuzzing, soaks, budgets, and the upgrade test
      from every earlier 1.x release.
- [ ] For a minor release: the 72-hour chaos soak passed on a self-hosted
      runner, and another user's connection was refused by hand
      ([security review, open item C](docs/security-review.md#open-items)).
- [ ] [docs/compatibility.md](docs/compatibility.md) has the release's row,
      and any deprecation or schema change.
- [ ] Every release-blocking defect fixed since the last release has a
      regression test.
- [ ] Plugin reports about failed upgrades or compatibility since the last
      release are triaged.
- [ ] `CHANGELOG.md` describes the release, with security fixes called out.
- [ ] For a minor release: [docs/security-review.md](docs/security-review.md)
      is redone.

**Release-blocking defects.** A bug is release-blocking (P0 or P1) when it
loses data or agents, breaks a compatibility promise, opens a security hole,
or stops a documented workflow on a supported platform. Its fix needs a test
that fails without it. A release candidate becomes the release after two
weeks with no open P0 or P1.

## Support and deprecation

- **Supported versions.** The latest release of the latest minor version gets
  fixes. Security fixes also go to the latest release of the previous minor
  version. Release candidates are fixed by the next candidate. Details are in
  [SECURITY.md](SECURITY.md#supported-versions).
- **Platforms.** A platform and architecture pair is supported while CI builds
  and tests it and releases ship it
  ([docs/platforms.md](docs/platforms.md#support-matrix)). Dropping one is
  announced one minor release ahead.
- **Deprecations** follow the breaking-change process above and are listed in
  [docs/compatibility.md](docs/compatibility.md#deprecated).
- **Previews** (currently the desktop app) are not covered until they are
  declared stable in a release's notes.

## Maintainer expectations

Maintainers:

- review within a week, or say when they cannot;
- answer security reports within three working days ([SECURITY.md](SECURITY.md));
- keep CI green on `main`, and revert a change that breaks it rather than
  leave it;
- hold the line on compatibility, and on the coding standards in
  [CONTRIBUTING.md](CONTRIBUTING.md);
- protect their accounts with two-factor authentication. Only the release
  workflow and an offline copy hold the release signing key;
- disclose conflicts of interest, and step back from decisions they touch.

## Official integrations

An official integration is one this repository ships: agent detection
manifests (`distribution/agent-detection`), agent integrations (`hive
integration install`), the SDKs (`sdk/`, `pkg/hiveapi`, `pkg/pluginsdk`) and
the official plugins. Each one:

- has a maintainer, named in the pull request that adds it, and tests in CI
  (recorded screens for agent manifests);
- follows the compatibility policy like the rest of Hive;
- is kept working with each minor release, or deprecated, with notice, when
  the tool it integrates with goes away;
- takes security reports through this repository's advisories.

Community integrations (plugins in the registry, manifests in users'
configuration) are maintained by their authors. The registry's policies for
them are in [docs/plugins/ecosystem.md](docs/plugins/ecosystem.md).

## Feedback and the roadmap

- **Discussions and issues** are open to everyone. Issue forms ask for the
  version, platform and whether the problem blocks a release.
- **Plugin authors** are asked for feedback with each minor release's
  release candidate: what broke, what the API lacks.
- **The roadmap** for each minor or major release is an RFC
  ([rfcs/0001-after-1.0.10.md](rfcs/0001-after-1.0.10.md) is the first). It
  is drafted from adoption, reliability gaps and the issues and discussions
  since the last one. It is open for comment before the council accepts it,
  and accepted versions update [ROADMAP.md](ROADMAP.md).
