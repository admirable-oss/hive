# RFC 0003: Branches, from worktree to merge

- **Status:** Proposed
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`branch.*`, `agent.start` `isolate`), CLI (`hive branch`, `hive agent start --isolate`), Go SDK, MCP tools, config (`[branches]`), plugin capabilities, on-disk state (`branches.json`), TUI and desktop app

## Summary

M23 has Hive follow the branch of every worktree it makes, whether for an
agent, a task or on request, from creation until the branch merges and its
worktree is cleaned up. The daemon:

- tracks each branch's state, base and changes, uncommitted work included;
- reports branches that change the same files, and the files a merge would
  conflict on, before anyone merges;
- shows each branch's diff, sends review comments back to its agent as a
  prompt, and runs gates (tests, lint, build) in its worktree before the
  branch counts as ready;
- pushes and opens pull requests only on a person's explicit command.

Hive never commits, merges, resolves a conflict or pushes by itself.
Everything is additive.

## Motivation

Worktrees (M2, M5) and isolated tasks (M22) let agents work in parallel
without sharing a checkout. What happens around those worktrees is still
left to the person:

- which agent's branch touches what;
- whether two agents are about to collide;
- what each one changed;
- whether its tests pass;
- what to do with the worktree once the work has landed.

Collisions are found late, at merge time. Worktrees pile up. Reviewing an
agent's work means leaving Hive. Hive already knows every worktree, its
agent and its repository, so it is the right place to follow these
branches.

## Proposal

### Branches and their states

A `Branch` is created when Hive makes or opens a worktree (`worktree.create`,
`worktree.open`, `agent.start` with `worktree` or `isolate`, isolated tasks).
Worktree environments from older releases are adopted when the daemon starts.
A branch records:

- its repository and name;
- its **base** (the branch checked out in the repository when the worktree
  was made, or the requested base) and the commit it started from;
- its worktree and environment;
- what it was made for: an agent, and a plan/task.

```
created → working → ready ⇄ working → merged → cleaned
   \________\___________\______________________→ removed
```

- **working**: an agent worked there (its first `working` state), or the
  branch differs from its base.
- **ready**: the branch's gates passed on its current tree. Any change
  makes it working again.
- **merged**: its head is in its base or the base's upstream, or merging it
  would change nothing (squash). A branch never seen with commits of its own
  has merged nothing: its head may only have followed its base forward. This
  is checked against what was observed and against the branch's reflog
  (`commit:` entries).
- **cleaned** or **removed**: the worktree is gone, after or before the
  merge. The record is kept for `branch.list --all`, up to `keep`.

Branches are named by their ID (the environment's), their environment, their
owner agent or plan/task, the environment of any agent working in them, or
their name. Ambiguous names are refused.

### Uncommitted work: snapshots

Agents rarely commit. To compare their work, the daemon makes a
**snapshot** of each worktree:

1. copy the worktree's index to a temporary file (keeping git's stat
   cache);
2. `git add --all` into that copy;
3. `write-tree`, then `commit-tree` with HEAD as the parent.

The worktree, its real index and all references stay untouched, so this is
safe while an agent works. A clean worktree's snapshot is HEAD. Snapshot
commits are unreferenced, and git collects them as garbage like any other
loose object.

### Overlaps and conflicts

For each repository, the daemon compares the open branches' changed files
(from the merge base to each snapshot) pairwise:

- **Overlap:** two branches change the same files.
- **Conflict:** the overlapping branches' snapshots, merged in memory with
  `git merge-tree --write-tree` (git 2.38+), stop on some of those files.
  Results are cached by the pair of trees. With an older git, overlaps are
  still reported, with `unknown: true` in place of conflicts.

Each branch is also compared with its base when the base has moved on.

A repository is read again soon (500 ms debounce) after one of its
environments' git state changes (`environment.git`) or an agent's state
changes there (`agent.state`). It is also read every `check_interval`
(default 30 s) in case nothing was noticed. New overlaps are published once
per pair as `branch.overlap`.

