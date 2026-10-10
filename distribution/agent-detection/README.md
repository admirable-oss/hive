# Agent manifests

Each `*.toml` file here teaches Hive one coding agent: how to recognise its
process, how to start it, and how to read its state from its screen. They
are built into the `hive` binary. Your own manifests go in
`~/.config/hive/agent-detection/` (beside `config.toml`). A file there adds an
agent, or replaces the built-in manifest with the same `id`. Run
`hive server reload-agent-manifests` after editing one.

```toml
id = "claude"                # lower-case letters, digits and dashes
name = "Claude Code"
priority = 0                 # breaks ties when two manifests recognise a process
synthetic = false            # true: written from documentation, not recorded
activity = false             # true: a changing screen with no rule matching is working

[detect]
process = ["claude"]         # argv[0]'s base name, or argv[1]'s for node, python, bun, …
args_regex = ""              # optional: must also match the arguments joined by spaces

[start]
command = ["claude"]         # what `hive agent start` runs
submit = "Enter"             # the key that sends a pasted prompt
submit_delay_ms = 150        # the pause between pasting a prompt and sending it

[[rules]]
state = "blocked"            # working | blocked | idle | done
priority = 20                # the matching rule with the highest priority wins
region = "screen"            # screen | bottom:N | top:N | lines:A-B | title
description = "a permission dialog"
[rules.match]
line_regex = '^\s*Esc to cancel'
```

A matcher sets exactly one of:

| Key | Matches when |
|---|---|
| `contains` | some line contains the text |
| `line_regex` | some single line matches |
| `regex` | the region's lines, joined by `\n`, match (`^` and `$` match at line breaks) |
| `any = [ … ]` | any of the nested matchers matches |
| `all = [ … ]` | every nested matcher matches (anywhere in the region) |
| `not = { … }` | the nested matcher does not match |

`ignore_case = true` applies to the text tests.

## How the state is decided

1. A report from the agent itself (`hive pane report-agent`, which the hooks
   installed by `hive integration install` call) wins until its TTL passes.
2. Otherwise the highest-priority rule that matches the screen decides.
3. Otherwise, with `activity = true`, a screen that changed in the last 1.5 s
   is working and a quiet one is idle. With `activity = false`, no match
   means idle.

An agent that was working or blocked and is now idle is **done** until
someone looks at it (focuses its pane). `hive agent explain <pane>` shows the
evidence: the rule that matched, any report, and the screen as Hive sees it.

## Fixtures

Every manifest has recorded or written terminal sessions in
`internal/agent/testdata`: `<name>.raw` holds the bytes the agent wrote, and
`<name>.json` holds the terminal size and the checkpoints where the state is
known. `go test ./internal/agent -run TestFixtureClassification` checks every
manifest against them, and `-dump` prints each checkpoint's screen. Fixtures
under `synthetic/` were written from documentation. Replace them with
recordings when you can: run the agent in Hive, take `hive ps logs <process>
-n 100000 > <name>.raw` at the end, and put checkpoints at offsets where a
frame ends (after `ESC[?2026l` for agents that use synchronized updates).
