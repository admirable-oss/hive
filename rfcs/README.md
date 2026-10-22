# RFCs

Substantial changes to Hive start here, as a written proposal anyone can
comment on before code is written. [GOVERNANCE.md](../GOVERNANCE.md#rfcs)
says which changes need one: protocol and plugin API changes, state schemas,
anything breaking, and each roadmap.

## Process

1. **Open an issue or discussion first** if you are unsure whether an idea
   needs an RFC, or is wanted at all.
2. **Write it.** Copy [0000-template.md](0000-template.md) to
   `NNNN-short-title.md`, using the next free number, and fill it in. Say
   what you propose, why, how it stays compatible, and what you considered
   instead.
3. **Open a pull request** titled `rfc: <title>`. Its status is `Draft` while
   you work on it, and `Proposed` when it is ready for comment.
4. **Discussion** happens on the pull request, for at least two weeks once
   it is `Proposed` (one week for a revision of an accepted roadmap). Plugin
   authors and users are invited when it touches them.
5. **Decision.** The maintainer council sets the status to `Accepted`,
   `Rejected` or `Postponed`, writes the reasons in the RFC's *Decision*
   section, and merges it. Rejected and postponed RFCs are merged too, so
   the reasoning is kept.
6. **Implementation** goes through normal pull requests that link the RFC.
   When it ships, the RFC's status becomes `Implemented (vX.Y.Z)`. An accepted
   RFC whose plan changes gets a new RFC, or an amendment reviewed the same
   way.

## Index

| RFC | Title | Status |
|---|---|---|
| [0001](0001-after-1.0.10.md) | The roadmap after 1.0.10 | Proposed |
| [0002](0002-task-orchestration.md) | Tasks and plans (M22) | Proposed |
