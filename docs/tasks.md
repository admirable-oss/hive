# Plans: many agents, one plan

A **plan** is a set of tasks for coding agents. Each **task** is a prompt
for an agent kind, run in an environment or a fresh worktree, with a
completion condition and a time budget. `needs` orders the tasks. Hive runs
them in parallel within the plan's limits, places them on this machine and
on saved machines, stops before the tasks a person must approve, and keeps
every task's result: its last screen, the files it changed, its exit status
and the summary its agent wrote. Plans survive a daemon restart.

```sh
hive task run plan.toml              # prints the plan's ID
hive task list                       # progress, and what waits for you
hive task wait 3f2a --until attention
hive task approve 3f2a deploy
hive task report 3f2a                # every task's outcome
```

Plans are also in the TUI's Overview (prefix `O`), in the desktop app's
sidebar (with Approve and Refuse buttons), in the web observer (read-only),
in the MCP tools (`task_run`, `task_wait`, …) and in the Go SDK
(`hiveapi.Client.RunPlan`). The design is in
[RFC 0002](../rfcs/0002-task-orchestration.md).

## A plan file

```toml
name = "session refactor"
max_parallel = 4          # tasks of this plan running at once (0: no cap)
timeout = "2h"            # the plan's budget: what is left is cancelled

[kinds]                   # per agent kind, tasks of this plan running at once
claude = 2

[defaults]                # anything a [[task]] may say, except id, prompt and needs
kind = "claude"
environment = "api"
isolate = true            # each task in a new worktree on hive/<plan>/<task>
timeout = "30m"           # each task's budget

[[task]]
id = "schema"
title = "Design the schema"
prompt = "Design the new session schema in docs/session.md."

[[task]]
id = "api"
needs = ["schema"]        # starts once schema has succeeded
approve = true            # and a person has said yes
prompt = "Implement docs/session.md."
until = "command"
command = ["go", "test", "./..."]
labels = ["linux"]        # on a machine with these labels
retries = 1
```

| Key | Meaning |
|---|---|
| `id` | The task's name in its plan, in `needs`, and in its branch |
| `prompt` | What the agent is asked to do |
| `kind` | The agent: `claude`, `codex`, … (`hive agent manifests`) |
| `environment` | The environment it runs in, or makes its worktree of |
| `worktree` | A branch: run in a new worktree on it |
| `isolate` | Run in a new worktree on `hive/<plan>/<task>` |
| `base` | Where a new worktree branch starts (default `HEAD`) |
| `needs` | Tasks that must succeed first |
| `until`, `match`, `command` | The completion condition (below) |
| `timeout` | How long the agent may work on it, not counting time blocked |
| `approve` | Wait for a person's approval before starting |
| `machine`, `labels` | Where it may run (below) |
| `retries` | How many times a failed attempt is tried again by itself |
| `args`, `env` | Extra arguments and environment variables for the agent |
| `no_summary` | Do not ask the agent for a summary |
| `keep_agent` | Leave the agent running when the task ends |

A plan file is read strictly: an unknown key is an error, so a typo never
runs a task early. A cycle in `needs` is reported with its path. A boolean
set to true in `[defaults]` holds for every task.

One task needs no file:

```sh
hive task run -p "Fix the flaky upload test" --kind codex --env api --isolate \
  --check go --check test --check ./upload/... --wait
```

## When a task is done

| `until` | The task succeeds when |
|---|---|
| `done` (default) | the agent finishes the turn the prompt began |
| `idle` | the agent is ready for another prompt |
| `match` | the agent's screen matches `match`, a regular expression (it fails if the turn ends without one) |
| `command` | after the turn, `command` run in the task's directory exits 0 |

A task fails when its agent exits, its screen stays still for 10 minutes
while it works, its budget runs out, or its check fails. A **blocked**
agent, one waiting for a permission prompt, pauses only its own task. Answer
it in its pane, or with `hive agent send-keys`, and the task goes on.

## States

```
waiting → (approval →) queued → running ⇄ blocked → succeeded | failed
```

A task whose need did not succeed is **skipped**. Any unfinished task can be
**cancelled**. `hive task retry <plan> [task]` runs failed, cancelled and
skipped tasks again. A retry of one task brings back what was skipped
because of it, and continues in the worktree its first attempt made.

## Where tasks run

A queued task starts when its plan's `max_parallel` and `[kinds]`, the
daemon's `[tasks.kinds]` and a machine's capacity all have room. A task runs
on this machine (`local`) or on a saved machine:

```sh
hive machine set gpu --label gpu --label linux --capacity 2
```

`labels = ["gpu"]` places a task only on machines with every one of those
labels, and `machine = "gpu"` on that machine alone. Without either, a task
runs wherever there is room, this machine first. This machine's capacity
and labels are `[tasks] capacity` and `labels`.

On a saved machine, the task runs on that machine's own daemon: the agent,
its worktree, its check and its result are all there. That machine needs a
hive with plans (1.3 or later). If it stays unreachable for 5 minutes the
task fails, but its agent keeps working there, and `hive task retry` goes on
where it was.

## Results

```sh
hive task report 3f2a --screen 10     # adds each task's last 10 screen lines
hive --json task report 3f2a          # the same, as data
hive task get 3f2a api                # one task: its log and result
```

Each task keeps:

- its agent's last 60 lines;
- the files changed since the commit it started from, committed or not;
- its check's exit status and last lines;
- the summary its agent wrote.

The prompt asks the agent to write that summary to `$HIVE_TASK_SUMMARY`, a
file under `.hive/tasks/` in its directory. Agents also see `$HIVE_PLAN_ID`
and `$HIVE_TASK_ID`. A task's agent is stopped when the task ends, unless
`keep_agent` is set, and its pane keeps the last screen.

## Waiting from scripts

`hive task wait <plan> [task]` exits:

| Status | Meaning |
|---|---|
| 0 | the plan (or task) succeeded |
| 1 | it finished without succeeding |
| 4 | `--timeout` passed |
| 5 | with `--until attention`: a task waits for approval, or its agent is blocked |

## Spend

A plan's `timeout` limits its time. To limit what its agents spend, a
budget with `per = "plan"` counts each plan's reported cost, tokens or
working time over its life, and a `pause` budget holds the plan's queued
tasks once it is over ([insights](insights.md#budgets)). Tasks already
running go on.

## Configuration

```toml
[tasks]
capacity = 8        # tasks running on this machine at once, across plans
labels = []         # what tasks' labels select this machine by
keep_plans = 100    # finished plans kept; the oldest are forgotten first

[tasks.kinds]       # per agent kind, tasks running at once across plans
claude = 4
```

Plans are kept in `tasks/<plan>.json` under the session's directory.
