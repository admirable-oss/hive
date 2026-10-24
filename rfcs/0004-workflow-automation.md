# RFC 0004: Workflows, from a trigger to a report

- **Status:** Proposed
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`workflow.*`), CLI (`hive workflow`), Go SDK, MCP tools, config (`[workflows]`, `[retention] workflow_runs`), plugin capabilities and action context, on-disk state (`workflows/`), TUI and desktop app

## Summary

M24 lets a person write down work they repeat as a **workflow**: a TOML
file of steps that are agents' tasks, commands or plugin actions, with
inputs, outputs, conditions and loops. A workflow runs when one of its
**triggers** fires: a Hive event (an agent done or blocked, a worktree
created), a schedule, a git event (a push, a new branch), or a webhook a
plugin received. It also runs on request, and headless in CI.

The daemon records every **run** with the workflow's text, its inputs and
the trigger that started it, retries steps with backoff, enforces timeouts,
cancels on request, and resumes runs after a restart. Everything is
additive.

## Motivation

Plans (M22) run once, from a file a person submits. Much agent work is the
same every time, and starts from something that happened:

- review every branch pushed to the shared remote;
- when the tests fail on a branch, hand the failure to an agent to fix;
- every night, have an agent update the dependencies and run the tests;
- when an agent stops on a permission prompt, notify a channel.

Today each of these is a script around `hive events`, `hive task run` and
cron, written again by each person, and dying with its terminal. Hive
already sees the events, runs the agents and keeps the results, so it is
the place to run these, the same way each time.

## Proposal

### Workflow files

A workflow is a TOML file. The format is versioned (`version = 1`, required)
and checked strictly: an unknown key is an error, as in plan files.

```toml
version = 1
name = "review-push"             # default: the file's name
description = "Review what was pushed, test it, and fix it if the tests fail."
timeout = "2h"                   # the run's budget
concurrency = "queue"            # queue (default) | parallel | skip
environment = "api"              # steps' default environment

[inputs.branch]
description = "the branch to review"
default = "main"

[[on]]
git = "push"
repo = "~/src/api"
branches = ["main", "release/*"]
[on.inputs]
branch = "${{ trigger.branch }}"

[[step]]
id = "review"
agent = ["claude", "codex"]      # the first kind this machine has
prompt = "Review the changes on ${{ inputs.branch }}. Write your findings to the summary."
isolate = true
timeout = "30m"

[[step]]
id = "test"
run = ["go", "test", "./..."]
timeout = "20m"
retries = 1
backoff = "10s"
on_failure = "continue"

[[step]]
id = "fix"
if = "steps.test.outcome == 'failure'"
agent = "any"
prompt = """
The tests fail:
${{ steps.test.output }}
Fix them."""

[[step]]
id = "retest"
if = "steps.fix.outcome == 'success'"
run = "go test ./..."

[outputs]
review = "${{ steps.review.summary }}"
```

The file is checked as it is read. These are rejected:

- a missing or unknown `version`;
- no steps, or more than 200;
- duplicate or malformed IDs, or an unknown `needs`;
- a cycle, reported with its path;
- a step that is not exactly one of `agent`, `run` or `action`;
- an agent step without a prompt;
- a bad duration, cron line or glob;
- an expression that does not parse;
- a trigger that is not exactly one of `event`, `schedule`, `every`, `git`
  or `webhook`;
- an input a trigger fills that the workflow does not declare.

### Steps

A step runs after the steps it `needs`. A step without `needs` runs after
the one written before it, so a list of steps runs in order. `needs = []`
starts a step at once. Steps whose needs have finished run in parallel.

| Kind | Written as | Runs |
|---|---|---|
| agent | `agent = "claude"`, a list, or `"any"` | a task (M22) of one: the prompt for an agent of that kind, in an environment or a fresh worktree (`isolate`, `worktree`, `base`), until its condition (`until`, `match`, `check`) |
| command | `run = ["argv", …]`, or `run = "a shell line"` | the command in the step's directory (`environment`, then `dir` relative to it), with `env` added |
| action | `action = "plugin/action"`, with `with = {…}` | a plugin's action, its `with` values in its context |

A step's **outcome** is `success`, `failure`, `skipped` or `cancelled`.

- `if` is a condition (see Expressions). The default is `success()`: every
  step this one needs succeeded. A false condition skips the step.
- A skipped step's dependents are skipped too, unless their own `if` says
  otherwise (`always()`, `failure()`, or a test on an outcome).
