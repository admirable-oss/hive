# RFC 0005: Agent insights, on your machine

- **Status:** Proposed
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`insights.*`), observer API (`observe.insights`), CLI (`hive insights`), Go SDK, MCP tools, config (`[insights]`, `[retention] insights_days`), task scheduling (budget holds), plugin capabilities, on-disk state (`insights/`), TUI, desktop app and web observer

## Summary

M25 turns what Hive already sees into answers about time and usage:

- **Time.** How long each agent spent working, blocked, idle or done, per
  agent, kind, environment, task and day. How long it waited on you, and
  what it asked.
- **Usage.** Tokens and cost where an agent reports them, per kind and
  model, with the source of each number. When an agent does not report,
  usage is *unknown*, never estimated.
- **Budgets.** Limits on cost, tokens or working time per day, plan,
  environment or kind. A budget warns as it nears its limit, and can hold
  new tasks once the limit is reached.

Everything stays in `HIVE_HOME`, under the retention settings. Nothing is
sent anywhere: data leaves the machine only when a person exports it, or
an observer they gave a credential reads it. Everything is additive.

## Motivation

People running several agents ask the same questions every day:

- where did the afternoon go;
- which agent sat waiting for an answer;
- what did this plan cost;
- did anyone notice the agent that has been burning tokens for an hour?

The answers are scattered across agents' own status lines, provider
dashboards and memory. Hive sees every state change of every agent and
every task, so it can answer the time questions exactly. It can answer the
usage questions wherever an agent tells it. Doing it locally keeps a
developer's work pattern on their machine.

## Proposal

### Time in state

The daemon follows `agent.state`. Each state an agent is in becomes a
**span**:

- its agent, kind, name, environment and pane;
- its plan and task, when the agent works on one;
- its state, its start and end;
- for `blocked`, the reason Hive read, which is what the agent asked.

A span ends when the state changes or the agent exits. Spans are split at
local midnight, so every span belongs to one day. A task waiting for
approval (M22) is also a span, in state `approval`, from `task.approval`
until a person decides.

**Waiting on you** is the time agents spent `blocked` plus the time tasks
waited for approval. **Asks** are those spans, each with what was asked
and how long it waited.

Across a daemon restart, agents keep running under shims, but nobody
watches their screens. The open spans are saved, and closed at the moment
the daemon stopped. The next daemon opens new spans from each agent's
state when it starts. The time between is not counted.

### Usage

Usage is **tokens** (input, output, cache reads, cache writes) and
**cost** in US dollars. Each number carries its **source**.

| Source | How | What it gives |
|---|---|---|
| `transcript` | the agent's Stop and SessionStart hooks (`hive integration install`) pass the session's transcript path; Hive reads it | tokens and model: Claude Code's assistant messages (`message.usage`, once per message ID), Codex's `token_count` events (`total_token_usage`) |
| `statusline` | `hive insights statusline`, as Claude Code's status line command, reads what Claude Code passes it | cost (`cost.total_cost_usd`) and model |
| `report` | `hive insights report`, or the `insights.usage` method, from any script, hook or plugin | whatever it says |

- **Session totals.** Agents report totals for their session.
  - Hive keeps the highest total seen per agent, session and field, and
    records only what grew.
  - A total that went down is a new session.
  - Two sources reporting the same session's total therefore never count it
    twice.
  - A report can instead give an increment (`total: false`), which is
    added.
- **Unknown.** A kind, day or task with no report shows usage as
  *unknown*, not zero. Hive keeps no price table and estimates nothing. A
  cost appears only where something stated it.

### Budgets

```toml
[[insights.budget]]
name = "daily"
per = "day"               # day | plan | environment | kind
cost = 25.0               # US dollars
tokens = 20000000
working = "10h"           # agents' working time
warn_at = 0.8             # warn at 80% of any limit
action = "pause"          # warn (default) | pause

[[insights.budget]]
name = "api plans"
per = "plan"
environment = "api"       # only what runs in this environment
cost = 5.0
```

- **Scope.** A budget applies to each key of its `per` separately:
  - `day`: today, for everything;
  - `plan`: each plan, over its life;
  - `environment`: each environment, today;
  - `kind`: each agent kind, today.

  `environment` and `kind` narrow which records count.
