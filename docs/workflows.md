# Workflows: the same steps, every time

A **workflow** is a TOML file of steps: agents' tasks, commands and plugin
actions, with inputs, outputs, conditions and loops. It runs when one of
its **triggers** fires or when you run it:

- a Hive event, such as an agent done or blocked, or a worktree created;
- a schedule;
- a git push or a new branch;
- a webhook a plugin received.

Each **run** is recorded with the workflow's text, its inputs and what
started it. Steps retry with backoff, have timeouts and can be cancelled.
Runs survive a daemon restart, and the same file runs headless in CI.

```sh
hive workflow list                          # workflows, their triggers and latest runs
hive workflow run review -i branch=main --wait
hive workflow runs                          # recent runs
hive workflow report 3f2a                   # what each step did
hive workflow run .hive/workflows/ci.toml --headless --json   # in CI
```

Workflow runs also appear in other clients:

- the TUI's Overview (prefix `O`), with a toast when a run fails;
- the desktop app's sidebar, with Cancel and Run again;
- the MCP tools (`workflow_list`, `workflow_run`, `workflow_wait`, …);
- the Go SDK (`hiveapi.Client.RunWorkflow`, …).

The design is in [RFC 0004](../rfcs/0004-workflow-automation.md).

## A workflow file

Your own workflows are the `*.toml` files in `~/.hive/workflows` (or
`$HIVE_HOME/workflows`). Hive reads them when it starts, when they change,
and on `hive workflow reload`.

```toml
version = 1                      # the file format: required
name = "review-push"             # default: the file's name
description = "Review what was pushed, test it, and fix it if the tests fail."
timeout = "2h"                   # the run's budget: what is left is cancelled
concurrency = "queue"            # queue (default) | parallel | skip
environment = "api"              # where steps run unless they say

[inputs.branch]
description = "the branch to review"
default = "main"                 # or required = true

[[on]]
git = "push"
branches = ["main", "release/*"]
[on.inputs]
branch = "${{ trigger.branch }}"

[[step]]
id = "review"
agent = ["claude", "codex"]      # the first kind installed here
prompt = "Review the changes on ${{ inputs.branch }}. Write your findings to the summary."
isolate = true
timeout = "30m"

[[step]]
id = "test"
run = ["go", "test", "./..."]
timeout = "20m"
retries = 1
backoff = "10s"
on_failure = "continue"          # the run goes on; the step is still failed

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
if = "steps.fix.ok"
run = "go test ./..."            # a shell line

[outputs]
review = "${{ steps.review.summary }}"
```

`hive workflow validate file.toml` checks a file without a daemon. Unknown
keys are errors, not warnings, as in plan files: a mistyped `needs` or `if`
must not run a step it was meant to hold back.

## Steps

A step runs after the steps it `needs`. A step without `needs` runs after
the one written before it, so a list of steps runs in order. `needs = []`
starts a step at once. Steps whose needs have finished run in parallel.

