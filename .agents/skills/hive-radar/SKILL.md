---
name: hive-radar
description: Analyze Hive's open GitHub issues to identify engineering risks, recurring user pain, valuable opportunities, and the most impactful next actions. Use when reviewing the backlog, deciding what to fix next, prioritizing engineering work, or identifying issues that need investigation, ownership, or deferral.
---

# Hive Radar

Hive Radar scans the project's open GitHub issues and turns the backlog into actionable engineering decisions.

The goal is not to count issues or rank them by popularity. It is to identify what matters, understand why it matters, and recommend where engineering effort will have the greatest impact.

Use this skill only inside the Hive repository.

## Operating Principles

- **Prioritize impact over popularity.** A serious persistence bug with zero reactions outranks a popular feature request.
- **Investigate before ranking.** Read issue descriptions, reproduction steps, comments, linked pull requests, and related issues when available.
- **Think in terms of Hive's architecture.** Understand how an issue affects the runtime, sessions, panes, PTYs, SSH connections, coding agents, socket API, persistence, or user-facing TUI.
- **Separate evidence from assumptions.** Distinguish confirmed bugs from suspected regressions, feature requests, and incomplete reports.
- **Look for patterns.** Multiple issues describing the same failure may indicate a deeper architectural problem.
- **Recommend actions, not just priorities.** Every recommendation should make the next step clear.
- **Stay read-only by default.** Never modify GitHub issues or start implementation unless explicitly asked.

## 0. Know What Hive Ships

Read `ROADMAP.md` before judging issues. It lists the milestones in order, what each one delivered, and what is still planned. As of v0.5 (M3) Hive ships the daemon and shims (agents survive daemon restarts), sessions, environments, tabs, split layouts, the pane API, git worktrees and the multiplexer UI. Agent state detection, the agents-operating-agents CLI and MCP (M5), host-reboot persistence (M6), SSH and remote machines (M7) and plugins (M8) are planned, not shipped.

- An issue about a planned capability is a feature request or roadmap input, not a regression: weigh it against that milestone rather than marking it 🔴.
- An issue about shipped behaviour is weighed against the relevant ADR in `docs/adr/` when one exists. A report that contradicts an accepted decision is a `needs owner decision`, not a bug.

## 1. Connect to the Repository

Run this skill from the Hive repository.

Confirm the repository identity before querying issues:

```bash
gh repo view --json nameWithOwner,url
```

The expected repository is `admirable-oss/hive`.

If the GitHub CLI is unavailable or unauthenticated, use an available connected GitHub integration if one exists. Otherwise, report the access limitation.

Never silently inspect another repository or fabricate issue data.

## 2. Scan the Backlog

Retrieve the open issues for `admirable-oss/hive`.

Use GitHub integration tools when available. Otherwise, use the GitHub CLI:

```bash
gh issue list \
  --repo admirable-oss/hive \
  --state open \
  --limit 100 \
  --json number,title,body,state,createdAt,updatedAt,author,labels,assignees,comments,reactionGroups,url
```

Paginate or retrieve additional batches if more issues exist. Do not imply the entire backlog was reviewed when only a subset was accessible.

For issues that could materially affect prioritization, inspect the full issue and relevant discussion:

```bash
gh issue view <number> \
  --repo admirable-oss/hive \
  --comments
```

Inspect linked pull requests, related issues, and repository changes when relevant and accessible.

Avoid spending equal investigation effort on every issue. Investigate broadly enough to identify priorities, then examine high-impact and ambiguous candidates in greater depth.

## 3. Evaluate Engineering Impact

Assess each issue using the following dimensions.

### User impact

- Does it prevent a user from completing a core workflow?
- Does it cause crashes, data loss, lost work, or corrupted state?
- Does it affect many users or a particularly important workflow?
- Is there a safe workaround?
- Is the issue reproducible and supported by concrete evidence?

### Technical severity

Pay particular attention to Hive's critical functionality:

- **Runtime:** Startup, shutdown, lifecycle management, and runtime stability.
- **Sessions:** Creation, attachment, detachment, reconnection, and persistence.
- **Panes and PTYs:** Process execution, terminal I/O, pane management, and layout restoration.
- **Remote machines:** SSH connections, remote session behavior, reconnection, and failure recovery.
- **Coding agents:** Agent detection, lifecycle management, interactive prompts, process state, and supported integrations.
- **Socket API and CLI:** Command correctness, protocol behavior, automation reliability, and compatibility.
- **Persistence and recovery:** State restoration, restart behavior, unexpected termination, and recovery from interrupted operations.
- **TUI and usability:** Navigation, rendering, keybindings, accessibility, and interaction consistency.
- **Installation and distribution:** Build failures, packaging, configuration, upgrades, and platform compatibility.
- **Security:** Unsafe process execution, unintended access, secret exposure, and insufficient isolation.