- **Levels.** A budget's fraction is its highest share of any limit.
  - At `warn_at` it raises a **warning**; at 1 it is **exceeded**.
  - Each level is raised once per key and window: an `insights.alert`
    event, a TUI toast, and desktop, web and push notifications.
  - Alerts are kept with the records.
- **Pause.** An exceeded budget with `action = "pause"` holds new work in
  its scope:
  - queued tasks of plans (M22), and so workflow agent steps (M24), wait in
    `queued` with the reason "held by budget daily";
  - agents already working are never stopped.

  The hold ends when the window does (a new day), or when a person runs
  `hive insights resume <budget> [key]`. That resume holds until the window
  ends.

Unknown usage cannot exceed a cost or token limit. A budget over cost for
agents that report none warns once that it cannot be enforced.

### Storage and privacy

```
<root>/insights/<YYYY-MM-DD>.jsonl   spans, usage and alerts of a local day, appended
<root>/insights/state.json           open spans, session totals, alerts raised, holds
```

- **Retention.** Day files older than `[retention] insights_days` (90) are
  deleted.
- **What is kept.** Records hold IDs, kinds, states, times, numbers, and
  the reasons agents gave when blocked. They hold no prompts, screens or
  transcripts.
- **What leaves the machine.** Nothing, unless a person exports it:
  - Hive has no telemetry;
  - transcripts are read in place, and only token counts are kept.
- **Export.** `hive insights export` writes CSV or JSON for a range, as
  spans, usage, alerts, or the summary.
- **Observers.** A credential reads `observe.insights`: the summary and
  budgets of the environments and panes in its scope only. Asks' reasons
  need the `screens` permission, since they come from agents' screens.

### Wire API (API level 15)

| Method | Params | Result |
|---|---|---|
| `insights.summary` | `{from?, to?, by?, kind?, environment?}` | `Summary`: rows by `kind` (default), `agent`, `environment`, `task`, `day` or `model`, and a total |
| `insights.asks` | `{from?, to?, kind?, environment?, limit?}` | `[]Ask`, newest first |
| `insights.usage` | `{id, session?, model?, source?, input?, output?, cache_read?, cache_write?, cost_usd?, total?}` | `UsageResult` (what was recorded) |
| `insights.budgets` | `{}` | `[]BudgetStatus` |
| `insights.resume` | `{budget, key?}` | `[]BudgetStatus` |
| `insights.export` | `{from?, to?, what: spans\|usage\|alerts}` | `[]Record` |

- **Ranges.** `from` and `to` are times. The default is today, local
  time. Ranges are capped at the retention period.
- **Events.** `insights.alert` (an `Alert`) and `insights.resumed`.
- **Plugins:**
  - `hive.read`: `summary`, `asks`, `budgets` and `export`;
  - `hive.write`: `usage`;
  - **closed:** `insights.resume`. Only a person lifts a hold on spending.
- **Observers:** `observe.insights` `{from?, to?, by?}`, scoped as
  described above.

### CLI, SDK and MCP

- `hive insights [--since 7d | --since 2026-10-01] [--by kind|agent|environment|task|day|model]`
  prints time in each state, waiting, asks, tokens and cost.
- `hive insights asks`, `budgets`, `resume <budget> [key]` and
  `export --format csv|json --what spans|usage|alerts|summary [-o file]`.
- `hive insights report --input N --output N --cost 1.2 [--total]`
  reports usage for the pane it runs in (`$HIVE_PANE_ID`).
- `hive insights statusline` is Claude Code's status line command: it
  reports cost and model, and prints a short status line. Hooks installed
  by `hive integration install` already report transcripts' tokens; they
  need no change.
- **Go SDK:** `InsightsSummary`, `Asks`, `ReportUsage`, `Budgets`,
  `ResumeBudget` and `ExportInsights`.
- **MCP:** `insights_summary` and `insights_budgets`, both read-only.

### Clients

- **TUI:** the Overview has an Insights section for today:
  - each kind's working, blocked and idle time;
  - time waiting on you;
  - tokens and cost, or "unknown";
  - budgets near or over their limit.

  Alerts raise a toast and a notification.
- **Desktop:** an Insights window with today's summary by kind, the asks,
  and the budgets (with Resume for a held one). Alerts raise notifications.
- **Web observer:** an Insights section with the summary of what the
  credential sees.

