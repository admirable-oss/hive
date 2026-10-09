---
description: Verify Hive release readiness across shipped changes, changelog, documentation, agent workflows, distribution, and runtime performance.
---

# Hive Five Release Check

Audit the selected Hive release candidate before publication. Determine what will actually ship, verify that user-facing changes are documented, identify release blockers, and produce an evidence-based readiness report.

This is an **audit-only workflow** unless the user explicitly asks to apply fixes.

## Inputs

- **Base ref (optional):** a Git ref the user names as the previous release.
- **Release intent (optional):** the version being prepared, and any constraints the user states.

Use the supplied context to understand the requested release, but do not assume that a branch name, preview tag, or recent commit necessarily represents the correct release boundary.

## Operating principles

1. **Audit the selected release, not whatever happens to be on main.**
2. Treat merged pull requests as release units and unassociated commits as individual changes.
3. Build a complete inventory before judging the changelog.
4. Describe product behavior, not implementation details, in user-facing release notes.
5. Verify claims against the actual code, tests, documentation, and distribution artifacts.
6. Distinguish confirmed defects from missing evidence and decisions requiring maintainer input.
7. Never silently omit an uncertain release item.
8. Never modify files during an audit unless explicitly authorized.
9. Never publish or tag a release as part of this workflow.
10. Prefer the smallest set of changes needed to make the release trustworthy.

---

# 1. Establish the Release Boundary

Determine the base revision against which the selected release candidate must be audited.

### 1.1 Resolve the base ref

If the user named a base ref and it resolves (`git rev-parse --verify <ref>`), use it as the base.

Otherwise:

1. Fetch the latest available `main` ref and tags.
2. Inspect the repository's actual release-publication workflow to determine how the previous stable version is recorded.
3. Identify the currently published stable version using the authoritative release metadata or publication mechanism.
4. Resolve that version to its corresponding Git tag or commit.

Do not use `git describe` to infer the release boundary. Preview tags, off-branch preparation, and release candidates can make nearest-tag ancestry misleading.

If the authoritative stable version cannot be determined, report the ambiguity instead of silently choosing a convenient tag.

Inspect the relevant metadata and release scripts before assuming where release state is stored.

### 1.2 Verify the candidate

Record:

- Selected base ref and commit SHA.
- Current `HEAD` and commit SHA.
- Current branch.
- Working-tree status.
- Whether the candidate is based on the intended release line.

The working tree may contain user changes. Do not reset, clean, stash, or otherwise modify it.

### 1.3 Enumerate the release range

Use first-parent history to establish the mainline release context:

```bash
git log --first-parent --reverse --format='%H%x09%s' <base>..HEAD
```

Inspect full commit messages and bodies when needed:

```bash
git log --reverse --format='%H%x09%s%n%b' <base>..HEAD
```

All subsequent conclusions must be scoped to changes included in `<base>..HEAD`.

Do not include unrelated work that exists on newer `main` but is absent from the selected candidate.

---

# 2. Build the Shipped-Change Inventory

The inventory is the foundation of the audit. Do not inspect the changelog first and then work backward to justify its contents.

### 2.1 Identify release units

Inspect first-parent history for merged pull requests, including:

- Merge commits.
- Squash commits with subjects such as `Add persistent agent sessions (#123)`.
- Other repository-specific PR identification conventions.

When a PR number is available and GitHub CLI is authenticated, retrieve its metadata:

```bash
gh pr view <number> --json number,title,body,author,mergedAt,baseRefName,headRefName
```

Use PR metadata to understand user intent, issue context, contributor identity, and scope.

Treat each merged PR as one release unit.

Do not separately list individual commits already represented by that PR.

Treat commits that are not represented by a merged PR as independent release units.

Do not assume every commit mentioning a PR number is a separately shipped change.

### 2.2 Inspect the actual changes

For each release unit, inspect its changed files and diff:

```bash
git diff --stat <base>..HEAD
git diff --name-status <base>..HEAD
git show --stat --summary <commit>
```

Use targeted diffs and read relevant files in full when necessary.

Pay particular attention to changes affecting:

- Hive CLI commands, flags, arguments, and output.
- Runtime lifecycle and background-server behavior.
- Socket protocol and client/server compatibility.
- PTY creation, attachment, detachment, and process handling.
- Session persistence and restart recovery.
- Pane creation, splitting, navigation, history, and layout restoration.
- Local and SSH machine connections.
- Agent detection, integrations, labels, and direct attachment.
- Native agent resume and handoff behavior.
- Configuration files, defaults, keybindings, and environment variables.
- Plugin manifests, actions, event hooks, and execution boundaries.
- Installation, upgrades, packaging, and distribution.
- Error handling, security boundaries, and data-loss risks.
- Public APIs, automation workflows, and documented integration contracts.

Use the repository's actual feature set. Do not assume a planned feature has shipped merely because it appears in a README or roadmap.

### 2.3 Classify every release unit

Assign each item one of these classifications:

| Classification | Meaning |
|---|---|
| User-facing | Changes observable behavior, functionality, compatibility, or usability. |
| Internal / maintenance | Refactoring, dependency updates, infrastructure, or maintenance without meaningful user-visible impact. |
| Documentation-only | Documentation changes without a material change to product behavior. |
| Needs decision | Ambiguous impact, unresolved compatibility implications, or a release decision requiring maintainer judgment. |

Identify pure housekeeping separately:

- Version-only updates.
- Release or tag commits.
- Changelog-only commits.
- Formatting-only changes.
- Comment-only changes.
- Internal refactoring with no meaningful external impact.

Do not automatically exclude an internal change if it affects reliability, security, performance, installation, or supported environments.

For each meaningful release unit, record:

- PR number or commit SHA.
- Human-readable summary.
- Affected subsystem.
- User-facing impact.
- Relevant issue references.
- Documentation implications.
- Changelog status.
- Any unresolved release decision.

---

# 3. Audit the Next-Release Changelog

Discover the repository's actual changelog conventions before deciding which files are authoritative.

Treat the latest published changelog as historical context and the designated next-release changelog as the candidate's human-curated release notes.

Do not assume Hive uses the same paths or release-preparation workflow as another project. Inspect the repository and its release scripts.

### 3.1 Compare against the complete inventory

Review every release unit classified as user-facing or requiring a decision.

Check whether the next-release changelog accurately covers:

- New functionality.
- Bug fixes.
- Removed functionality.
- Breaking changes.
- Changed defaults.
- Compatibility changes.
- CLI behavior.
- Configuration options.
- Public APIs and automation behavior.
- Agent integration changes.
- Session and process lifecycle changes.
- Security-relevant changes.
- Installation or upgrade behavior.

Flag missing entries.

Flag existing entries that do not correspond to changes in the selected release range.

Flag statements that promise behavior not implemented by the candidate.

### 3.2 Write at product height

Release notes should explain what developers gain, what becomes easier, what no longer breaks, or what behavior changes.

Prefer:

> Restored sessions now recover their saved pane layout after restarting Hive, making it easier to return to the same workspace.

Avoid:

> Added a new layout restoration method to the runtime session manager.

The implementation detail belongs in the commit history, not the changelog.

Use concise, human-written prose. Do not mechanically convert conventional commit subjects into release notes.

### 3.3 Preserve the existing style

Follow the project's existing section conventions. Prefer these categories when they fit the repository:

- Added
- Changed
- Fixed
- Removed
- Breaking Changes

Do not create redundant categories or reorganize the changelog unnecessarily.

If no meaningful user-facing changes occurred, say so. Do not invent features to make the release look substantial.

### 3.4 External contributors

For each merged external human PR, verify whether the corresponding changelog entry acknowledges the contributor in the repository's established style.

For example:

`(#123, thanks @username)`

When an entry primarily addresses a shipped issue, include both the issue and PR references when useful.

Do not add thanks text for bots or automation accounts.

### 3.5 Audit issue references

Inspect commit bodies for issue references such as:

`refs #123`

Also detect GitHub closing keywords in ordinary commits:

- `fixes #123`
- `closes #123`
- `resolves #123`

Flag closing keywords that may cause an issue to close when a commit lands on `main`, before the release containing the fix is published.

Do not require or introduce closing keywords in changelog entries.

For every shipped issue reference:

1. Verify that the referenced change is actually in the release range.
2. Determine whether the changelog should mention the issue.
3. Confirm the reference points to the intended issue.
4. Include the issue in the report's post-release closure checklist where appropriate.

Use the following report section:

**Issue references to close after release:**

