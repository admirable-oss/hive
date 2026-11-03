# RFC 0009: Projects as code

- **Status:** Draft
- **Authors:** @admirsaheta
- **Discussion:** <!-- the pull request -->
- **Affects:** wire API (`project.*`), CLI (`hive up`, `hive down`, `hive project`), Go SDK, on-disk state (`projects/trust.json`), branch gates and workflow sources, the threat model

## Summary

M30 puts a project's Hive setup in its repository:

- **`hive.toml`**, a versioned file at the repository root. It declares the
  project's environments, their tabs and panes, the agents they start, the
  variables they need, and later its gates, workflows, plugins and
  toolchain.
- **`hive up`** makes the running workspace match the file. It creates what
  is missing, leaves what runs alone, and does nothing the second time.
  `hive up --plan` says what it would do first. **`hive down`** stops the
  project's agents and closes its tabs, and keeps its environments and
  worktrees.
- **Trust.** Nothing in a project file runs until the person trusts its
  current text. A changed file is untrusted again until someone looks at
  it. This is how shared workflows already work (M24).
- **`hive.local.toml`**, ignored by git, holds a person's own values and
  preferences, layered over the project's.

## Motivation

Today a contributor who clones a repository that is worked on with Hive
rebuilds its setup by hand: `hive env create --cwd .`, the tabs and their
commands, `hive agent start` for each agent, the variables. Teams write
shell scripts for this, and the scripts break: `layout.apply` adds its tabs
again every time it runs, and `agent.start` always opens a new agent.
Nothing says what a repository expects, and nothing checks it.

The same setup also belongs next to the code it serves. When a project adds
a service, its `hive.toml` changes in the same commit, and every
contributor's next `hive up` picks it up.

## Proposal

### The file

```toml
version = 1
name = "shop"            # the main environment's ID; default: the directory's name

# Plain settings for every pane. Secrets do not belong here: values that
# look like tokens or keys are refused.
[vars]
NODE_ENV = "development"

# Variables the project needs, by name. Their values come from
# hive.local.toml or from the shell that runs `hive up`.
[env]
required = ["DATABASE_URL"]
optional = ["STRIPE_KEY"]

[[tab]]
name = "dev"
split = "horizontal"     # side by side (the default), or "vertical"

  [[tab.pane]]
  name = "server"
  command = ["npm", "run", "dev"]
  cwd = "web"

  [[tab.pane]]
  name = "shell"

[[agent]]
name = "reviewer"
kind = ["claude", "codex"]   # in order of preference; or one kind
isolate = true               # in a worktree of its own
args = []

# More environments, each in its own directory of the repository.
[[environment]]
id = "shop-api"
path = "api"
  [[environment.tab]]
  name = "api"
    [[environment.tab.pane]]
    command = ["go", "run", "./cmd/api"]
```

- The top-level `vars`, `env`, `tab` and `agent` describe the **main
  environment**, whose ID is `name` and whose root is the file's directory.
  Each `[[environment]]` adds another, with an ID and a path inside the
  repository. Paths may not leave it.
- The file is checked strictly. Unknown keys are errors, and every error
  points at its line. `version = 1` is required, and a file written for a
  newer format is refused with a hint to update Hive.
- Within an environment, tab and agent names are unique. Agents get a tab
  of their own named after them, as `hive agent start --name` does, so no
  tab and agent may share a name.
- `kind` takes one agent kind or a list in order of preference. `hive up`
  starts the first one that is installed.
- `isolate = true` starts the agent in a worktree of its own, on a branch
  named by `[branches] agent_pattern`. `worktree = "<branch>"` names the
  branch instead.

### `hive.local.toml`

```toml
version = 1

[vars]                       # values for [env] names, and the person's own settings
DATABASE_URL = "postgres://localhost/shop"

[agent.reviewer]
kind = "codex"               # the person's preferred kind for this agent
args = ["--model", "o4"]
start = false                # never start this agent here
```

Values are resolved in this order: `hive.local.toml`, then the shell that
runs `hive up` (only for the names the project declares), then the
project's `[vars]`. A missing `required` name stops `hive up` before it
changes anything.

`hive.local.toml` is the person's own and needs no trust while git does not
track it. If git tracks it, it is part of what is trusted.

### Up, down and plans

`hive up [dir]` finds `hive.toml` in `dir` (default: the current
directory) or the nearest directory above it, and asks the daemon for a
plan. The plan compares the file with what runs:

| The file declares | It is there when | Otherwise |
|---|---|---|
| an environment | an environment with that ID exists | create it at its path |
| a variable | the environment has that value | set it |
| a tab | the environment has a tab with that name | create it with its panes |
| an agent | an agent with that name runs in the environment, or in a worktree of it | start it, or start it again in its pane if it exited |

Some differences cannot be resolved by creating something, and stop the
plan instead: an environment with the project's ID at another path, an
agent kind no manifest knows, a missing required variable. Nothing is
changed when a plan has problems.

`hive up --plan` prints the plan and changes nothing. `hive up` applies
it, and reports what it did. Applying stops at the first failure and
reports what was done before it.

`hive down [dir]` stops the agents the file declares and closes the tabs
it declares. Environments and worktrees stay, with any uncommitted work
in them. `hive down --plan` says what it would stop.

An agent that `hive up` starts in a new worktree marks the worktree's
environment with `HIVE_PROJECT_AGENT=<name>`. Branch names from
`agent_pattern` usually carry a random part, so the mark is how the next
`hive up` finds the worktree `hive down` kept and starts the agent there
again, rather than in another new worktree.