These are investigation areas, not assumptions that every reported issue affects the corresponding subsystem.

### Engineering leverage

Consider whether the issue:

- Fixes a root cause behind multiple symptoms.
- Eliminates a recurring class of bugs.
- Unlocks a blocked workflow or important feature.
- Improves reliability across multiple platforms or integrations.
- Prevents future regressions through better tests or contracts.
- Introduces substantial maintenance cost relative to its value.

### Community signal

Use reactions, comments, duplicates, and recurring reports as supporting evidence.

Do not treat reactions as votes that determine priority. A low-signal infrastructure bug can be more important than a highly discussed cosmetic enhancement.

### Confidence

Assess whether the available evidence supports the proposed action.

- **High:** Clear reproduction, convincing evidence, or a confirmed regression.
- **Medium:** Credible report with incomplete reproduction or uncertain scope.
- **Low:** Vague description, missing context, or multiple plausible explanations.

Do not invent a confidence level based on information that is unavailable.

## 4. Detect Patterns and Hidden Risks

Look beyond individual issue titles.

Identify clusters involving:

- Repeated session loss or unreliable recovery.
- Similar failures across different terminal environments.
- Agent integrations breaking under comparable conditions.
- CLI and socket API inconsistencies.
- Remote machine failures sharing a likely cause.
- Configuration behavior that differs between fresh installations and upgrades.
- Missing tests around critical lifecycle transitions.
- Repeated feature requests that suggest a broader product need.

When several issues may share a root cause, group them conceptually and recommend investigating the underlying problem before implementing separate fixes.

Do not claim a common root cause without evidence. Label it as a hypothesis when it has not been verified.

If useful, mention related issue numbers in the reasoning so maintainers can investigate the cluster efficiently.

## 5. Assign a Recommendation

Every issue should receive one recommendation.

| Recommendation | Meaning |
|---|---|
| `fix now` | High-impact defect, serious regression, data-loss risk, security concern, or blocker requiring immediate attention. |
| `investigate` | Potentially important issue whose severity, cause, or reproducibility needs clarification. |
| `queue` | Valid issue worth scheduling, but not urgent enough to interrupt higher-priority work. |
| `defer` | Low-impact improvement or speculative request that can wait. |
| `close?` | Likely duplicate, obsolete report, already-resolved issue, or issue that may no longer apply. Verify before closing. |
| `needs owner decision` | Product direction, compatibility, or architectural trade-off requiring a maintainer's decision. |

Use the recommendation to express the next action, not merely to restate the issue's category.

### Priority lights

Use these lights in the final report:

- 🔴 **Immediate attention:** Critical user impact, credible data-loss risk, security exposure, release blocker, or high-confidence regression.
- 🟠 **Investigate:** Potentially serious issue requiring reproduction, diagnosis, or confirmation before implementation.
- 🟡 **Schedule:** Valuable feature, meaningful quality improvement, recurring user pain, or valid non-urgent bug.
- 🔵 **Defer:** Low-impact polish, speculative enhancement, or issue with insufficient value to justify current effort.
- ⚪ **Decision required:** Product or architectural decision that cannot be resolved through implementation work alone.

A priority light and a recommendation serve different purposes. The light communicates urgency; the recommendation identifies the next action.

Do not mark an issue 🔴 merely because it is old, popular, or labeled as a bug.

## 6. Calculate Backlog Signals

For each issue, collect the following where available:

- **Age:** Days since issue creation, calculated from the current date.
- **Reactions:** Total reaction count, optionally including a compact breakdown when informative.
- **Last activity:** Time since the most recent meaningful update.
- **Labels:** Relevant existing classifications.
- **Ownership:** Assigned maintainer, if any.
- **Related work:** Linked pull requests, duplicates, or potentially overlapping issues.

Use the actual issue creation timestamp to calculate age. Do not confuse age with inactivity.

Use total reactions when reaction data is available. If it is missing, report `N/A` rather than guessing.

