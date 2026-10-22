# RFC 0002: Tasks and plans

- **Status:** Proposed
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`task.*`, `agent.wait`), CLI (`hive task`, `hive machine set`), Go SDK, MCP tools, config (`[tasks]`), observer API, on-disk state (`tasks/`, `machines.json`)

## Summary

M22 gives Hive a task model. A *task* is a prompt for an agent kind, run in
an environment or a fresh worktree, with a completion condition and a time
budget. A *plan* is a set of tasks whose `needs` form a DAG. The daemon
runs a plan's tasks in parallel within the plan's limits, per-kind caps and
each machine's capacity, places them on this machine and on saved machines
by label, stops before tasks that need a person's approval, and keeps every
task's result. Plans survive a daemon restart. Everything is additive.

## Motivation

Agents can already start, prompt and wait for each other (M5), and users
script that today: start N agents in worktrees, prompt each, wait on each,
read each screen. Every script reinvents the same parts badly: ordering, a
cap on how many run at once, retries, a timeout, collecting results, and
pausing for a human. A script also dies with its terminal, while the agents
it started keep running with nobody waiting for them. Hive owns the
agents, so it is where this belongs.

## Proposal

### Plan files

Plans are TOML (the format `hive task run` and `task.run` read), checked
strictly: an unknown key is an error, not a warning, because a mistyped
`needs` must not run a task early.

```toml
name = "session refactor"
max_parallel = 4          # tasks of this plan running at once (0: no cap)
timeout = "2h"            # the plan's budget: what is left is cancelled

[kinds]                   # per agent kind, tasks of this plan running at once
claude = 2

[defaults]                # anything a [[task]] may say, except id, prompt, needs
kind = "claude"
environment = "api"
isolate = true            # a new worktree on hive/<plan>/<task>
timeout = "30m"

[[task]]
id = "schema"
title = "Design the schema"
prompt = "Design the new session schema in docs/session.md."

[[task]]
id = "api"
needs = ["schema"]
approve = true            # waits for a person's yes before it starts
prompt = "Implement docs/session.md."
until = "command"         # done | idle | match | command
command = ["go", "test", "./..."]
labels = ["linux"]        # or machine = "build-box"
retries = 1
```

Validation rejects: no tasks, more than 500, duplicate or malformed IDs, a
missing prompt, kind or environment, an unknown `needs`, a task needing
itself, a cycle (reported with its path, `a → c → b → a`), `until = "match"`
without a compilable `match`, `until = "command"` without `command`, a bad
branch name, more than 10 retries, and negative budgets. Tasks are kept in
an order where needs come first, ties in the written order.

### Task states

```
waiting → (approval →) queued → running ⇄ blocked → succeeded | failed
```

Any unfinished task may be **cancelled**. A task whose need finished
without succeeding is **skipped** at once, even while its other needs still
run. A plan runs until every task has finished. It **succeeded** when every
task did, is **cancelled** when someone cancelled it or its budget ran out,
and **failed** otherwise.

A **blocked** agent (a permission prompt) pauses only its own task. The time
it spends blocked does not count against the task's budget.

### Completion conditions

| `until` | The task succeeds when |
|---|---|
| `done` (default) | the agent finishes the turn the prompt began |
| `idle` | the agent is ready for its next prompt |
| `match` | the agent's screen matches `match` (fails if the turn ends without a match) |
| `command` | after the turn, `command` run in the task's directory exits 0 |

A task fails when the agent exits, its screen stops changing while it works
(the stall timeout, 10 minutes), its budget runs out, or its check fails.
`retries` re-queues a failed attempt that many times. A person's
`task.retry` grants those retries again, and continues in the worktree the
first attempt made.

### Results

Each attempt leaves a `Result`, and every part of it is bounded so plans
stay small in memory, on disk and on the wire:

- `screen`: the agent's last 60 lines;
- `changed`: files changed since the commit the task started from,
  committed or not, plus new files (at most 200, with `changed_more`);
- `exit_code` and `output`: the check command's status and last 60 lines;
- `summary`: what the agent wrote to `$HIVE_TASK_SUMMARY` (at most 16 KiB).
  Unless `no_summary` is set, the prompt asks for this. The path is
  `.hive/tasks/<plan>-<task>.md` relative to the agent's directory, and is
  left out of `changed`.

A task also keeps its last 64 log entries. `hive task report` prints the
plan's report as readable text, and `--json` gives the same report as data.