- `on_failure = "continue"` lets the run go on as if the step had
  succeeded. The step's outcome is still `failure`, and conditions can test
  it.

### Any provider

`agent` names kinds, not providers. A list is an order of preference;
`"any"` means every installed kind, in the order `hive agent manifests`
lists them.

When the step starts, the first kind this machine can run is chosen: its
manifest's start command is on the `PATH`. The choice is recorded in the
run. A step with no kind available fails with the kinds it looked for, so
a workflow written on a machine with Claude runs on one with only Codex.

### Inputs and outputs

- **Inputs** are declared under `[inputs.<name>]` with a `description`, a
  `default` and `required`. Values are strings. A run gets them from the
  request (`hive workflow run -i branch=main`) or from its trigger's
  `inputs`. An undeclared input, or a missing required one, is refused.
- **Step results.** Every step leaves:
  - `outcome` and `ok`;
  - `attempts`, `started_at`, `ended_at` and `reason`;
  - for a command: `exit_code` and `output` (its last 60 lines);
  - for an agent: `kind`, `summary`, `changed` (the files) and `screen`
    (its last lines);
  - for an action: `exit_code` and `output`.
- **Step outputs.** A command (or an action whose plugin passes the
  variable on) may write `name=value` lines, or `name<<END` … `END` blocks,
  to the file named by `$HIVE_OUTPUT`. They become `steps.<id>.outputs.<name>`.
  At most 64 KiB, as GitHub Actions does.
- **Workflow outputs** (`[outputs]`) are evaluated when the run ends, and
  are part of its report.

### Expressions

`${{ … }}` in prompts, commands, directories, `env`, `with` and trigger
`inputs` is replaced by the expression's value as text. `if` and a loop's
`until` are bare expressions.

The language is small, and the same input always gives the same value:

- **Values:** strings in single or double quotes, numbers, `true`, `false`
  and `null`.
- **Names:** `inputs.x`, `trigger.x`, `steps.<id>.x`, `run.id`,
  `run.workflow`, `run.dir`, `item` and `index`. Brackets read a key or an
  index: `steps['my-step'].outputs.x`, `trigger.commits[0]`.
- **Operators:** `== != < <= > >= && || !` and parentheses.
- **Functions:**
  - `success()`, `failure()`, `always()`, `cancelled()`;
  - `contains(a, b)`, `startsWith(a, b)`, `endsWith(a, b)`;
  - `join(list, sep)`, `len(x)`, `format('{0}-{1}', a, b)`;
  - `toJSON(x)`, `fromJSON(s)`.

A missing name is `null`. `false`, `null`, `""` and `0` are false. There
are no environment variables, no clock and no randomness in expressions.

### Loops

- `foreach` runs a step once per item: a list in the file, or an expression
  giving one (`fromJSON(inputs.services)`). Inside, `${{ item }}` and
  `${{ index }}` name the current one. Items run one after another. The
  step succeeds when every item does; `results` keeps each item's result.
- `repeat = { max = 3, until = "…" }` runs a step again until the condition
  holds, at most `max` times (at most 20). The condition sees the step's
  latest result as `steps.<id>`. A step whose condition never holds fails.

### Triggers

Each `[[on]]` table is one trigger, with optional `inputs` (expressions
over `trigger`) for the runs it starts.

| Trigger | Fires when | `trigger` holds |
|---|---|---|
| `event = "agent.state"` and `[on.match]` | a Hive event of that type (or prefix: `"branch."`) is published and every `match` key, a dotted path into its data, equals its value (`*` globs) | `type`, `seq` and the event's `data` |
| `event = "agent.done"` (also `agent.blocked`, `agent.idle`, `agent.exited`) | an agent enters that state: `agent.state` with `state` matched | the same, from `agent.state` |
| `schedule = "0 3 * * 1-5"` | a cron line: minute, hour, day of the month, month, day of the week, local time; ranges, lists, steps and names | `type`, `time` |
| `every = "15m"` | that much time since the last scheduled run (at least 1 minute) | `type`, `time` |
| `git = "push"` (`repo`, `branches`, `remote`) | a remote-tracking branch moves to the commit its local branch has: this repository pushed it | `repo`, `remote`, `branch`, `commit`, `before` |
| `git = "branch"` (`repo`, `branches`) | a local branch appears | `repo`, `branch`, `commit` |
| `webhook = "name"` | `workflow.fire` is called with that name, by a plugin that received a webhook or by a person | `name`, `data` (the payload) |