Matching by name means a person can rename or close what `hive up` made,
and the next `hive up` makes it again. Changing a tab that exists is not
`hive up`'s job: `hive project diff` and `hive project save` (delivery
step 3) handle the layout.

### Trust

A project file is trusted at its current text: its SHA-256, and that of a
tracked `hive.local.toml`, recorded by `hive project trust` in
`projects/trust.json`. `hive up` on an untrusted file shows what the file
declares (environments, every command, every agent) and asks once. `--yes`
accepts in scripts. Off a terminal without `--yes`, it refuses and says to
run `hive project trust`. When the file changes, its trust no longer
matches, and the next `hive up` asks again.

`hive project check [dir]` validates a file without a daemon, for CI and
editors, and never needs trust.

### API

API level 18 adds:

| Method | Params | Result |
|---|---|---|
| `project.get` | `{dir}` | `Project`: root, file, digest, trusted, error, the resolved spec |
| `project.list` | `{}` | trusted projects |
| `project.trust` | `{dir, revoke}` | `Project` |
| `project.plan` | `{dir, down, vars}` | `Plan`: actions and problems |
| `project.up` | `{dir, vars}` | `Applied`: the plan and what was done |
| `project.down` | `{dir}` | `Applied` |

`vars` carries the values of the declared `[env]` names from the client's
own environment, and nothing else. `dir` is absolute, on the daemon's
machine, so `--remote` and `--machine` work as for every other command.

Members (M27): `project.trust` is closed. `project.up` and `project.down`
need the owner role across all environments, like `layout.apply`.
`project.get`, `project.list` and `project.plan` need the owner role too,
because they read files at a path the caller chooses. Plugins:
`project.trust` is closed, `project.up` and `project.down` need
`hive:process`, and the reads need `hive:read`.

### Later steps

These are part of M30 and are designed with this file in mind. Each comes
with an amendment to this RFC:

- `[[gate]]`: branch gates for the project's repository, added to
  `[[branches.gate]]` while the file is trusted.
- `workflows = [".hive/workflows"]`: workflow directories, trusted with the
  file at their current text.
- `plugins = ["pr-status"]`: plugins the project needs, installed from the
  catalog by `hive up` after a confirmation.
- `toolchain = "auto" | "nix" | "devcontainer" | "mise" | "direnv" | "none"`:
  panes start inside the project's toolchain, with the environment cached.
- `secrets`: values by reference to the person's own store (Keychain,
  Secret Service, `pass`, the 1Password CLI).
- `hive project diff`, `hive project save`, `hive project init --from`, and
  a JSON Schema for editors.

## Compatibility

Additions only. A daemon below API level 18 does not know `project.*`; the
CLI says so and asks for an update, as for every new method. The trust file
has a schema version, and a file written by a newer Hive is left untouched
and refused, like the workflow trust file.

The project file has its own `version`. Format 1 can gain optional keys in
1.x. A key that changes meaning needs format 2, which this Hive refuses
with a hint to update.

## Security and privacy

A repository is code from someone else. A project file can start any
command in a pane and any agent, so it is held to the same rule as shared
workflows: nothing runs until the person trusts its exact text, and the
prompt shows every command before asking. A pull that changes the file
makes it untrusted.

- The trust prompt lists environments, every pane's command and directory,
  and every agent with its kind, worktree and arguments.
- Paths in the file may not leave the repository.
- No secret values belong in the file. `[vars]` refuses values that match
  known token formats (the redaction patterns), and secrets go in
  `hive.local.toml` or the shell.
- `vars` on the wire carries only the declared names, from the caller's own
  environment to its own daemon.
- Plugins and members cannot trust a project.

[docs/threat-model.md](../docs/threat-model.md) gains a row for project
files.

## Testing

- Unit tests for parsing (every error with its line), the main
  environment's defaults, paths outside the repository, secret-looking
  values, `hive.local.toml` layering, and the plan against fake workspaces.
- A fuzz target for the parser.
- An end-to-end test of the acceptance: an untrusted file refused, trusted
  once, `hive up` twice with the second changing nothing, `--plan` matching
  what ran, `hive down` keeping the worktrees, and a changed file asking
  again.

## Alternatives

- **Run `hive up` in the CLI.** It would need no new methods, but it would
  read the file on the client's machine, not the daemon's, so `--remote`
  would break. The desktop app and editors (M31) would each need their own
  copy.
- **Make `layout.apply` idempotent.** It would cover tabs but not agents,
  variables or trust. Its current behaviour is also part of API 1.
- **A JSON or YAML file.** Hive's configuration, workflows and plans are
  TOML, and a file people edit by hand should look like the others.

## Open questions

1. `hive up` starts an agent that exited again, in its pane, so that the
   file's agents run after `hive up`. Should that need a flag instead?
2. Should `hive down` close the project's tabs, or only stop their
   processes and leave the screens?

## Delivery

Each step is a pull request that links this RFC:

1. The file, `hive.local.toml`, trust, `project.*`, `hive up | down`,
   `hive project check | show | trust | list`.
2. Gates, workflows and required plugins from the file.
3. `hive project diff` and `hive project save`.
4. Toolchains, with their environment cached.
5. Secrets by reference.
6. Templates (`hive project init --from`) and the JSON Schema.
7. The acceptance test on macOS, Linux and Windows, and `docs/projects.md`;
   then tag `v1.11.0`.

## Decision

<!-- Written by the maintainer council: the decision, its reasons, and the date. -->
