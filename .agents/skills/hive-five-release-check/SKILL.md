---
name: hive-five-release-check
description: Audit Hive release readiness by comparing the selected release candidate against shipped changes, changelog coverage, public documentation, bundled agent instructions, build and distribution checks, and runtime performance. Use when asked to audit a Hive release, check release readiness, review changelog coverage, verify issue references, validate release documentation, or apply explicitly requested release fixes.
---

# Hive Five Release Check

Use this skill only inside the Hive repository.

Read `references/hive-five-release-check.md` and follow its workflow. Treat that reference as the source of truth for:

- Establishing the correct release base from the selected candidate and published stable-version metadata.
- Auditing the exact release range rather than unrelated changes on `main`.
- Inspecting first-parent history, merged pull requests, direct commits, and their user-facing impact.
- Building a complete release inventory before reviewing the next-release changelog.
- Checking changelog completeness, stale entries, contributor acknowledgments, and issue references.
- Detecting GitHub closing keywords that could close issues before the fix is released.
- Auditing next-release README and website documentation against shipped behavior.
- Reviewing localized documentation, examples, configuration, and compatibility guidance.
- Checking the bundled Hive agent skill against shipped CLI, runtime, session, and agent-control behavior.
- Verifying release finalization, build and distribution requirements, and repository-specific validation commands.
- Reviewing runtime reliability and performance benchmarks, including workload scaling when supported.
- Producing the final release-readiness report with evidence, blockers, and required actions.

## Hive Today

Verify these against the repository before relying on them; they describe Hive as of v0.5 (M3) and change as release tooling lands:

- **Default branch:** `main`. Milestones merge as pull requests (`M2/Workspace-Model`, `M3/Multiplexer-TUI`).
- **Release metadata:** no `CHANGELOG.md`, no version tags and no publishing workflow exist yet. `.goreleaser.yaml` builds snapshots only (`make snapshot`; CI job `release-snapshot`), with `CGO_ENABLED=0` for darwin and linux, amd64 and arm64. Until tags exist, establish the base from the previous milestone's merge into `main` (or a ref the user names) and say so in the report.
- **Version injection:** `-X …/internal/buildinfo.version|commit|date` in `.goreleaser.yaml` and the `Makefile`. The daemon also reports `api_level` (`protocol.APILevel`) in `hive status --json`; a change to methods, events or their meaning must raise it.
- **Documentation:** `README.md`, `ROADMAP.md`, `docs/adr/` (decision records, amended per milestone) and `docs/keybindings.md`, which is generated (`go test ./internal/tui/keymap -update`) and checked by a test; never edit it by hand. The website lives in a separate repository (`hive-landing`). There is no localized documentation.
- **Bundled agent skill:** `skills/hive/SKILL.md` is planned for M5 and does not exist yet. Report the agent-skill check as `NOT APPLICABLE` until it does, and audit `.agents/skills/` only if the user asks.
- **Required checks:** `make check` (vet, golangci-lint, `-race` tests, `go mod tidy -diff`), `make fuzz`, `make snapshot`; CI runs `lint`, `fuzz`, `test (macos-latest)`, `test (ubuntu-latest)` and `release-snapshot`.
- **Performance evidence:** `BenchmarkInputLatency16Panes` (`go test ./internal/tui/mux -run '^$' -bench InputLatency`) reports keystroke-to-frame p50, p99 and max with 16 visible panes; `TestInputLatency16Panes` enforces the 10 ms p99 budget outside `-race`. No 1/15/50-pane workload benchmark exists; report that gap rather than inventing figures.

## Operating Rules

1. **Audit the selected candidate.** Never substitute the latest `main` state for the actual release range.
2. **Follow the repository's real release process.** Inspect existing scripts, configuration, CI workflows, and documentation instead of assuming another project's paths or tooling.
3. **Use evidence.** Verify findings against commits, diffs, source code, tests, documentation, and release metadata.
4. **Keep the audit read-only by default.** Do not edit files unless the user explicitly asks you to apply fixes.
5. **Do not publish releases.** Creating tags, deploying documentation, or invoking release automation requires separate authorization.
6. **Be explicit about uncertainty.** Distinguish verified defects, missing evidence, and decisions requiring maintainer input.
7. **Do not fabricate results.** Never claim unrun tests passed, invent benchmark measurements, or assume unsupported features exist.
8. **Prioritize actionable findings.** Identify release blockers first and keep the final report concise.

## When Asked to Apply Fixes

Follow the reference workflow and keep changes limited to the explicitly authorized release-readiness findings.

- Write changelog entries based on the complete release inventory.
- Update the appropriate next-release documentation and examples.
- Correct stale bundled agent instructions when necessary.
- Preserve generated-file and publication boundaries.
- Run relevant validation checks and re-audit the affected findings.
- Report remaining blockers and checks that could not be completed.

Do not modify unrelated features, rewrite the release process, or finalize and publish a release unless separately requested.

## Final Output

Use the report structure defined in `references/hive-five-release-check.md`.

Always report:

- Release readiness and the selected base-to-candidate range.
- Changelog and documentation status.
- Issue references and potential premature closures.
- Agent-skill freshness.
- Build and distribution validation.
- Runtime performance evidence or an explicit `NOT CHECKED` status.
- Release-finalization status.
- Required actions before release.

Mark the release `READY` only when the required checks have sufficient evidence and no unresolved release blocker remains.