- **Git repositories.** Without `repo`, a git trigger watches every
  repository Hive has an environment in. Watched repositories' references
  are read every `[workflows] git_interval` (10 s), and soon after
  `environment.git` reports a change. The references seen are saved, so a
  push made while no daemon ran fires once the next daemon reads them.
- **Schedules.** A schedule that came due while no daemon ran does not fire
  late.
- **Feedback.** A run never triggers its own workflow, so a workflow that
  reacts to `workflow.run` events cannot loop on itself.
- **Concurrency.** `concurrency` says what happens when a trigger fires
  while a run of the workflow is still going:
  - `queue` (the default) runs it after, keeping at most 20 waiting;
  - `parallel` runs it now;
  - `skip` drops it.
- **Daemon limit.** At most `[workflows] max_runs` (4) runs run at once
  across workflows. The rest wait, oldest first.

### Where workflows come from, and trust

Workflows are read from:

- `~/.hive/workflows/*.toml`, the person's own;
- the files and directories listed in `[workflows] paths`, such as a
  repository's `.hive/workflows`. Files from `paths` are shared with
  whoever can change them.

A workflow from `paths` is **untrusted** until the person runs
`hive workflow trust <name>`. That records the SHA-256 of the file's text,
and its triggers fire only while the file still has that text. A changed
file is untrusted again until trusted again, so a commit to a shared
repository cannot add commands to a trigger. Running a workflow by hand
(`hive workflow run`) is the person's own command, and needs no trust.

Files are read again on `hive workflow reload`, when the daemon starts, and
every `git_interval` (by modification time). A file that does not parse is
listed with its error and never runs.

A workflow is a file, so sharing one is sharing a file. Packages in the
registry are M26's.

### Runs

A run records:

- the workflow's name, file and digest;
- its full text, as parsed when the run began;
- its inputs and its trigger (type, time and data);
- the directory it runs in (`run.dir`): the request's, the trigger's
  repository, or the repository the workflow file is in;
- every step: each attempt's resolved command or prompt (expressions
  replaced), and its results.

Running the same workflow text with the same inputs and trigger takes the
same steps. `workflow.rerun` starts a new run from a run's recorded text,
inputs and trigger. Changes to the file since do not apply.

```
queued → running → succeeded | failed | cancelled
```

A run **succeeded** when no step ended in `failure` (except those with
`on_failure = "continue"`) or `cancelled`. It is **cancelled** when
someone cancelled it or its budget ran out, and **failed** otherwise.

### Reliability

- **Retries.** `retries` re-runs a failed attempt up to 10 more times. The
  first waits `backoff` (default 5 s), and each later one waits twice as
  long, at most 10 minutes. The time of the next attempt is saved.
- **Timeouts.**
  - A step's `timeout` is an agent task's budget (time blocked on a person
    does not count, as in M22), or a command's or action's wall-clock time.
  - The run's `timeout` cancels what is left.
- **Cancellation.**
  - `workflow.cancel` cancels the run's agent tasks (stopping their agents)
    and stops its commands with everything they started.
  - An action gets `plugin.cancel`.
  - Steps that had not started are cancelled.

**Restarts.** Runs are saved as they change: one JSON file per run, written
atomically. The next daemon resumes every unfinished run:

- **Agent steps** are M22 tasks, and their agents run under shims, so a
  running agent step keeps working. The new daemon waits on the same plan.
- **Commands and actions** that were running are run again from the start.
  This is recorded as an interrupted attempt, which does not count against
  `retries`. Steps should therefore be safe to run twice.
- **Backoff waits** end at the saved time.
- **Queued runs** stay queued.

A daemon that stops does not cancel runs. Only `workflow.cancel` and run
budgets do.

### History

Finished runs are kept up to `[retention] workflow_runs` (200) and
`workflow_run_age` (30 days). The oldest are forgotten first, as runs
finish and at least hourly. A step keeps:

- its last 60 output lines, its outputs and its summary;
- its last 32 log entries, each at most 1 KiB;
- the agent task's plan ID, so `hive task report` shows the task while M22
  keeps the plan.

### Headless, for CI

```sh
hive workflow run .hive/workflows/ci.toml --headless -i branch="$GITHUB_REF_NAME"
```

- `--headless` starts a daemon of its own in a temporary `HIVE_HOME` and
  creates an environment for the current directory.
- It runs the workflow there, waits for it, prints its report (`--json`
  for the data), stops the daemon with its agents, and removes the
  directory.
- The exit status is 0 when the run succeeded, 1 when it did not, and 4 on
  `--timeout`.
