# Plugin registry governance

The public catalog is a static registry. Changes are proposed and reviewed as
pull requests; the PR, checks, review approvals, and merged commit form the
authoritative review audit. `registry/submissions/<plugin-id>.json` is the
versioned submission record. Its schema is in `registry/submissions/schema-v1.json`.

## Submission states

Records move through `draft`, `submitted`, `changes_requested`, `approved`,
`rejected`, `withdrawn`, and `archived`. The validator checks allowed state
transitions and requires each transition in the append-only `history` list.
Update a record by adding an event; do not delete or rewrite prior events.
Release records are append-only too. A withdrawal or archive changes listing
availability without erasing the published release history.

To submit, open a PR containing a valid `hive-plugin.toml`, license, maintainer
metadata, immutable release ref/commit/checksum, and a submission record in the
`submitted` state. The `Plugin registry submissions` workflow validates the
record and registry package tests. Both repositories protect `main` with the
registry validator and review-gate checks required. A maintainer records
requested changes or the decision in the history and links the PR/review in
`reference`.

## Ownership and trusted listings

Ownership transfer is a PR, never a silent maintainer-list replacement. Include
the old and new maintainers' GitHub logins, outgoing approval, incoming
acceptance, and an `ownership_transferred` history entry. The submission check
compares old and new maintainers with the transfer record and queries the PR's
review history to require approvals from outgoing and incoming maintainers.
Keep the prior maintainer list and transfer record in the PR history. If maintainers are
unavailable, a maintainer council may transfer ownership after recording the
evidence and rationale in the review history.

Official and verified status requires human review. Any registry change while
a trusted listing is present waits at the `plugin-registry-trusted-review`
GitHub Environment. Both repositories configure the authenticated maintainer
as required reviewer, disable self-approval and admin bypass, and require the
review-gate status before merge. A trusted listing PR also needs the normal PR
approval. Do not set trusted flags unless the gate passes.

## Reports, appeals, withdrawal

Use the Plugin report issue template for listing or behavior reports. Triage
the issue, preserve evidence, and link the report to the submission history.
For a suspected security vulnerability, use GitHub's private Security
Advisories instead of a public issue. A maintainer may temporarily delist a
plugin while investigating; record the reason and keep its release history.

Use the Plugin appeal issue template to request an independent reviewer. Link
the original decision and add the outcome as a new history event. Plugin owners
can withdraw by PR, moving the submission to `withdrawn`; archival uses
`archived`. Neither action silently deletes historical metadata or releases.

## Moderation and contributor expectations

Reviewers check manifest validity, supported Hive compatibility, license and
ownership, source integrity, requested runtime commands/capabilities, and
whether the listing matches the published package. Human reviewers decide
trust and policy questions; automated checks do not confer verification.
Reports and moderation decisions should include a reason, evidence reference,
reviewer, and timestamp. Keep discussion civil and disclose conflicts of
interest. Appeals should be reviewed by someone who did not make the original
decision where practical.
