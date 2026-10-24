# Branches: parallel agents, clean handoffs

Every worktree Hive makes, whether for an agent (`--isolate`, `--worktree`),
a task (`isolate = true`) or on request (`hive worktree create`), gets a
**branch** that Hive follows from creation to cleanup. Hive tells you when
two agents' branches start changing the same files, and which of those
files a merge would conflict on, before anyone merges. It shows each
branch's diff, sends your review comments back to the agent as a prompt,
and runs your gates (tests, lint, build) in the branch's worktree before
calling it ready. You keep every commit, merge and push: Hive pushes or
opens a pull request only when you ask.

```sh
hive agent start --kind claude --env api --isolate   # its own worktree and branch
hive branch list                                     # states, changes, overlaps
hive branch diff hive/claude/3f2a91                  # its changes, uncommitted ones included
hive branch comment p12 "Handle the error" --file api/user.go --line 42 --send
hive branch ready p12                                # run the gates
hive branch pr p12 --push                            # when you say so
```

Branches also appear in the TUI (the sidebar marks each worktree
environment's state, the Overview lists branches, and prefix `D` opens the
review of the current environment's branch), in the desktop app (a
Branches section and a review window), in the MCP tools (`branch_list`,
`branch_diff`, `branch_conflicts`, `branch_comment`, `branch_ready`) and in
the Go SDK (`hiveapi.Client.Branches`, `BranchDiff`, …). The design is in
[RFC 0003](../rfcs/0003-git-native-development.md).

## A branch's life

| State | What it means |
|---|---|
| `created` | the worktree exists; nothing has happened in it yet |
| `working` | an agent has worked there, or it differs from its base |
| `ready` | its gates passed on the files exactly as they are now |
| `merged` | its commits are in its base (by merge, fast-forward or squash) |
| `cleaned` | merged, and its worktree removed |
| `removed` | its worktree was removed before it merged; the branch is kept |

A branch records its **base**: the branch it merges into (the one checked
out in the repository when the worktree was made, or `--base`), and the
commit it started from. Any change to the files of a `ready` branch makes it
`working` again, because its gates passed on different files.

A branch has **merged** when its head is in its base, or in the base's
upstream (a pull request merged and fetched), or when merging it would
change nothing (a squash merge). A branch that never had commits of its own
has merged nothing, even if its head moved forward with its base.

Name a branch by its ID (its environment's ID), the ID of an agent working
in it, its task (`plan/task`), or its name.

### Naming

```toml
[branches]
agent_pattern = "hive/{kind}/{id}"     # agents started with --isolate
task_pattern = "hive/{plan}/{task}"    # isolated tasks
```

Placeholders are `{kind}`, `{name}`, `{env}`, `{plan}`, `{task}`, `{date}`
(`20261023`) and `{id}` (6 random hex digits). An isolated task's branch is
named when its plan is submitted and kept with it, so a resumed or retried
task finds its worktree again.

## Overlaps and conflicts

Hive compares the open branches of each repository whenever something
changes in one of them (its files, its git state, its agent's work), and at
least every `check_interval`. Uncommitted work counts: each worktree is read
as a **snapshot**, a commit of its files as they are that Hive makes
without touching the worktree, its index or any branch.

- Two branches **overlap** when they change the same files.
- They **conflict** when a merge of the two would stop on some of those
  files. Hive merges the snapshots in memory (`git merge-tree`, git 2.38 or
  newer) to find out.
- A branch also overlaps its **base** when the base has moved on and
  changed files the branch changes.

New overlaps raise a notification in the TUI and the desktop app, and are
published as `branch.overlap` events. To see the conflict itself:

```sh
hive branch conflicts p12                 # against its base
hive branch conflicts p12 --with p14      # against another branch
```

This prints each conflicted file with its conflict markers. Nothing is
merged.

### Bringing the base in

```sh
hive branch update p12             # merge the base into the branch
hive branch update p12 --rebase    # or rebase onto it
hive branch update p12 --abort     # undo one that stopped on a conflict
```

This is your merge, run because you asked. It refuses while an agent is
working (or waiting for you) in the worktree, or while files are
uncommitted. On a conflict it stops and leaves the files for you. **Hive
never resolves a conflict.**

## Review

`hive branch diff` shows what a branch changes since it forked from its
base: its commits, its uncommitted changes and its new files, as one
unified diff. Changes the base made since then are left out. `--stat` lists
only the files.

Comments are attached to a branch, optionally to a file and line:

```sh
hive branch comment p12 "Handle the empty case" --file api/user.go --line 42
hive branch comment p12 "Add a test for it"
hive branch send p12          # or --send on the last comment
```

Sending delivers every unsent comment to the branch's agent as one prompt
(the agent it was made for, if it still runs there, otherwise another agent
in its worktree). With no agent there, comments are kept until one is.

### Gates

```toml
[[branches.gate]]
name = "test"
command = ["go", "test", "./..."]
timeout = "20m"           # default 30m
repo = "~/src/api"        # only this repository's branches (default: all)

[[branches.gate]]
name = "lint"
command = ["golangci-lint", "run"]
```

`hive branch ready <branch>` runs the gates in the branch's worktree, in
order, and stops at the first failure. Each gate gets `HIVE_BRANCH` and
`HIVE_BASE` in its environment. The branch becomes `ready` only when every
gate passes and its files did not change while they ran. A command after
`--` runs instead of the configured gates:

```sh
hive branch ready p12 -- make check
```

## Publishing, by consent

```sh
hive branch push p12                 # never forced
hive branch pr p12                   # refuses if the remote lacks the branch
hive branch pr p12 --push --draft    # push first, then open it
```

`pr` refuses a branch with uncommitted changes (a pull request carries
commits, and Hive does not make them) or with nothing to propose. Its title
defaults to the first commit's subject. Its description defaults to the
commits and the gates that passed.

Forges are chosen by the remote's host. `github.com` and `github.*` hosts
use GitHub's `gh`; `gitlab.com` and `gitlab.*` hosts use GitLab's `glab`.
Both run with your own login. Other hosts:

```toml
[[branches.forge]]
host = "git.example.com"
type = "gitlab"           # github | gitlab | command

[[branches.forge]]
host = "code.corp"
type = "command"
command = ["corp-pr"]     # gets the request as JSON on stdin, prints {"url": …}
```

Plugins cannot push or open pull requests (`branch.push` and `branch.pr` are
closed to them), and the MCP tools do not offer either. Those actions only
happen on a person's command.

## Cleanup

```toml
[branches]
cleanup = "merged"        # or "never"
delete_merged = false     # also delete the local branch, when git agrees it merged
keep = 100                # cleaned and removed branches remembered
```

With `cleanup = "merged"`, a merged branch's worktree (and its
environments) is removed once nothing in it is uncommitted and no process
runs there. Until then, the branch says why it is kept
(`hive branch get`).

`hive branch clean <branch>` removes a worktree on your command, and
`--merged` cleans every merged branch. **Work is never lost:**

- the branch and its commits stay in the repository; unmerged branches are
  never deleted;
- a worktree with uncommitted changes is kept unless you pass `--force`,
  and even then those changes are first saved as a commit under
  `refs/hive/saved/<branch>-<time>`
  (`git checkout refs/hive/saved/… -- .` brings them back).