- It is the same engine as the daemon's, so a workflow runs the same way
  in CI and on a laptop. Triggers do not fire in a headless run.

### Wire API (API level 14)

| Method | Params | Result |
|---|---|---|
| `workflow.list` | `{}` | `[]Workflow` (definitions, with any error and their trust) |
| `workflow.get` | `{name}` | `Workflow`, with its spec |
| `workflow.validate` | `{toml, source}` | `Spec` |
| `workflow.run` | `{name}` or `{toml, source}`, and `{inputs?, dir?}` | `Run` |
| `workflow.runs` | `{workflow?, limit?}` | `[]RunSummary`, newest first |
| `workflow.status` | `{id}` (an ID or a unique prefix) | `Run` |
| `workflow.wait` | `{id, timeout_ms?}` | `{outcome: finished\|timeout, run}` |
| `workflow.cancel` | `{id}` | `Run` |
| `workflow.rerun` | `{id}` | `Run` (a new one) |
| `workflow.fire` | `{webhook, data?}` | `[]RunSummary` (the runs it started) |
| `workflow.trust` | `{name, revoke?}` | `Workflow` |
| `workflow.reload` | `{}` | `[]Workflow` |

Errors:

- `not_found`;
- `invalid_params` (a bad workflow, input or prefix);
- `conflict` (cancelling a finished run, trusting a file that does not
  parse);
- `unavailable`.

Events:

- `workflow.run` (a `RunSummary`, with every change, in `rev` order);
- `workflow.step` (a `StepView`);
- `workflow.removed` (`{ids}`);
- `workflow.changed` (`{names}`, when definitions were read again).

Plugins:

- `hive.read`: `workflow.list`, `get`, `validate`, `runs`, `status` and
  `wait`;
- `hive.process`: `workflow.run`, `rerun`, `cancel` and `fire`, since they
  run agents and commands;
- `hive.admin`: `workflow.reload`;
- **closed:** `workflow.trust`. Only a person decides which shared files
  may run on their own.

An action run by a workflow step gets `workflow: {run, step, with}` in its
context. `trigger` stays `action`.

### CLI, SDK and MCP

- `hive workflow list | show | validate | run | runs | status | wait |
  cancel | rerun | report | fire | trust | reload`.
- `hive workflow run <name | file | ->` takes `-i name=value` (repeated),
  `--dir`, `--wait`, `--timeout` and `--headless`.
- `hive workflow run --wait`, `wait` and `report` exit 0 when the run
  succeeded, 1 when it did not, and 4 on a timeout.
- Go SDK, with type aliases:
  - `Workflows`, `Workflow`, `ValidateWorkflow`;
  - `RunWorkflow`, `RunWorkflowFile`, `WorkflowRuns`, `WorkflowRun`;
  - `WaitWorkflowRun`, `CancelWorkflowRun`, `RerunWorkflow`;
  - `FireWebhook`, `ParseWorkflow` and `NewWorkflowReport`.
- MCP tools:
  - `workflow_list`, `workflow_runs`, `workflow_status`, `workflow_wait`
    (read-only);
  - `workflow_run` (by name only);
  - `workflow_cancel`.

  No MCP tool trusts a workflow or runs workflow text an agent wrote.

### Clients

- **TUI:** the Overview has a Workflows section with the runs going on and
  the latest three that finished, each with its trigger and its steps'
  progress. A failed run raises a toast and a notification.
- **Desktop:** a Workflows section in the sidebar, with the runs, their
  steps, Cancel and Run again, and notifications for failed runs.

### Configuration

```toml
[workflows]
paths = []               # more workflow files or directories ("~/" is home)
max_runs = 4             # runs at once, across workflows
git_interval = "10s"     # how often watched repositories' references are read

[retention]
workflow_runs = 200      # finished runs kept
workflow_run_age = "720h"
```

## Compatibility

- Only additions. No method, field, flag or key changes meaning.
- The API level is now 14.
  - **An older client with a newer daemon** sees nothing new.
  - **A newer client with an older daemon** gets `unknown_method` for
    `workflow.*`. The TUI and the desktop app treat that as "no
    workflows", and `hive workflow` says the daemon is too old.
- **On-disk state:** `workflows/runs/<id>.json`, `workflows/trust.json` and
  `workflows/state.json` are new, at schema 1.
  - A newer schema is refused: nothing is loaded, and the file is never
    written over.
  - An unreadable file is moved aside to `*.corrupt-<time>`.