### Configuration

```toml
[insights]
enabled = true            # record spans and usage (off: nothing is recorded)

[[insights.budget]]       # any number
name = "daily"
per = "day"
cost = 25.0

[retention]
insights_days = 90
```

## Compatibility

- Only additions. No method, field, flag or key changes meaning.
- The API level is now 15.
  - **An older client with a newer daemon** sees nothing new.
  - **A newer client with an older daemon** gets `unknown_method` for
    `insights.*`. The TUI, the desktop app and the web observer show no
    insights.
- **On-disk state:**
  - `insights/state.json` is new, at schema 1. A newer schema is refused
    and the file never written over; an unreadable file is moved aside.
  - Day files are JSON lines with a `v` field. Lines a build does not
    understand are skipped and counted.
- **Plans:** a task held by a budget is `queued`, as it would be waiting for
  a slot, with a reason. Plans saved by earlier releases are unaffected.
- **Observer API:** `observe.insights` is new; existing methods and events
  are unchanged.

## Security and privacy

- **No telemetry.** Insights are never sent anywhere by Hive. Exports are
  files or output the person asked for. Observers read only through
  credentials the person created, scoped as they chose.
- **No content.** Prompts, screens and transcripts are not stored.
  Transcripts are read where the agent keeps them, for counts only, and only
  when the agent's own hook passes the path. Blocked reasons are kept,
  because they are what "what you were asked" means. They are short and
  shown only to the person and to observers with `screens`.
- **Spending holds.** A paused budget holds new tasks, but it cannot be
  lifted by a plugin or an MCP tool. It never stops an agent that is
  already working, which would lose work.
- **Usage reports.** Reports through `insights.usage` (and plugins with
  `hive.write`) can be wrong. Each number keeps its source, so a dashboard
  can tell reported from read.

## Testing

- **Spans:** state changes, midnight splits, exits, approvals, task
  attribution, restarts (open spans closed at the stop), and an injected
  clock.
- **Usage:** session totals across sources without double counting,
  increments, new sessions, unknown versus zero, and the Claude Code and
  Codex transcript formats (with fixtures). Malformed transcripts are
  fuzzed.
- **Summaries:** every `by`, ranges across days, open spans counted to
  now, and filters.
- **Budgets:** every `per`, filters, warn and exceed raised once per
  window, pause holding queued tasks (and only in scope), resume, and the
  window ending.
- **The store:** appends, retention, newer schemas, corrupt state, and
  unknown lines skipped.
- **Observer scope:** rows outside a credential's scope never appear, and
  reasons need `screens`.
- **CLI:** tables, CSV and JSON exports, `report`, `statusline`.
- **TUI, desktop and web:** their insights views.
- **M25's acceptance, end to end** (`TestM25Insights`, with a real daemon,
  shims and a fake Codex):
  - agents work, block on a question and are answered, in tasks of a plan;
  - one reports usage through its hook's transcript, another through
    `hive insights report`, and a third reports nothing;
  - `hive insights` shows each kind's time in state, the waiting with what
    was asked, each kind's usage with its source, and *unknown* for the
    silent one;
  - a pausing budget raises its warning and exceeded alerts, and holds the
    next plan's task until `hive insights resume`;
  - `hive insights export` writes CSV and JSON;
  - and nothing is written outside `HIVE_HOME`.

## Alternatives

- **Estimating cost from tokens with a price table.** Prices change, differ
  by plan and contract, and would be wrong without anyone noticing. Hive
  shows what was stated, and says unknown otherwise.
- **Reading every agent's screen for numbers.** Status lines differ by
  version and are easily misread. The transcripts and the status line
  command are the agents' own, documented outputs.
- **A time-series database.** A day of spans is a few thousand lines.
  Appending JSON lines per day keeps the data greppable, exportable and easy
  to delete, with nothing new to run.
- **Stopping agents over budget.** Stopping an agent mid-change loses work
  and leaves files half-written. Holding new work is safe, and a person
  decides about the rest.

## Open questions

1. Should budgets apply across machines, adding up the usage of tasks
   placed on saved machines (M22) from their own insights?
2. Should Hive learn usage formats from manifests (`[usage]` rules over the
   screen) for agents with neither hooks nor transcripts?
3. Should a held budget also refuse `agent.start`, or only tasks?

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
