---
name: hive-throwaway-repro
description: Reproduce a Hive bug in a disposable, isolated Hive runtime (its own daemon, state and socket) without touching the user's sessions or agents, including when the agent itself runs inside a Hive pane. Use for runtime, pane, PTY, terminal, process, socket API, persistence, multiplexer UI or coding-agent reproductions.
---

# Hive Throwaway Reproduction

Reproduce against a disposable Hive runtime: its own daemon, socket, state and configuration, created for the experiment and removed afterwards. Drive it through the CLI. The user's runtime, sessions, panes and agents must come out untouched.

The goal is a reproducible experiment with explicit isolation, observable state transitions, useful evidence and verified cleanup.

## Why care: what a careless command does here

Inside a Hive pane, the environment points at the user's runtime:

| Variable | Set by Hive in every pane | Effect on a bare `hive` command |
|---|---|---|
| `HIVE_HOME` | the user's data directory | targets the user's daemons and state |
| `HIVE_SESSION` | the user's session | targets that session's daemon |
| `HIVE_PANE_ID`, `HIVE_TAB_ID`, `HIVE_ENV_ID`, `HIVE_PROCESS_ID` | the current pane | `hive pane …` without a pane ID acts on **this** pane |
| `HIVE_SOCKET_PATH`, `HIVE_BIN` | the user's daemon and binary | informational; the CLI does not read `HIVE_SOCKET_PATH` |

So, from inside a pane, `hive stop` stops the user's daemon **and every agent it runs**, and `hive pane close` closes the pane you are running in.

There is a second hazard. A Hive build whose API level (`api_level` in `hive status --json`) is newer than the running daemon's **replaces that daemon** on its first command, when no agent can be lost. A checkout build pointed at the user's runtime can therefore restart their daemon. Isolation below prevents both.

## Non-negotiable safety

- Never run reproduction commands against the user's `HIVE_HOME` default session, and never stop, restart or remove the user's daemons or sessions.
- Run every reproduction command through the wrapper created below, which isolates the runtime and clears the inherited pane variables. Never rely on shell state persisting between tool calls.
- Never use `pkill`, `killall`, broad process matching or guessed PIDs. Stop the disposable runtime with its own `hive stop`.
- Read every ID (environment, tab, pane, process) from command output (`--json`). Never construct or guess IDs, and always pass pane IDs explicitly.
- Keep reproduction directories and large artifacts under `/var/tmp`.
- Do not spend paid agent tokens without the user's approval; use the requested low-cost model and the smallest useful prompts.
- If the build cannot isolate the reproduction this way, stop and report the limitation instead of improvising.

## 1. Learn the build under test

The binary is the authority; commands change between versions.

```bash
command -v hive && hive version          # there is no --version flag
hive --help
test -n "${HIVE_PANE_ID:-}" && echo "inside a Hive pane: $HIVE_PANE_ID"
```

Read `hive <group> --help` for every command group you will use (`environment`, `tab`, `pane`, `process`, `terminal`, `session`, `layout`, `daemon`).

Record the binary path, its version, and whether it is an installed release or a checkout build. To test a checkout, build it to a fixed path rather than using `go run`:

```bash
go build -o /var/tmp/hive-repro-bin ./cmd/hive
```

Do not run a bare `hive`: it opens the multiplexer.

## 2. Create the isolated runtime

Create the reproduction directory and a wrapper that runs Hive with its own home and configuration, and without the inherited pane variables:

```bash
REPRO="$(mktemp -d /var/tmp/hive-repro-XXXXXX)"
mkdir -p "$REPRO/home" "$REPRO/work"
HIVE="$(command -v hive)"            # or /var/tmp/hive-repro-bin for a checkout build
cat > "$REPRO/hive" <<EOF
#!/bin/sh
exec env -u HIVE_SESSION -u HIVE_PANE_ID -u HIVE_TAB_ID -u HIVE_ENV_ID -u HIVE_PROCESS_ID \\
  HIVE_HOME="$REPRO/home" HIVE_CONFIG="$REPRO/config.toml" "$HIVE" "\$@"
EOF
chmod +x "$REPRO/hive"
printf '[terminal]\nshell = "/bin/sh"\n' > "$REPRO/config.toml"   # deterministic panes
"$REPRO/hive" config validate
echo "$REPRO"
```