- **Plugin API 1:** the action context gains `workflow`, which plugins that
  do not know it ignore.
- **Workflow files** carry `version = 1`. A later version is refused by
  this build, with the version it reads.

## Security and privacy

- **Who runs what.** Workflows run as the daemon's user, like plan checks,
  branch gates and `pane.run`. Their commands come from the person's own
  files, or from shared files the person trusted at their current text.
- **Repositories.** Cloning or pulling a repository never adds a trigger:
  its workflows are read only when listed in `paths`, and fire only once
  trusted, until they change.
- **Plugins** cannot trust workflows. Running workflows (and firing
  webhooks) needs `hive.process`, the capability for starting agents and
  processes.
- **Webhooks** reach the daemon only through a plugin that received them.
  Hive opens no port. The payload is data for expressions, never code.
- **Expressions** read only the run's own inputs, trigger and step results.
  There are no environment variables, files or commands in them. A value
  put into an argv stays one argument, but a value put into a shell line
  (`run = "…"`) is shell text. The documentation says to use argv for
  untrusted input, such as a branch name from a trigger.
- **Recorded data.** Runs keep prompts, outputs and summaries on disk under
  the Hive home, with the permissions of the rest of its state.

## Testing

- **The format:** parsing, every rejection above, defaults, `needs` order,
  cycles with their path, and file versions.
- **Expressions:** parsing, precedence, comparisons, truthiness, every
  function, missing names, interpolation, and malformed input (fuzzed).
- **Cron:** fields, ranges, steps, names, the day-of-month / day-of-week
  rule, and the next time across months and years.
- **The engine,** under `-race` with goleak, with fake tasks, commands and
  actions:
  - order, parallel steps, conditions, skips;
  - `on_failure`, `foreach`, `repeat`;
  - retries with backoff, timeouts, cancellation;
  - outputs from `$HIVE_OUTPUT`, and workflow outputs;
  - any-provider choice;
  - resuming after a restart: an agent step waits on its plan, a command
    runs again without spending a retry, a backoff keeps its time.
- **Triggers:** event matching and aliases, a run never triggering its own
  workflow, schedules, git pushes and new branches (against real
  repositories, including a push made while no daemon ran), webhooks,
  concurrency (queue, parallel, skip) and `max_runs`.
- **Trust:** untrusted files never fire, trusting records the digest, and
  a changed file stops firing.
- **The store:** newer schemas left alone, corrupt files moved aside, and
  retention.
- **Config:** `[workflows]` and the retention keys round trip.
- **TUI and desktop:** the Workflows sections.
- **M24's acceptance, end to end** (`TestM24WorkflowOnPush`, with a real
  daemon, shims and a fake Codex):
  - a workflow triggered by a push runs an agent review, then the tests,
    then a fix-up agent because they fail;
  - the daemon restarts while the review agent works, and the run goes on
    without prompting it again;
  - the run is recorded with its trigger, inputs, each step's output and
    the review's summary;
  - the same workflow file runs with `--headless` from a CI-like shell with
    no daemon, prints a JSON report, and exits 1 because the tests fail.

## Alternatives

- **Workflows as plans.** A plan's tasks are all agents, and a plan has no
  trigger, inputs or conditions. Making every plan file a workflow would
  change what an accepted format means. Agent steps reuse M22's tasks
  instead, with placement, isolation, results and resume included.
- **GitHub Actions' YAML.** People know it, but its meaning (runners,
  marketplace actions, contexts) does not carry over. Hive's files are TOML
  like everything else it reads. The expression syntax is close to Actions'
  on purpose.
- **A general-purpose language** (Starlark, JavaScript). That would be more
  power than "the same steps every time" needs, and a second runtime to
  secure. The expression language has no loops, no I/O and no clock.
- **Re-attaching to commands after a restart.** Commands would have to run
  under shims like agents. Most workflow commands are short and safe to
  repeat. A long one can be an agent task, or the shims can learn to run
  plain commands later (open question 1).
- **Trusting a repository rather than a file.** One `trust` for every file
  in a repository would let a new file add triggers unseen. Trust is per
  file and per text.

## Open questions

1. Should commands run under shims, so that a running command survives a
   restart instead of running again?
2. Should a schedule missed while no daemon ran fire once when the daemon
   starts (`catch_up = true`)?
3. Should a workflow be able to call another workflow as a step, with
   inputs, and wait for it?
4. Should steps be placed on saved machines by label, as plan tasks are?
   (Agent steps already can be, through `machine` and `labels`.)

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