### Limits and placement

A queued task starts when all of these allow it:

- its plan's `max_parallel` and `kinds`;
- the daemon's per-kind caps (`[tasks.kinds]`), across plans;
- a machine that fits: `machine` names one (`local` is this one), `labels`
  must all be on it, and it has a free slot. Capacity is `[tasks] capacity`
  here and `hive machine set <name> --capacity` for a saved machine.

Among machines that fit, the one with the most free slots wins, this
machine first on a tie. Older plans go first. A task no machine could ever
run fails at once with the reason.

A task placed on a saved machine runs there as a plan of one task, on that
machine's own daemon (`task.run` through the same ssh bridge `--machine`
uses). That daemon's runner starts the agent, makes the worktree on the
branch this plan names, checks the condition and gathers the result where
the files are. The placing daemon mirrors the remote task through `task.wait`
(`until: "change"`, 30-second polls). A machine unreachable for 5 minutes
fails the task. Its agent keeps working there, and `task.retry` resumes in
the same worktree. Cancelling cancels the remote plan.

Spend budgets need agents to report usage, which is M25.

### Approvals

A task with `approve = true` waits in `approval` once its needs succeeded.
The daemon publishes `task.approval`. The TUI raises it as it raises a
blocked agent (toast, OSC 9/777, desktop notification), and so does the
desktop app, which also has Approve and Refuse buttons. `task.approve`
starts the task. With `deny` it cancels the task instead, and what needs it
is skipped. `task.retry` asks again.

### Surviving a restart

Plans are saved as they change: one JSON file per plan, written atomically,
with changes made within 100 ms gathered into one write. A task records how
far its attempt got (`phase`: `starting`, `started`, `prompted`,
`checking`), the agent and directory, and its working time. Agents run under
shims and outlive the daemon, so the next daemon resumes each running task
on its machine without prompting it again. An agent's completion count
starts again after a restart, so a resumed wait ends when the agent is idle
(or done) rather than at a count. A remote task is resumed by its remote
plan's ID. An interrupted attempt is never treated as cancelled: only
`task.cancel` and plan budgets stop agents.

### Wire API (API level 12)

| Method | Params | Result |
|---|---|---|
| `task.run` | `{plan: PlanSpec}` or `{toml, source}` | `Plan` |
| `task.list` | `{}` | `[]Summary`, newest first |
| `task.get` | `{id}` (an ID or a unique prefix) | `Plan` |
| `task.wait` | `{id, task?, until?: finished\|attention\|change, after?, timeout_ms?}` | `{outcome, plan}` |
| `task.cancel` | `{id, task?}` | `Plan` |
| `task.retry` | `{id, task?}` | `Plan` |
| `task.approve` | `{id, task?, deny?, by?, note?}` | `Plan` |

The errors are `not_found`, `invalid_params` (a bad plan, or an ambiguous
prefix), `conflict` (approving a task that is not waiting, retrying one that
runs) and `unavailable`.

Events: `task.plan` (a `Summary`, with every change), `task.updated` (a
`TaskView`), `task.approval` (a `TaskView`) and `task.removed` (`{ids}`).
They are published in revision order: every plan has a `rev` that grows
with each change.

`agent.wait` gains `until: "unblocked"`: it waits until someone answers a
blocked agent.

Plugins: `task.list`, `task.get` and `task.wait` need `hive.read`.
`task.run`, `task.cancel`, `task.retry` and `task.approve` need
`hive.process`.

### CLI, SDK and MCP

- `hive task run [plan.toml | -] [--wait]`, or one task from flags (`-p`,
  `--kind`, `--env`, `--isolate`, `--check …`, `--approve`, …).
- `hive task list | get | wait | cancel | retry | approve | report`.
- `hive task wait` exits 0 when the plan succeeded, 1 when it did not, 4 on
  a timeout, and 5 when a task needs a person.
- `hive machine set <name> --label … --capacity N`.
- Go SDK: `RunPlan`, `RunPlanFile`, `Plans`, `Plan`, `WaitPlan`,
  `CancelPlan`, `RetryPlan`, `ApproveTask`, `ParsePlan` and `NewReport`, with
  type aliases.
- MCP: `task_run`, `task_list`, `task_get`, `task_wait`, `task_cancel`,
  `task_retry` and `task_approve`.

### Clients

- **TUI:** the Overview lists running plans and the latest three finished
  ones, with their progress and the tasks waiting for a person. Plan
  summaries arrive whole in events, so following them needs no extra reads.