- `#123` — short explanation of the shipped fix.

Include only verified references that the release operator should check. Explain uncertainty instead of presenting inferred references as confirmed.

---

# 4. Audit Public Documentation

Inspect the repository to identify the authoritative locations for:

- The current stable README.
- The next-release README or release-facing documentation.
- The stable documentation website.
- The next-release documentation draft.
- Localized documentation.
- Published examples and sample configurations.
- Agent-facing instructions or bundled skills.

Determine how the release process promotes or publishes each source. Do not assume every generated or published documentation directory is editable.

### 4.1 Compare implementation against documentation

Compare meaningful shipped changes against the next-release documentation first.

Flag missing or inaccurate guidance for:

- New CLI commands and flags.
- Configuration keys and changed defaults.
- Agent integrations and detection behavior.
- Session restoration and lifecycle semantics.
- PTY and pane workflows.
- SSH connections and reconnection behavior.
- Socket API methods and protocol behavior.
- Plugin contracts and execution semantics.
- Installation, upgrades, and supported platforms.
- Compatibility requirements and breaking changes.
- Security warnings and operational limitations.

Documentation requirements should be proportional to the feature. A trivial internal fix does not automatically need a new guide.

### 4.2 Compare next-release docs against stable docs

Compare the candidate's README and website draft against the latest stable documentation.

Classify meaningful differences as:

- Intended to ship.
- Stale or inaccurate.
- Missing required documentation.
- Needs maintainer decision.

Do not require the candidate's documentation tree to be identical to stable documentation. New features and improvements are expected.

Do not use newer main documentation as evidence that a feature exists in the selected release.

### 4.3 Audit localized documentation

If the project publishes translations, compare the next-release English documentation against every supported localized documentation tree.

Check for:

- Missing translated files.
- Translated files that are now stale.
- Missing sections.
- Heading-outline drift.
- Incorrect links.
- Changed commands or configuration examples.
- Technical terms that have become inconsistent with the implementation.

Do not require translated prose to match English word for word. Verify that its technical meaning and heading structure remain aligned.

If localization is intentionally deferred, distinguish that policy decision from an accidental omission.

### 4.4 Audit examples

Inspect example configuration files, command snippets, installation instructions, and copied setup templates.

Verify that they:

- Use valid current command names and options.
- Reflect the actual configuration schema.
- Use correct defaults.
- Do not reference removed features.
- Do not imply unsupported behavior.
- Are consistent with the selected release candidate.

Examples are part of the public interface. An outdated example can be a release blocker even when the implementation is correct.

### 4.5 Respect generated documentation

Determine which files are maintained by humans and which are generated by release automation.

Never modify generated preview or published snapshots during an audit.

Do not copy draft documentation into generated release directories as a shortcut to make the trees appear synchronized.

---

# 5. Audit the Hive Agent Skill

Identify the canonical agent-facing skill distributed with Hive, using the repository's actual path.

For example, if `skills/hive/SKILL.md` exists, audit that file. Do not assume a path before checking the repository.

The bundled skill must accurately describe the behavior shipped in the selected candidate.

Review its guidance for:

### CLI
- Current command names and options.
- Correct examples.
- Supported flags and expected output.
- Status, stop, attach, and other lifecycle commands where applicable.

### Runtime and sessions
- Starting and stopping the runtime.
- Background execution.
- Detaching and reconnecting.
- Restart recovery.
- The difference between restoring a workspace and resuming an agent's original process.
- Pane history and live handoff semantics.

### Agents and machines
- Agent discovery.
- Supported integrations.
- Direct attachment.
- Local and SSH machine workflows.
- Reconnection behavior.
- Correctly supported resume mechanisms.

### Automation and API
- Current socket location and protocol.
- Supported operations.
- Correct request and response examples.
- Error handling.
- Compatibility expectations.

### Safety
- Commands that can terminate processes or destroy work.
- Correct handling of user data and session state.
- Confirmation requirements for destructive actions, if applicable.
- The limits of automatic agent recovery.
- Unsupported assumptions about process persistence.

Flag stale commands, incorrect behavioral claims, missing warnings, and examples that no longer work.

Do not require byte-for-byte synchronization with other documentation. Audit semantic accuracy.

If the skill is bundled into the binary or release archive, treat inaccuracies as shipped product defects.