| Kind | Written as | Runs |
|---|---|---|
| agent | `agent = "claude"`, a list, or `"any"` | a task (as `hive task run` runs one): the prompt, in an environment or a fresh worktree (`isolate`, `worktree`, `base`), until its condition (`until`, `match`, `check`); `machine` and `labels` place it |
| command | `run = ["argv", …]`, or `run = "a shell line"` | the command in the step's directory: its `environment`'s (or the run's), then `dir` under it, with `env` added |
| action | `action = "plugin/action"`, with `with = { … }` | a plugin's action, with `with` in its context |

**Where steps run.** The run's directory (`run.dir`) is where commands run,
and where agent steps get an environment, unless a step or the workflow
names an `environment`. It is:

- the directory you ran it from, or `--dir`;
- for a git trigger, the repository;
- for an event about an environment, that environment's directory;
- otherwise, the repository the workflow file is in (`<repo>/.hive/workflows`).

**Outcomes.** Every step ends `success`, `failure`, `skipped` or
`cancelled`.

- `if` is a condition. The default is `success()`: every step this one
  needs succeeded. A false condition skips the step.
- Dependents of a skipped step are skipped too, unless their own `if` says
  otherwise: `always()`, `failure()`, or a test of an outcome.
- `on_failure = "continue"` lets the run go on, and counts the step as
  succeeded for its dependents' `success()`. The step's outcome is still
  `failure`, and an `if` can test it.

**Any provider.** `agent` names kinds, not providers. A list is an order
of preference, and `"any"` is every installed kind, in the order
`hive agent manifests` lists them. The first kind whose command is on this
machine's `PATH` runs the step, and the run records which one it was. A
workflow written with Claude in mind runs with Codex on a machine that has
only Codex.

## Inputs and outputs

**Inputs.**

- Inputs are strings, declared under `[inputs.<name>]` with `description`,
  `default` and `required`.
- A run takes them from `-i name=value` (repeated), from its trigger's
  `[on.inputs]`, or from their defaults.
- An undeclared input, or a missing required one, is refused.

**What every step leaves**, for later steps and for the report:

| Field | Of |
|---|---|
| `steps.<id>.outcome`, `ok`, `attempts`, `reason`, `started_at`, `ended_at` | every step |
| `exit_code`, `output` (its last 60 lines) | commands and actions |
| `kind`, `summary`, `changed` (files), `screen` (its last lines), `plan` | agent steps |
| `results` (each item's or round's result) | loops |

**Outputs.** A command may write `name=value` lines, or `name<<END` … `END`
blocks, to the file `$HIVE_OUTPUT` names, as in GitHub Actions. They
become `steps.<id>.outputs.<name>`, at most 64 KiB in all. A plugin action
gets the same file in its context and in `$HIVE_OUTPUT`.

`[outputs]` are the workflow's own, evaluated when the run ends and printed
in its report.

## Expressions

`${{ … }}` in `prompt`, `run`, `dir`, `environment`, `worktree`, `env`,
`with`, `[on.inputs]` and `[outputs]` is replaced by its value as text. `if`,
`foreach` and a repeat's `until` are bare expressions.

| | |
|---|---|
| Values | `'text'` or `"text"`, numbers, `true`, `false`, `null` |
| Names | `inputs.x`, `trigger.x`, `steps.<id>.x`, `run.id`, `run.workflow`, `run.dir`, `item`, `index`; brackets for keys and indexes: `steps['my-step'].ok`, `trigger.data.commits[0]` |
| Operators | `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `\|\|`, `!`, `( )` |
| Functions | `success()`, `failure()`, `always()`, `cancelled()`, `contains(a, b)`, `startsWith(a, b)`, `endsWith(a, b)`, `join(list, sep)`, `len(x)`, `format('{0}-{1}', a, b)`, `toJSON(x)`, `fromJSON(s)` |

Some rules:

- A missing name is `null`.
- `false`, `null`, `''` and `0` are false.
- Numbers compare as numbers, even when one side is text (inputs are text).
- `a || 'default'` gives a default.

There are no environment variables, no clock and no randomness in
expressions: the same run records give the same values.

> A value put into an argv (`run = ["git", "checkout", "${{ inputs.branch }}"]`)
> stays one argument. A value put into a shell line (`run = "…"`) is shell
> text. Use argv for anything a trigger or a webhook can set.

## Loops

```toml
[[step]]
id = "deploy"
foreach = "fromJSON(inputs.services)"     # or a list: ["api", "web"]
run = ["./deploy", "${{ item }}"]

[[step]]
id = "polish"
agent = "codex"
prompt = "Make the linter pass."
repeat = { max = 3, until = "steps.polish.ok && steps.polish.summary != ''" }
```

- **`foreach`** runs a step once per item, one after another. `${{ item }}`
  and `${{ index }}` name the current one. A text value gives one item per
  line. The step succeeds when every item does.
- **`repeat`** runs a step again, round after round, until `until` holds,
  at most `max` (up to 20) rounds. `until` sees the step's latest round.

## Triggers

Each `[[on]]` table is one trigger.

| Trigger | Fires when | `trigger` holds |
|---|---|---|
| `event = "agent.state"` with `[on.match]` | a Hive event of that type (or prefix: `"branch."`) is published, and every `match` key (a dotted path into its data) has its value (`*` globs) | `event`, `seq` and `data` |
| `event = "agent.done"`, `agent.blocked`, `agent.idle`, `agent.exited` | an agent enters that state | the same, from `agent.state` |
| `schedule = "0 3 * * 1-5"` | a cron line, local time; also `@hourly`, `@daily`, `@weekly`, `@monthly` | `time` |
| `every = "6h"` | that long since it last fired (at least 1 minute) | `time` |
| `git = "push"` (`repo`, `branches`, `remote`) | a remote-tracking branch moves to the commit its local branch has: this repository pushed it | `repo`, `remote`, `branch`, `commit`, `before` |
| `git = "branch"` (`repo`, `branches`) | a local branch appears | `repo`, `branch`, `commit` |
| `webhook = "github"` | `hive workflow fire github --data '{…}'`, or a plugin that received a webhook calls `workflow.fire` | `name`, `data` |

```toml
[[on]]
event = "agent.blocked"
[on.inputs]
agent = "${{ trigger.data.id }}"

[[on]]
event = "branch.updated"
[on.match]
state = "ready"
```

**Git.**

- A git trigger without `repo` watches every repository Hive has an
  environment in.
- References are read every `[workflows] git_interval` (10 s), and soon
  after a repository's git state changes.
- What was seen is saved, so a push made while no daemon ran fires when the
  next daemon looks.
- A fetch that brings in someone else's commits is not a push.

**Schedules** that came due while no daemon ran do not fire late. An
`every` trigger counts from when it last fired, so a restart does not reset
it.

**Concurrency.** A run never triggers its own workflow. `concurrency` says
what a trigger does while a run of the workflow goes on:

- `queue` runs it afterwards, with at most 20 waiting;
- `parallel` runs it now;
- `skip` drops it.

`[workflows] max_runs` (4) bounds the runs running at once across
workflows. The rest wait, oldest first.

## Shared workflows and trust

A repository can carry workflows in `.hive/workflows/`. Hive reads them
only when you list the directory (or a file) in `[workflows] paths`:

```toml
[workflows]
paths = ["~/src/api/.hive/workflows"]
```

A shared workflow's triggers do not fire until you trust it:

```sh
hive workflow list              # FIRES says "no: not trusted"
hive workflow show review       # what it would run
hive workflow trust review      # fires while the file keeps this text
hive workflow trust review --revoke
```

Trust records the SHA-256 of the file's text, so any change to the file
needs trusting again before its triggers fire. A commit to a shared
repository therefore cannot add commands to a trigger.

Running a workflow by hand (`hive workflow run`) is your own command, and
needs no trust. Plugins cannot trust workflows.

## Runs

```
queued → running → succeeded | failed | cancelled
```

- **Outcome.** A run **succeeded** when no step failed (except those that
  `continue`) and none was cancelled. It is **cancelled** when you
  cancelled it or its budget ran out, and **failed** otherwise.
- **Retries.** `retries` (up to 10) runs a failed attempt again after
  `backoff` (default 5 s), doubling each time up to 10 minutes.
- **Timeouts.** A step's `timeout` is an agent task's budget (time blocked
  on you does not count) or a command's or action's wall-clock time. The
  run's `timeout` cancels what is left.
- **Cancel.** `hive workflow cancel <run>` cancels its agents' tasks
  (stopping their agents) and stops its commands with everything they
  started.
- **Run again.** `hive workflow rerun <run>` starts a new run from the
  recorded text, inputs and trigger. Changes to the file since do not
  apply.

**Restarts.** Runs are saved as they change, and the next daemon resumes
every unfinished one:

- Agents work under shims, so an agent step's agent keeps working. The run
  waits on the same task, and its agent is not prompted again.
- Commands and actions that were running are run again from the start.
  This does not spend a retry, so make commands safe to run twice.
- A daemon that stops never cancels a run. Only `cancel` and run budgets
  do.

**History.** Finished runs are kept up to `[retention] workflow_runs`
(200) and `workflow_run_age` (30 days). For each step a run keeps:

- what it actually ran, with expressions replaced;
- its last output lines, outputs and summary;
- its log.

`hive workflow report <run>` prints all of it (`--json` for the data).

## In CI

`--headless` runs a workflow without a daemon around:

- it starts a daemon of its own, in a temporary `HIVE_HOME`;
- the workflow's steps run in the current directory (or `--dir`), with an
  environment made for it;
- it waits, prints the report, stops the daemon and its agents, and
  removes the directory;
- it exits 0 when the run succeeded, 1 when it did not, and 4 on
  `--timeout`.

It is the same engine as the daemon's, so a workflow behaves the same in
CI and on your machine. Triggers do not fire in a headless run.

```yaml
# .github/workflows/agents.yml
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://hive.admir-saheta.com/install.sh | sh
      - run: npm install -g @openai/codex   # an agent the workflow can use
      - run: hive workflow run .hive/workflows/ci.toml --headless --json -i branch="$GITHUB_REF_NAME" > report.json
      - uses: actions/upload-artifact@v4
        if: always()
        with: { name: workflow-report, path: report.json }
```

## API

The wire API is at level 14:

- **Methods:** `workflow.list`, `get`, `validate`, `run`, `runs`, `status`,
  `wait`, `cancel`, `rerun`, `fire`, `trust` and `reload`.
- **Events:** `workflow.run` (a run's summary, on every change),
  `workflow.step`, `workflow.removed` and `workflow.changed`.

Plugins need these capabilities:

| Methods | Capability |
|---|---|
| `workflow.list`, `get`, `validate`, `runs`, `status`, `wait` | `hive.read` |
| `workflow.run`, `rerun`, `cancel`, `fire` | `hive.process` |
| `workflow.reload` | `hive.admin` |
| `workflow.trust` | none: closed to plugins |

An action run by a workflow gets `workflow: {workflow, run, step, with,
output}` in its context.

MCP has `workflow_list`, `workflow_runs`, `workflow_status` and
`workflow_wait` (read-only), plus `workflow_run` (by name only) and
`workflow_cancel`. No tool trusts a workflow or runs workflow text an agent
wrote.

## Configuration

```toml
[workflows]
paths = []               # shared workflow files and directories ("~/" is home)
max_runs = 4             # runs at once, across workflows
git_interval = "10s"     # how often watched repositories and workflow files are read

[retention]
workflow_runs = 200      # finished runs kept
workflow_run_age = "720h"
```