- **Desktop:** a Plans section with progress, Approve and Refuse, a way to
  the pane of a blocked task, and Cancel. Plans on saved machines appear
  too.
- **Web observer** (read-only): a Plans table. Observers see tasks without
  their prompts, results or logs, and only the tasks in their scope. Plan
  counts across environments (`task.plan`) reach only observers that see
  everything.

### Configuration

```toml
[tasks]
capacity = 8        # tasks running on this machine at once, across plans
labels = []         # what tasks' labels select this machine by
keep_plans = 100    # finished plans kept; the oldest are forgotten first

[tasks.kinds]
claude = 4          # per kind, across plans
```

## Compatibility

- Only additions. No method, field, flag or key changes meaning.
- **An older client with a newer daemon** sees nothing new. **A newer client
  with an older daemon** gets `unknown_method` for `task.*`. The TUI, the
  desktop app and the web observer treat that as "no plans". The API level
  is now 12, so `hive` replaces a running daemon older than itself after an
  upgrade.
- **On-disk state:** `tasks/<plan>.json` is new, at schema 1. A newer
  schema is refused and the file left alone, and an unreadable file is moved
  aside to `*.corrupt-<time>`. `machines.json` gains optional `labels` and
  `capacity`. Its schema stays 1, since older releases ignore the fields, but
  a machine saved again by an older release loses them. That is recorded in
  [docs/compatibility.md](../docs/compatibility.md#on-disk-state).
- **A saved machine running an older hive** cannot take tasks. A task
  placed there fails with "the hive on <machine> does not run tasks; update
  it".
- **Observer API:** `Snapshot.plans` and three event types are added.
  Clients that do not know them ignore them.

## Security and privacy

- Running a plan is running agents. `task.run` needs the same capability as
  `agent.start` (`hive.process` for plugins), and the control socket's
  permissions are unchanged.
- Check commands run as the daemon's user in the task's directory, as
  `pane.run` and agents already do. They are part of the plan file, which
  the user wrote or approved.
- Observers never see prompts, reasons, results or summaries. Scoped
  observers see only the tasks in their scope, and counts only of those.
- Remote placement uses the saved machine's pinned identity, as
  `--machine` does. Disabled machines and machines whose identity changed
  are never used.
- Nothing leaves the machine except to the saved machines the user placed
  tasks on. There is no telemetry.

## Testing

- Unit tests (`internal/task`), all under `-race` with goleak: plan parsing
  and validation (19 kinds of bad plan), DAG ordering, placement limits and
  labels, retries and skipping, approvals, cancellation, plan budgets,
  restart and resume, forgetting old plans, waits, and the runner against a
  fake agent (blocked and unblocked, every condition, budgets, resume
  without a second prompt, cancel versus interrupt, retry in the same
  worktree).
- `FuzzParsePlan`: whatever parses also validates, in dependency order.
- `git.Head` and `git.Changed` against real repositories.
- **M22's acceptance, end to end** (`TestM22PlanAcrossMachines`, with a
  real daemon, shims, a fake Codex and a second machine over a fake ssh): a
  plan of 10 tasks with dependencies runs across 2 machines within
  `max_parallel` 3 and each machine's capacity 2. It survives
  `hive daemon restart`, pauses one task for a blocked agent and one for an
  approval, and retries a failed check without disturbing the other tasks.
  Its report holds every task's screen, changed files and summary.
- Clients: the TUI Overview and its notifications, the desktop core and its
  frontend, and the web observer's model and its scoping.

## Alternatives

- **Running remote tasks through `agent.*` from the placing daemon.** The
  worktree, the check command, the changed files and the summary all live on
  the other machine, so each would need a new remote file and exec API.
  Running the task with the other daemon's own runner needs only `task.*`,
  and survives either daemon restarting.
- **A workflow engine (M24) first.** Workflows are plans that a trigger
  starts. The task model comes first, and M24 builds on it.
- **YAML plans.** Hive's configuration and manifests are TOML, and its
  strict decoder catches typos.
- **Persisting agents' completion counts.** A resumed wait ends on
  idle-or-done instead, which needs no change to agent detection.

## Open questions

1. Should a plan be able to run another plan as a task (sub-plans), or is
   that M24?
2. Should approvals be possible per attempt (before a retry) as well as per
   task?
3. Spend budgets: what should a plan do when an agent stops reporting usage
   midway (M25)?

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