---

# 6. Verify Release Finalization

Inspect the repository's actual release scripts, CI workflows, build configuration, and publication process.

Determine which files must be finalized before release and which are generated after publication.

### 6.1 Release-facing files

Verify that approved release-facing README changes are finalized in the correct source file before the release process runs.

Verify that the next-release changelog is complete and consistent with the release inventory.

Do not promote drafts into published documentation snapshots manually unless the repository's documented process explicitly requires it.

### 6.2 Build and packaging

Identify the project's actual Go build, test, packaging, and distribution workflows.

Inspect:
- `go.mod` and `go.sum`, if present.
- Build tags and supported Go versions.
- Cross-compilation configuration.
- CLI binary naming and output paths.
- Release archives and checksums.
- Container or package definitions, if used.
- Embedded resources and bundled agent skills.
- Version injection and release metadata.
- Installation scripts and upgrade compatibility.

Do not import Rust-specific or Nix-specific release requirements from another project unless Hive actually uses those systems.

### 6.3 Dependency integrity

Review dependency changes for compatibility and reproducibility.

Check that:
- Required dependency updates are committed.
- Build instructions match the supported toolchain.
- New platform-specific dependencies are intentional.
- Git-based dependencies, if any, are pinned and reproducible.
- The project's actual packaging system can resolve the dependencies.

Do not regenerate lockfiles or modify dependencies during the audit.

### 6.4 Required release checks

Discover the repository's documented validation commands and identify which are required before publication.

Run appropriate non-destructive checks when practical, subject to the user's instructions and the state of the working tree.

Potential checks include:

```bash
go test ./...
go vet ./...
go build ./...
```

These are examples, not a substitute for the repository's actual release-check commands.

Do not assume these three commands alone constitute release validation. Inspect the project's `Makefile`, `justfile`, CI workflows, scripts, and contributor documentation.

Record which checks were run, which passed, which failed, and which were not run.

Do not run a release or publication command as part of this audit.

---

# 7. Review Runtime and Rendering Performance

Hive is a persistent runtime for interactive coding-agent workflows. Release readiness includes performance and reliability, not only documentation.

Identify the project's existing performance benchmarks and release-relevant runtime tests.

Prioritize:

- Pane creation and teardown.
- Workspace resize and layout calculation.
- Active versus background panes.
- Large workspace behavior.
- PTY output and scrollback.
- Socket request handling.
- Session restoration.
- Runtime startup and shutdown.
- Agent attachment and reconnection.
- Memory use and goroutine lifecycle.
- Resource cleanup after disconnects.

Use existing benchmarks whenever possible.

If the repository defines a workload-size benchmark at 1, 15, and 50 panes, record the median and p95 latency for each workload and compare the scaling ratios.

For each relevant operation, report:

| Workload | Median | p95 |
|---|---:|---:|
| 1 pane | measured result | measured result |
| 15 panes | measured result | measured result |
| 50 panes | measured result | measured result |

Compare the 15-to-1 and 50-to-1 ratios for median and p95 latency.

Do not fabricate measurements or infer performance from code inspection.

If the existing benchmark does not report p95, identify that limitation. Recommend extending the benchmark or running an appropriate measurement rather than inventing a result.

Treat a material regression against a comparable baseline as a potential release blocker until investigated.

Do not invent universal timing thresholds. Consider the operation, workload, previous baseline, machine, and variance.

If no meaningful baseline exists, report the available measurements and state that the regression assessment is inconclusive.

Avoid running expensive or disruptive benchmarks without considering their effect on the user's environment.

---

# 8. Apply the Release-Readiness Gate

Before marking the release ready, assess each of the following.

### Product correctness
- No known critical runtime or data-loss defect remains unresolved.
- Public CLI and API behavior matches implementation and documentation.
- Session and process lifecycle semantics are accurate.
- Supported integrations and platforms are correctly represented.

### Changelog completeness
- Every meaningful user-facing change has been considered.
- Missing and stale entries are identified.
- External contributor acknowledgments follow project conventions.
- Issue references are verified.

### Documentation
- Required next-release guides exist.
- Examples reflect the candidate.
- Localized documentation has been reviewed.
- Bundled agent instructions match shipped behavior.

### Build and distribution
- Required release checks have been identified.
- Relevant validation results are recorded.
- Version metadata and distribution artifacts are consistent.
- Installation and upgrade guidance is accurate.

