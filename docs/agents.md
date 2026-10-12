# Agent guides

Hive runs any program, but it understands coding agents: it recognises them
by their process, reads their state from their screen (working, blocked,
done, idle), prompts them safely, and resumes their conversations after a
reboot. Each agent is described by a manifest
([distribution/agent-detection](../distribution/agent-detection)); your own
in `~/.config/hive/agent-detection/` add agents or replace these.

| Agent | Kind | State from | Hooks (`hive integration install`) | Resumes with | Fixtures |
|---|---|---|---|---|---|
| Claude Code | `claude` | screen + hooks | ✅ | `claude --resume <id>` · `claude --continue` | recorded |
| Codex | `codex` | screen + hooks | ✅ | `codex resume <id>` · `codex resume --last` | recorded |
| OpenCode | `opencode` | screen | — | `opencode --session <id>` · `opencode --continue` | from docs |
| Gemini CLI | `gemini` | screen | — | `gemini --resume <id>` · `gemini --resume latest` | from docs |
| Cursor Agent | `cursor-agent` | screen | — | `cursor-agent --resume <id>` · `cursor-agent resume` | from docs |
| Copilot CLI | `copilot` | screen | — | `copilot --resume <id>` · `copilot --continue` | from docs |
| Amp | `amp` | screen | — | `amp threads continue <id>` · `amp threads continue` | from docs |
| Aider | `aider` | screen activity | — | `aider --restore-chat-history` | from docs |

"From docs" manifests were written from the agent's documentation and
screenshots, not recorded from it. They work for the common screens; a
recording (see the manifests' README) makes them exact, and is a welcome
contribution.

## Everything Hive does with an agent

- **Start:** `hive agent start --kind <kind> --env <env> [--worktree <branch>] [-- args]`
  runs the manifest's command in a new tab, in its own worktree if you like.
  Agents started in a shell are recognised too.
- **Watch:** the sidebar and the Overview (`Ctrl+B O`) show every agent's
  state. `hive agent explain <agent>` shows why Hive thinks so: the rule that
  matched, the report, the screen.
- **Notify:** when an agent blocks or finishes, Hive raises a toast, a
  terminal notification (works over SSH) or a system one, never for the pane
  you are in (`[notify]`).
- **Prompt:** `hive agent prompt` pastes a prompt whole (bracketed paste when
  the agent asks for it), never while the agent is blocked, and only once its
  screen has settled. It then waits (`--wait`, `--until done`) and reports
  stalls.
- **Resume:** after a reboot, `session.resume_agents_on_restore` brings each
  agent back in its conversation. With hooks installed this is exact (by
  session ID); without them, it is the latest conversation in the directory.

## Claude Code

```sh
hive integration install claude     # hooks in ~/.claude/settings.json (or $CLAUDE_CONFIG_DIR)
claude mcp add hive -- hive mcp     # lets Claude drive other agents through Hive
```

The hooks report every state change, including permission prompts (blocked),
and the session ID, so resuming is exact. Installing is idempotent and backs
up your settings file; `hive integration uninstall claude` puts it back.

## Codex

```sh
hive integration install codex      # hooks in ~/.codex/hooks.json (or $CODEX_HOME)
```

Codex's approval prompts and its folder-trust prompt show as blocked. Without
hooks, resuming uses `codex resume --last`, the latest session in the
directory: give each Codex its own worktree when several run in one
repository.

## The others

OpenCode, Gemini CLI, Cursor Agent, Copilot CLI and Amp are read from their
screens. Their states are right for the usual screens: working while they
show their busy indicator, blocked on their approval prompts, idle at the
prompt. If one is misread, `hive agent explain` shows what Hive saw, and a
user manifest can adjust the rules without waiting for a release.

Aider prints a plain transcript with no busy indicator, so its state comes
from activity: a changing screen is working, a quiet one idle. Its yes/no
questions are blocked.

## Your own agent

Write a manifest in `~/.config/hive/agent-detection/<id>.toml`, then
`hive server reload-agent-manifests` (or restart the daemon):

```toml
id = "my-agent"
name = "My Agent"

[detect]
process = ["my-agent"]

[start]
command = ["my-agent"]

[resume]
last = ["--continue"]

[[rules]]
state = "working"
region = "bottom:3"
[rules.match]
contains = "thinking"
```

`hive agent manifests` lists what is loaded. An agent can also report its own
state, with no manifest rules at all: `hive pane report-agent --state working`.