`branch.conflicts` shows each conflicted file with its markers, from the
in-memory merge. `branch.update` merges (or rebases onto) the base in the
worktree, on request only:

- it refuses while an agent works or waits in that worktree, or while files
  are uncommitted;
- on a conflict, git stops and the files are left for the person;
- `abort` undoes it.

### Review

- `branch.diff`: the branch's changes from its fork point to its snapshot,
  with files and line counts, and a unified patch bounded at 512 KiB by
  default (4 MiB at most).
- `branch.comment`: a comment, optionally on a file and line; up to 200 per
  branch, and 16 KiB each. `branch.send` (or `send: true` on a comment)
  sends every unsent comment as one prompt to the agent the branch was made
  for if it runs there, otherwise to another live agent in its worktree.
  With no agent, comments are kept.
- `branch.ready`: runs the gates (`[[branches.gate]]`, or the ones given in
  the request) in order in the worktree, failing fast. Each gate gets
  `HIVE_BRANCH` and `HIVE_BASE`. It runs like a task's check: output bounded,
  everything it started stopped on its timeout. The branch is ready when all
  gates pass and its tree did not change while they ran.

### Publishing, by consent

- `branch.push` pushes the branch to the same name on a remote (its
  upstream's, else `origin`, else the only one), sets the upstream, and
  never forces.
- `branch.pr` opens a pull request. It refuses:
  - a branch with uncommitted changes;
  - a branch without commits of its own;
  - a branch whose remote lacks its head, unless `push` asks to push it
    first.

Forges are chosen by the remote's host:

- **GitHub** (`gh pr create`) for `github.com` and `github.*` hosts;
- **GitLab** (`glab mr create`) for `gitlab.com` and `gitlab.*` hosts;
- any host routed with `[[branches.forge]]`, either to one of those two or
  to a command. A command gets the request as JSON on stdin and prints the
  pull request's URL or `{"url", "number"}`.

The person's own `gh` or `glab` login is used, and Hive stores no tokens.
`branch.Forges.Register` is the hook M26 plugins will use to add forges.

### Cleanup

With `cleanup = "merged"` (the default), a merged branch's worktree and its
environments are removed once no uncommitted work and no running process is
in them. Otherwise the branch says why it was kept. With `delete_merged`, the
local branch is deleted too, with `git branch -d`, so git agrees it has
merged. A squash-merged branch is kept.

`branch.clean` removes a worktree on request (or every merged one's).
Uncommitted changes need `force`, and even then they are first saved as a
commit under `refs/hive/saved/<branch>-<unix time>`. Unmerged branches are
never deleted. **Unmerged work is never lost.**

### Naming

`[branches] agent_pattern` (default `hive/{kind}/{id}`) names the branch of
`agent.start` with `isolate: true`. `task_pattern` (default
`hive/{plan}/{task}`, as before) names isolated tasks' branches. The task's
branch is resolved when its plan is submitted and stored in the plan, so a
change to the pattern never strands a running task.

Placeholders: `{kind}`, `{name}`, `{env}`, `{plan}`, `{task}`, `{date}`,
`{id}`. A pattern that cannot make a valid branch is refused when the
configuration is read.

### Wire API (API level 13)

| Method | Params | Result |
|---|---|---|
| `branch.list` | `{repo?, all?}` | `[]Branch`, active first, newest first |
| `branch.get` | `{ref}` | `Branch` |
| `branch.check` | `{ref?, repo?}` | `[]Branch`, read again now |
| `branch.diff` | `{ref, paths?, context?, limit?, stat?}` | `Diff` |
| `branch.conflicts` | `{ref, with?}` | `Conflicts` |
| `branch.update` | `{ref, rebase?, abort?}` | `{branch, conflicts}` |
| `branch.ready` | `{ref, gates?}` | `Branch` (a failing gate is a result, not an error) |
| `branch.comment` | `{ref, path?, line?, body, by?, send?}` | `Branch` |
| `branch.send` | `{ref}` | `Branch` |
| `branch.push` | `{ref, remote?}` | `Branch` |
| `branch.pr` | `{ref, title?, body?, draft?, base?, push?, remote?}` | `Branch` |
| `branch.clean` | `{ref?, merged?, force?, delete_branch?}` | `[]Branch` |

`agent.start` gains `isolate`.

Errors:

- `not_found`;
- `invalid_params` (a bad request, an ambiguous name, no forge for the
  host);
- `conflict` (the state forbids it: dirty, nothing to propose, not pushed,
  an agent working, git refusing);
- `unavailable` (git too old).

Events:

- `branch.updated` (a `Branch`, with every change, in `rev` order);
- `branch.overlap` (`{repo, ids, names, files, conflicts}`);
- `branch.removed` (`{ids}`).

Plugins:

- `hive.read`: `branch.list`, `get`, `check`, `diff` and `conflicts`;
- `hive.input`: `branch.comment` and `send`, since they prompt agents;
- `hive.process`: `branch.ready`, `update` and `clean`;
- `branch.push` and `branch.pr` are **closed** to plugins: publishing
  happens only on a person's command.

### CLI, SDK and MCP

- `hive branch list | get | check | diff | conflicts | update | ready |
  comment | send | push | pr | clean`, and
  `hive agent start --isolate`.
- `hive branch ready`, `hive branch conflicts` and `hive branch update` exit
  1 when a gate fails, there are conflicts, or conflicts are left.
- Go SDK: `Branches`, `Branch`, `CheckBranches`, `BranchDiff`,
  `BranchConflicts`, `UpdateBranch`, `ReadyBranch`, `CommentBranch`,
  `SendComments`, `PushBranch`, `OpenPullRequest` and `CleanBranches`, with
  type aliases.
- MCP tools:
  - `branch_list`, `branch_get`, `branch_diff`, `branch_conflicts`
    (read-only);
  - `branch_comment` (a reviewing agent may send comments to another);
  - `branch_ready`.

  No MCP tool pushes, merges, opens a pull request or removes a worktree.

### Clients

- **TUI:**
  - each worktree environment in the sidebar shows its branch's state
    (conflicts and overlaps first) and its unsent comments;
  - the Overview has a Branches section;
  - new overlaps raise a toast and a notification;
  - prefix `D` opens the review of the current environment's branch. It is
    a scrollable diff: `c` comments on the line under the cursor, `C` on
    the whole change, `s` sends, `r` runs the gates.
- **Desktop:**
  - a Branches section in the sidebar;
  - a review window with overlaps, gate results, the diff, comments on any
    line, and Run gates, Send, Push, Open pull request (confirmed) and
    Clean up (confirmed);
  - notifications for new overlaps;
  - "its own worktree" when starting an agent.

### Configuration

```toml
[branches]
agent_pattern = "hive/{kind}/{id}"
task_pattern = "hive/{plan}/{task}"
cleanup = "merged"        # merged | never
delete_merged = false
check_interval = "30s"
keep = 100

[[branches.gate]]
name = "test"
command = ["go", "test", "./..."]
timeout = "20m"
repo = "~/src/api"

[[branches.forge]]
host = "git.example.com"
type = "gitlab"           # github | gitlab | command
command = []              # for type = "command"
```

## Compatibility

- Only additions. No method, field, flag or key changes meaning.
- The API level is now 13.
  - **An older client with a newer daemon** sees nothing new.
  - **A newer client with an older daemon** gets `unknown_method` for
    `branch.*`. The TUI and the desktop app treat that as "no branches".
- **On-disk state:** `branches.json` is new, at schema 1.
  - A newer schema is refused: nothing is loaded, and the file is never
    written over.
  - An unreadable file is moved aside to `*.corrupt-<time>`.
- **Isolated tasks:** the default task pattern names the same branches as
  before (`hive/<plan>/<task>`). Plans saved by v1.3.0 have no resolved
  branch and keep naming it as before.
- **git:** conflicts need git 2.38 (October 2022). Everything else works
  with older git, and conflicts show as unknown.

## Security and privacy

- Gates run as the daemon's user in the branch's worktree, like task checks
  and `pane.run`. They come from the user's configuration or the request.
  A repository cannot define gates, so cloning a repository never adds
  commands to run.
- Publishing is explicit and attributable to a person:
  - plugins cannot call `branch.push` or `branch.pr`;
  - MCP offers neither;
  - pushes are never forced;
  - forges use the person's own CLI login, and Hive stores no credential.
- Snapshots and saved refs live in the repository's own object store and
  `refs/hive/`, never anywhere else. Saved refs are written only on a
  forced cleanup.
- Comments become prompts to agents. They need `hive.input` from plugins,
  as `agent.prompt` does.

## Testing

- `internal/git`, against real repositories:
  - snapshots leave the index and worktree untouched, ignore what git
    ignores, and work before the first commit;
  - files with renames and binaries, patches and their limits;
  - in-memory merges and conflict content;
  - update and abort, for merge and rebase;
  - push, delete branch, saved refs, reflog commits and subjects.
- `internal/branch`, under `-race` with goleak:
  - the lifecycle from created to cleaned, and squash merges;
  - a base moving forward is not a merge;
  - dirty and busy merged worktrees are kept, and forced cleanups save the
    work;
  - unmerged removal keeps the branch, and a worktree removed elsewhere is
    noticed;
  - pairwise overlaps and conflicts, and the base moving under a branch;
  - one event per new overlap;
  - updates refused while an agent works, and stopped on conflicts;
  - diffs with uncommitted work;
  - comments sent to the owner, or kept with no agent;
  - gates: failing, skipped, files changed meanwhile, and their
    environment;
  - pull requests only after a push, once, with the forge's request;
  - references and ambiguity, `keep`, the run loop, and the store's
    newer and corrupt files;
  - forges: host routing, remote URL parsing, gh/glab arguments and output,
    command forges;
  - patterns.
- Config: `[branches]`, gates and forges round trip; bad entries warn.
- TUI: sidebar states, the review overlay with a comment placed on its
  line, and the Overview.
- Desktop: the core view model and overlap notifications; the frontend's
  Branches section and review (conflicts, gates, a line comment sent, gates
  run).
- **M23's acceptance, end to end** (`TestM23ParallelBranches`, with a real
  daemon, shims, a fake Codex and a fake `gh`):
  - three agents start with `--isolate` and write the same file
    differently;
  - every branch reports overlaps with the other two and the conflict,
    before any merge;
  - each diff shows its agent's work, and a comment reaches its agent;
  - a failing gate leaves a branch working, and the configured gate makes
    another ready;
  - nothing is pushed and `gh` never runs until `hive branch pr --push`;
  - merged into main, the first branch's worktree is cleaned up while the
    other two keep theirs;
  - forcing the removal of one of them saves its uncommitted work under
    `refs/hive/saved/` and keeps its branch.

## Alternatives

- **Reading only committed work.** Agents rarely commit before they are
  done. Overlaps would then be found only after the work, which is the
  problem this milestone is about.
- **Stashing to compare.** `git stash create` touches no files, but it
  leaves out new files. A snapshot through a copy of the index includes
  them and touches nothing.
- **Forge APIs with tokens.** These would need Hive to store credentials.
  `gh` and `glab` already hold the person's login, handle enterprise hosts,
  and are what people use by hand.
- **Resolving conflicts with the agent.** That could be a review comment the
  person sends. Hive never decides a conflict on its own.
- **Gates from a file in the repository.** That would let any repository
  run commands. It is left for a design with explicit trust (open
  question 2).

## Open questions

1. Should a merged branch whose agent is idle be cleaned up (stopping that
   agent), rather than waiting until no process runs there?
2. Should a repository be able to propose gates (`.hive/gates.toml`) that
   run once the person trusts them, as plugins' capabilities are granted?
3. Should overlaps across machines (a task's worktree on a saved machine)
   be compared too, by fetching the other daemon's snapshots?

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
