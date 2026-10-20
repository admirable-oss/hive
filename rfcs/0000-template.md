# RFC NNNN: Title

- **Status:** Draft <!-- Draft | Proposed | Accepted | Rejected | Postponed | Implemented (vX.Y.Z) -->
- **Authors:** @you
- **Discussion:** <!-- the pull request -->
- **Affects:** <!-- wire API, CLI, Go SDK, config, plugin API, observer API, registry, on-disk state, … -->

## Summary

One paragraph: what changes, for whom.

## Motivation

The problem, who has it, and what happens today. Link issues, reports and
measurements.

## Proposal

What you propose, in enough detail to review and to implement: new methods,
fields, commands, configuration keys, file formats, with examples.

## Compatibility

How it fits the [compatibility policy](https://hive.admir-saheta.com/docs/ref/compatibility/):

- Is it only additions? If not, what is deprecated, from which release, and
  what replaces it?
- Older clients with a newer daemon, and newer clients with an older daemon:
  what does each see?
- On-disk state: does a schema change? How is it migrated, and what does an
  older release do with the new file?
- Plugins and the registry: what do existing plugins and older hive releases
  see?

## Security and privacy

What it exposes, to whom, and how it is protected. Update
[docs/threat-model.md](../docs/threat-model.md) if a trust boundary moves.

## Testing

How it will be tested: unit, end-to-end, upgrade, fuzzing, budgets.

## Alternatives

What else was considered, and why not.

## Open questions

What is not decided yet.

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