### Performance
- Required benchmarks have been reviewed.
- Material regressions are investigated.
- Missing measurements are reported honestly.

### Release process
- Required release-facing files are finalized.
- Generated files remain under their intended automation workflow.
- The working tree and release procedure meet the project's requirements.

A release is **NOT READY** if a verified release blocker remains unresolved.

Missing evidence must be reported explicitly. Do not mark a check as passing merely because no problem was observed.

A documentation or localization issue may be non-blocking if the project explicitly accepts the gap. Record the decision and its scope rather than silently downgrading the finding.

---

# 9. Apply Changes Only When Authorized

The default behavior is audit-only.

Do not edit source files, changelogs, documentation, configuration, release metadata, or generated output during the audit.

If the user explicitly requests fixes:

1. Present or identify the findings being addressed.
2. Update the changelog using the complete release inventory.
3. Update the appropriate next-release README and website documentation.
4. Correct inaccurate examples and agent instructions.
5. Preserve existing project conventions and generated-file boundaries.
6. Run the relevant validation checks.
7. Re-audit the affected release items.
8. Report any unresolved findings.

If the user requests release finalization, finalize only the designated human-maintained release files and run the repository's documented release-documentation checks.

Do not create tags, publish releases, deploy documentation, or invoke release automation unless the user separately authorizes those actions.

---

# 10. Final Report

Use this format for the final response.

Keep the main report concise. Include an appendix only when commit inventories, commands, or technical evidence materially help the release operator.

```md
# Hive Five Release Check

Release readiness: READY | NOT READY

Base: <base ref and SHA>
Candidate: <branch or ref and HEAD SHA>
Range: <base>..HEAD
Working tree: CLEAN | DIRTY
Meaningful shipped changes: YES | NO

## Changelog: OK | MISSING ENTRIES | NEEDS ATTENTION

Missing:
- <only user-facing changes absent from the changelog>

Stale or questionable:
- <entries not supported by the release range>
- <unclear or implementation-focused entries>

## Documentation: OK | MISSING | INACCURATE | NEEDS DECISION

Missing:
- <required documentation gaps>

Wrong or questionable:
- <docs, examples, configuration, or compatibility guidance that disagrees with implementation>

Localization:
- <missing translations, stale files, heading drift, or accepted gaps>

## Issue references: OK | NEEDS ATTENTION

Potential premature closures:
- <ordinary commits using GitHub closing keywords>

Issue references to close after release:
- #<issue> — <shipped change and verification status>

Contributor acknowledgments:
- <missing PR attribution, or OK>

## Agent skill: UP TO DATE | NEEDS UPDATE | NOT CHECKED

- <stale commands, incorrect lifecycle claims, missing guidance, or verification status>

## Build and distribution: OK | NEEDS ATTENTION | NOT CHECKED

- <required checks and their results>
- <packaging, versioning, installation, or upgrade concerns>

## Runtime performance: OK | NEEDS ATTENTION | NOT CHECKED

- <benchmark name and environment>
- <measured results and scaling ratios>
- <baseline comparison and unresolved concerns>

## Release finalization: COMPLETE | INCOMPLETE | NOT CHECKED

- <required source files and checks>
- <whether finalization checks ran and their results>

## Accepted / no action

- <explicitly accepted gaps or maintenance items that require no action>

## Required before release

1. <highest-priority action>
2. <next action, if applicable>

## Appendix

- <release inventory or commands, only when materially useful>
```

### Reporting rules

- Use `READY` only when the required release checks have sufficient evidence and no unresolved blocker remains.
- Use `NOT READY` when a verified blocker exists or required release-critical evidence is missing.
- Distinguish a confirmed defect from an unverified concern.
- Include file paths, commit SHAs, PR numbers, or test names when they make findings actionable.
- Do not list every housekeeping commit in the main report.
- Do not describe unexecuted tests as passing.
- Do not claim a release is published or ready to publish solely because the working tree is clean.
- Do not force changelog entries when the release contains no meaningful user-facing changes.
- Do not invent issue references, performance results, contributor identities, or release metadata.

**The objective is simple: know exactly what is shipping, make sure the documentation tells the truth, and leave the release operator with a clear decision rather than a pile of unchecked boxes.**