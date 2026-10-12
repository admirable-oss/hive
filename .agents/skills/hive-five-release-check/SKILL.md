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

Verify these against the repository before relying on them; they describe Hive as of v1.0.0-rc.1 (M9):

- **Default branch:** `main`. Milestones merge as pull requests (`M8/Extensibility`, `M9/Hardening`).
- **Release metadata:** `CHANGELOG.md` (one section per release; the next one is marked unreleased). Releases are `v*` tags; `.github/workflows/release.yml` runs goreleaser, which builds darwin and linux, amd64 and arm64 (`CGO_ENABLED=0`), signs `checksums.txt` (`tools/release-sign`, secret `HIVE_RELEASE_SIGNING_KEY`, variable `HIVE_RELEASE_PUBLIC_KEY`), marks `-rc.N` tags as pre-releases (the preview channel) and updates the Homebrew cask (`HOMEBREW_TAP_TOKEN`). The workflow refuses to publish without the signing key. Establish the base from the previous tag, or, before the first tag, from the previous milestone's merge into `main`, and say so in the report.
- **Version injection:** `-X …/internal/buildinfo.version|commit|date` in `.goreleaser.yaml` and the `Makefile`; only release builds set `releaseKey`, so source builds verify checksums but not signatures. The daemon also reports `api_level` (`protocol.APILevel`) in `hive status --json`; a change to methods, events or their meaning must raise it.
- **Frozen surface:** `api/v1/` holds the frozen wire API (`api.txt`, `schema.json`), CLI (`cli.txt`), configuration keys (`config.txt`) and Go SDK (`hiveapi.txt`). `go test ./... -run Frozen` fails on a removed or changed line (a break, not allowed in 1.x; see `docs/compatibility.md`) and on an unreviewed addition. Additions are accepted with `HIVE_UPDATE_SURFACE=1 go test ./... -run 'Frozen|Reference'`; check that the diff in `api/v1/` only adds lines.
- **Documentation:** `README.md`, `ROADMAP.md`, `CHANGELOG.md`, `SECURITY.md`, `docs/` (index in `docs/README.md`) and `docs/adr/`. Generated, checked by tests, never edited by hand: `docs/keybindings.md` (`go test ./internal/tui/keymap -update`), `docs/config.md` and `docs/api.md` (`HIVE_UPDATE_SURFACE=1`). The website lives in a separate repository (`hive-landing`). There is no localized documentation.
- **Bundled agent skill:** `skills/hive/SKILL.md`, printed by `hive --skill`. Check that every command it names still exists (`api/v1/cli.txt`).
- **Distribution:** `distribution/install.sh` (`HIVE_CHANNEL`, `HIVE_VERSION`), the Homebrew cask, `flake.nix` (its `vendorHash` is checked by `go test ./tools/nixhash`, built by CI's `nix` job), `hive update` (`--channel`).
- **Required checks:** `make check` (vet, golangci-lint, `-race` tests, `go mod tidy -diff`), `make fuzz`, `make snapshot`; CI runs `lint`, `fuzz`, `test (macos-latest)`, `test (ubuntu-latest)`, `nix` and `release-snapshot`. `.github/workflows/nightly.yml` fuzzes for 5 h per target, runs the multi-machine soak, the chaos soak (`TestChaos`, `HIVE_CHAOS`) and the performance budgets; report the latest nightly's result.
- **Performance evidence:** `make perf` enforces the budgets outside `-race`: `TestInputLatency16Panes` (10 ms p99), `TestFrameTime100Panes` (16 ms p99 frame with 100 streaming panes), `TestRuntime_IdleCPUWith100Panes` (under 1 % CPU). `make bench` and `make bench-compare` (benchstat against `main`) cover the emulator (`internal/vt`). Emulator throughput is limited upstream (ultraviolet `Buffer.DeleteLineArea`) to a few MB/s, below ADR 0001's 50 MB/s goal; report it as a known gap, not a regression, unless it got worse.

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