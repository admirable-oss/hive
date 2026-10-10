---
name: hive
description: Drive other coding agents through Hive. Start Claude Code, Codex, OpenCode, Gemini and other agents in their own git worktrees, give them prompts, wait for them to finish, answer their permission prompts and collect their results, using the `hive` CLI or Hive's MCP tools. Use when you run inside Hive ($HIVE_PANE_ID is set) or have the hive MCP server, and work should be split across parallel agents, delegated, reviewed by another agent, or checked on.
---

# Operating agents with Hive

Hive runs coding agents in terminals that outlive the UI and the shell that
started them, and reads each agent's state from its screen: **working**,
**blocked** (waiting for a decision, such as a permission prompt), **done**
(finished a turn, not looked at yet), **idle** (ready for a prompt) or
**exited**. You can start agents, prompt them, wait for them and read them,
either with the `hive` command or, when it is configured, with the hive MCP
server (`claude mcp add hive -- hive mcp`), whose tools have the same names:
`agent_start`, `agent_prompt`, `agent_wait`, `agent_read`, `agent_send_keys`,
`agent_list`, `env_list` and others.

Inside a Hive pane, `$HIVE_PANE_ID`, `$HIVE_ENV_ID` and `$HIVE_BIN` are set,
and every command talks to the session you are in. Use `"$HIVE_BIN"` when
`hive` is not on `PATH`.

## The loop

```bash
# 1. One agent per task, each on its own branch in its own worktree.
P1=$(hive agent start --kind codex --env "$HIVE_ENV_ID" --worktree review/auth)
P2=$(hive agent start --kind codex --env "$HIVE_ENV_ID" --worktree fix/flaky-tests)

# 2. Prompt them. Without --wait, a prompt returns once the agent starts working.
hive agent prompt "$P1" "Review the auth package for security bugs. Write findings to REVIEW.md."
hive agent prompt "$P2" "Make TestUploadRetries pass reliably. Commit when done."

# 3. Wait for each, then collect what it did.
hive agent wait "$P1" --until done --timeout 30m
hive agent read "$P1" --source recent-unwrapped -n 80
```

Or in one step: `hive agent prompt "$P" "…" --wait --read 60` prompts, waits
for the turn to end and prints the agent's last 60 lines.

- `agent start` prints the new agent's **pane ID**. It names the agent in
  every other command (process IDs work too). `--json` gives everything,
  including the new environment and its directory.
- `--worktree <branch>` checks the branch out in a new worktree with its own
  environment. Always use it for agents that work at the same time on one
  repository; otherwise they edit the same files.
- `hive agent manifests` lists the kinds Hive knows (claude, codex, opencode,
  gemini, cursor-agent, copilot, amp, aider).

## Prompts and waits

- A prompt is pasted whole and then submitted, never interleaved with another
  prompt to the same agent. It waits for a just-started agent to be ready.
- **A blocked agent refuses prompts** (error code `conflict`). Read what it
  asks (`hive agent read "$P"`), then answer with keys:
  `hive agent send-keys "$P" 1` or `Down Enter` or `Escape`. Only approve
  what you would approve yourself; when in doubt, ask the user.
- Waits end when the agent reaches what you waited for, **or** blocks, exits,
  stalls or times out. Check the outcome:

| Outcome | Exit status | Meaning |
|---|---|---|
| `reached` / `started` | 0 | as asked |
| `timeout` | 4 | still going; wait again |
| `blocked` | 5 | it needs a decision: read it, answer it, wait again |
| `stalled` | 5 | it did not react to the prompt, or its screen froze while working |
| `exited` | 5 | its process ended |

- `--until idle` waits until it is ready for another prompt; `--until done`
  until it finishes a turn. A finished turn nobody looked at counts: pass
  `--after <completion_seq>` (from `hive --json agent get "$P"`) to wait for
  a newer one.
- Prefer one long wait over polling. With several agents, wait on each in
  turn; the ones that already finished return at once.

## Reading results

- `hive agent read "$P" --source recent-unwrapped -n 100` gives recent output
  as plain lines; `--source visible` (the default) is just the screen.
- Better still, ask agents to leave results in files or commits in their
  worktree (`hive --json agent get "$P"` and `hive env get <env>` give the
  path), and read those.
- `hive agent explain "$P"` shows why Hive thinks it is in its state, with
  the screen as Hive sees it.

## Rules

- Only stop or close agents you started (`hive agent stop "$P"`). Never run
  `hive stop`, `hive daemon stop` or `hive session stop`: they stop every
  agent, including the user's and yours.
- Do not remove worktrees with unmerged work; tell the user where it is
  (`hive worktree list`).
- Name agents after their task (`--name review-auth`) so the user can follow
  them in the sidebar.
- Every command takes `--json`. `hive api schema` describes the whole API
  and `hive api snapshot` returns everything at once. For programs, the Go
  SDK is `github.com/admirable-oss/hive/pkg/hiveapi`.

## Exit status

0 success, 1 error, 2 bad command line, 3 daemon not running, 4 timed out,
5 the agent blocked, stalled or exited.