Reuse `"$REPRO/hive"` (with the literal path) in every later call.

A separate `HIVE_HOME` gives the disposable runtime its own daemon, socket, environments, logs, managed workspaces and worktree directory. A named session (`hive --session repro-<topic>`) is lighter, but it shares the user's `HIVE_HOME` and configuration file. Use it only for an installed release when the bug is about sessions themselves, and then pass `--session` on every command (it overrides `HIVE_SESSION`).

Shared resources remain either way: the repositories and directories that environments point at, the user's shell startup files, and credentials of agents you launch. Point environments at copies under `$REPRO/work` when the bug involves file changes.

## 3. Start and verify

The first command starts the disposable daemon (autostart):

```bash
"$REPRO/hive" environment create repro --cwd "$REPRO/work"
"$REPRO/hive" --json status        # socket and data under $REPRO/home; note api_level and pid
"$REPRO/hive" session list
```

Before continuing, confirm:

1. The status reports a socket and data inside `$REPRO/home`.
2. Its PID is not one of the user's daemons (compare with a plain `hive status --json` run **without** the wrapper, which only reads).
3. `"$REPRO/hive" environment list` shows only what you created.

## 4. Drive the reproduction

Use the CLI with `--json` and read IDs from the output:

```bash
"$REPRO/hive" --json tab create repro --name t      # prints the tab and its first pane
"$REPRO/hive" pane run <pane> 'echo ready-1'
"$REPRO/hive" pane wait-output <pane> --regex '^ready-1$' --anywhere --timeout 10s   # exit status 4 on timeout
"$REPRO/hive" --json pane split <pane> -d down -- sh -c 'make test'
"$REPRO/hive" pane read <pane> --source recent-unwrapped -n 200
"$REPRO/hive" pane send-keys <pane> C-c
"$REPRO/hive" pane send-text <pane> 'some text'
"$REPRO/hive" process logs <process> -n 200
"$REPRO/hive" terminal snapshot <process> --scrollback 500
"$REPRO/hive" layout export repro                    # the whole workspace as JSON
"$REPRO/hive" events                                 # live events (stop it with ctrl+C)
```

- Wait on events or output (`pane wait-output`, `events`) instead of sleeping. `wait-output` only matches output that appears after it starts unless `--anywhere` is given, so a fast command can finish before the wait begins: wait for a unique marker with `--anywhere`. When timing is under test, record timestamps, use bounded polling, and capture state before, during and after the transition.
- Daemon restarts are part of many bugs: `"$REPRO/hive" daemon restart` keeps agents running (they run under shims), and `"$REPRO/hive" daemon logs -n 200` shows what the daemon saw.
- Change one variable at a time, starting from a baseline that behaves correctly.

### Multiplexer UI bugs

The daemon runs headless, so most reproductions need no UI. When the bug is in the UI, open it for the disposable runtime in a new pane of the user's session, created without moving focus, and record that pane's ID:

```bash
hive --json pane split "$HIVE_PANE_ID" -d down --no-focus --name repro-ui -- "$REPRO/hive" ui
```

This is the one command that targets the user's session, and it only adds a pane. Drive the UI by sending keys to that pane with the user's `hive pane send-keys <ui-pane> …`, and read it with `hive pane read <ui-pane>`. The UI's prefix is `ctrl+b` unless `$REPRO/config.toml` sets `[keys] prefix_keys`.

### Coding agents

Start agents with `hive agent` and drive them through it; Hive reads their state (working, blocked, done, idle) from their screens:

```bash
P=$("$REPRO/hive" agent start --kind claude --env repro --name agent -- --model <approved-model>)
"$REPRO/hive" agent wait "$P" --until blocked --timeout 30s   # a new folder: the trust dialog
"$REPRO/hive" agent read "$P"                                  # see what it asks
"$REPRO/hive" agent send-keys "$P" Down Enter                  # answer it (Claude: Down selects "trust")
"$REPRO/hive" agent prompt "$P" "Reply with exactly one word: PONG" --wait --read 30 --timeout 2m
"$REPRO/hive" agent explain "$P"                               # why Hive thinks it is in its state
"$REPRO/hive" agent stop "$P"
```

- `--worktree <branch>` on `agent start` gives the agent its own checkout under `$REPRO/home/worktrees`.
- To test agents driving agents over MCP, point an MCP client at the wrapper: `{"mcpServers":{"hive":{"command":"$REPRO/hive","args":["mcp"]}}}`. When the client is Claude Code run from inside another Claude Code, unset `CLAUDECODE` and the `CLAUDE_CODE_*` session variables for it.
- A raw recording of an agent's session for a detection fixture is `"$REPRO/hive" ps logs <process> -n 100000 > session.raw` (see `distribution/agent-detection/README.md`).

- Verify the active model on the agent's own screen (`pane read`) rather than trusting an alias.
- Prefer safe modes and manual permissions for a baseline. Keep prompts narrowly scoped, and do not approve unnecessary file changes, network access or destructive actions.
- For permission-state tests, use harmless operations, capture the pending prompt, reject it, and verify that no artifact was created.

## 5. Collect evidence

Record enough for someone else to reproduce the result:

- The binary path, its version and API level, and whether it is a release or a checkout build.
- `$REPRO`, `config.toml`, the exact commands in order, and the IDs they returned.
- Pane screens and process logs before, during and after each transition.
- Daemon logs (`"$REPRO/hive" daemon logs -n 300`) and relevant events.
- Behaviour across detach, `daemon restart` and process exit, when relevant.
- What was isolated and what was shared.

Avoid collecting secrets, credentials or unrelated terminal history. Separate observed facts from proposed causes.

## 6. Clean up, and verify it

Cleanup is part of the reproduction, including when setup or testing failed.

1. **Stop test activity.** Reject pending agent permission prompts and stop test agents (`"$REPRO/hive" pane send-keys <pane> C-c`, or `"$REPRO/hive" process stop <process>`).
2. **Stop the disposable runtime and its agents:**
   ```bash
   "$REPRO/hive" stop
   "$REPRO/hive" status; echo "exit $?"     # exit 3: not running
   ```
   For a named session, use `hive --session <name> stop`, then `hive session remove <name>` and `hive session list`. Remove only the session you created.
3. **Close the UI pane, if you opened one,** by its recorded ID: `hive pane close <ui-pane>`. Never close the pane you are running in.
4. **Remove the reproduction directory** once the evidence is saved, after checking the path:
   ```bash
   case "$REPRO" in /var/tmp/hive-repro-*) rm -rf "$REPRO" ;; *) echo "refusing: $REPRO" ;; esac
   ```
5. **Confirm the final state.** The user's daemon still answers (`hive status`, read-only, without the wrapper), no reproduction process remains, and no unrelated pane, session or file changed.

If cleanup fails, report exactly what remains and the safe next step. Never escalate to broad process termination.

## Report the result

- **Result:** reproduced, not reproduced, inconclusive, or blocked.
- **Environment:** Hive version, API level, binary source, platform, relevant configuration.
- **Scenario:** exact commands, prompts and starting conditions.
- **Observed and expected behaviour,** with the basis for the expectation.
- **Evidence:** screens, logs, events and API responses.
- **Cause:** a confirmed root cause, or clearly labelled hypotheses.
- **Isolation:** what was isolated and what was shared.
- **Cleanup:** what was stopped and removed, and how it was verified.
- **Remaining work.**

Never claim a reproduction succeeded because a command exited 0, and never claim cleanup completed without verifying it.