A stale issue is not automatically invalid. A recent issue is not automatically urgent. Treat age and activity as context, not substitutes for impact.

## 7. Produce the Radar Report

The default output should be a concise Markdown table, ordered by urgency and engineering impact.

Use this format:

| Light | Recommendation | Issue | Age | Reactions | Why |
|---|---|---|---:|---:|---|
| 🔴 | fix now | [#123](https://github.com/admirable-oss/hive/issues/123) | 3d | 0 | Reproducible session-loss bug with no safe recovery path. |
| 🟠 | investigate | [#124](https://github.com/admirable-oss/hive/issues/124) | 12d | 4 | Agent becomes unresponsive after reconnect; reproduction is incomplete. |
| 🟡 | queue | [#125](https://github.com/admirable-oss/hive/issues/125) | 28d | 7 | Valuable workflow improvement requested by multiple users. |
| 🔵 | defer | [#126](https://github.com/admirable-oss/hive/issues/126) | 5d | 0 | Cosmetic TUI adjustment with limited functional impact. |

These rows are illustrative only. Never present them as actual issues unless retrieved from GitHub.

Keep issue numbers as Markdown links to the canonical issue URLs.

Write one short sentence before the table when necessary to establish scope, such as the number of open issues reviewed and the time of the scan.

Keep the `Why` column concise and evidence-based. Explain the impact or uncertainty rather than repeating the title.

Sort issues in this order:

1. 🔴 Immediate attention.
2. 🟠 Investigation needed.
3. 🟡 Worth scheduling.
4. ⚪ Requires an owner decision.
5. 🔵 Safe to defer.

Within each group, order by impact, confidence, and engineering leverage.

If the backlog is large, include the most actionable issues rather than producing an unwieldy table. State how many issues were reviewed and how many are shown. Offer a deeper breakdown when useful.

After the table, include at most one short note highlighting a cross-issue pattern, an important uncertainty, or the single most valuable next action.

Do not produce a long narrative unless the user requests a deeper analysis.

## 8. Optional Deep-Dive Mode

When the user asks for deeper analysis, expand beyond the default table.

Useful additional outputs include:

- **Top engineering bets:** The three to five actions likely to deliver the greatest reliability or product impact.
- **Root-cause clusters:** Related issues that may share a technical cause.
- **Quick wins:** Small, high-confidence fixes with meaningful impact.
- **Blocked workflows:** Issues preventing users from adopting or relying on Hive.
- **Maintenance debt:** Repeated reports suggesting fragile abstractions or missing tests.
- **Product opportunities:** Recurring requests that point toward a coherent feature rather than isolated enhancements.
- **Ownership gaps:** High-impact issues without an assigned maintainer or clear next step.
- **Release risks:** Open issues that could affect the next release, with supporting evidence.

Separate observed facts from hypotheses and proposed work. Do not estimate implementation effort or release impact without enough context.

## 9. Keep GitHub Read-Only

Hive Radar is an analysis and recommendation skill.

Unless the user explicitly authorizes a separate action:

- Do not comment on issues.
- Do not assign issues.
- Do not add or change labels.
- Do not close or reopen issues.
- Do not create pull requests.
- Do not modify source code.
- Do not start implementing recommendations.

A recommendation to close an issue is not permission to close it. A recommendation to fix a bug is not permission to modify the repository.

If the user asks to implement a selected recommendation, transition to the appropriate implementation workflow and confirm the relevant scope before making changes.

## 10. Handle Incomplete Information Honestly

If GitHub access fails, explain what could not be retrieved.

If only some issues were accessible, state the coverage limitation.

If an issue's severity is uncertain, recommend investigation instead of overstating urgency.

If a pull request appears to resolve an issue, verify its status and relevance before calling the issue resolved.

If the evidence supports multiple interpretations, explain the uncertainty briefly and recommend the next step that would resolve it.

Never fabricate issue titles, creation dates, reaction counts, linked pull requests, reproduction results, or implementation status.

## Definition of Done

A successful Hive Radar run:

- Confirms the correct repository.
- Retrieves and accurately scopes the open-issue backlog.
- Identifies the issues with the greatest engineering impact.
- Uses evidence instead of popularity alone.
- Surfaces related issues and potential root-cause patterns where supported.
- Assigns a clear recommendation and priority to each reported issue.
- Produces a concise, decision-first Markdown report.
- Leaves GitHub and the repository unchanged.