# Projects: the same Hive for everyone who clones it

A **project file**, `hive.toml` at the root of a repository, declares the
Hive setup the repository is worked with:

- its environments, each in a directory of the repository;
- their tabs and panes, with the commands they run;
- the agents they start;
- the variables they need.

`hive up` makes the running workspace match the file. `hive down` stops it
again.

```sh
hive project check      # does the file check? (no daemon needed)
hive up --plan          # what hive up would do
hive up                 # do it: asks once to trust the file
hive up                 # again: nothing to do
hive down               # stop the agents, close the tabs, keep the worktrees
```

The file lives with the code, so when a project adds a service, its
`hive.toml` changes in the same commit, and everyone's next `hive up` picks
it up.

The design is in [RFC 0009](../rfcs/0009-projects-as-code.md).

## A project file

```toml
version = 1
name = "shop"            # the main environment's ID; default: the directory's name

# Plain settings for every pane. Secrets do not belong here.
[vars]
NODE_ENV = "development"

# Variables the project needs, by name. Their values come from
# hive.local.toml or from the shell that runs `hive up`.
[env]
required = ["DATABASE_URL"]
optional = ["STRIPE_KEY"]

[[tab]]
name = "dev"
split = "horizontal"     # side by side (the default), or "vertical" (stacked)

  [[tab.pane]]
  name = "server"
  command = ["npm", "run", "dev"]
  cwd = "web"            # relative to the environment's directory

  [[tab.pane]]
  name = "shell"         # no command: your shell

[[agent]]
name = "reviewer"
kind = ["claude", "codex"]   # in order of preference; or one kind
isolate = true               # in a worktree of its own

# More environments, each in its own directory of the repository.
[[environment]]
id = "shop-api"
path = "api"
  [[environment.tab]]
  name = "api"
    [[environment.tab.pane]]
    command = ["go", "run", "./cmd/api"]
```

- `version = 1` is required. A file written for a newer format is refused,
  with a hint to update Hive.
- The top-level `tab` and `agent` belong to the **main environment**, whose
  ID is `name` and whose directory is the file's.
- The top-level `vars` and `env` hold in every environment. Each
  `[[environment]]` can add its own `vars`, `env`, `tab` and `agent`.
- The file is checked strictly: unknown keys are errors, and errors point
  at their line.
- Paths (`path`, `cwd`) are relative to the file, and may not leave the
  repository.
- `command` is a list of arguments, as in `["npm", "run", "dev"]`, run
  without a shell. Use `["sh", "-c", "…"]` for a shell line. Without a
  command, a pane runs your shell.
- Within an environment, tab and agent names are unique. An agent gets a
  tab of its own named after it, so no tab may share an agent's name.

### Agents

| Key | Meaning |
|---|---|
| `name` | The agent's name, and its tab's (default: its first kind) |
| `kind` | An agent kind (`hive agent manifests`), or a list in order of preference: the first one installed starts |
| `isolate` | Start it in a worktree of its own, on a branch named by `[branches] agent_pattern` |
| `worktree` | Start it in a worktree on this branch instead |
| `base` | Where a new worktree's branch starts (default: `HEAD`) |
| `args` | Added to the agent's start command |

An agent that `hive up` starts in a worktree marks that worktree's
environment with `HIVE_PROJECT_AGENT=<name>`. `hive down` keeps the
worktree, and the next `hive up` starts the agent there again, with the
work it left.

### Variables

A project file holds plain settings in `[vars]` and the **names** of what
it needs in `[env]`. Values that look like secrets are refused in `[vars]`:
tokens and keys in known formats, private keys, URLs with passwords, and
names ending in `_TOKEN`, `_SECRET`, `_PASSWORD`, `_API_KEY` and the like.
Declare those in `[env]`, and give them values:

1. in `hive.local.toml`, which wins;
2. or in the shell that runs `hive up`. Only the names the project
   declares are passed on, nothing else from your environment;
3. otherwise the project's `[vars]` apply.

A `required` name with no value stops `hive up` before it changes
anything.

## hive.local.toml

Your own values and preferences, beside `hive.toml`. Add it to
`.gitignore`.

```toml
version = 1

[vars]                       # values for [env] names, and your own settings
DATABASE_URL = "postgres://localhost/shop"

[agent.reviewer]
kind = "codex"               # your preferred kind for this agent
args = ["--model", "o4"]
start = false                # never start this agent here
```

A `hive.local.toml` that git does not track is yours, and changing it needs
no trust. One that git tracks is part of the project, and is trusted with
`hive.toml`.

## Up, down and plans

`hive up [dir]` finds `hive.toml` in `dir` (default: the current
directory) or the nearest directory above it. It compares the file with
what runs:

| The file declares | It is there when | Otherwise |
|---|---|---|
| an environment | an environment with that ID exists | create it at its path |
| a variable | the environment has that value | set it |
| a tab | the environment has a tab with that name | create it with its panes |
| an agent | an agent with that name runs in the environment, or in a worktree of it | start it, or start it again if it exited |

What already runs is left alone, so `hive up` twice changes nothing. It
matches by name, so a tab you closed or an agent you stopped is made again
by the next `hive up`. Changing a tab that exists is not its job.

Some differences stop the plan instead, and nothing is changed while there
are any:

- an environment with the project's ID at another directory, or one that
  is a worktree;
- an agent kind no manifest knows, or none of an agent's kinds installed;
- a `required` variable with no value.

```
$ hive up --plan
+ environment shop at /src/shop, with DATABASE_URL, NODE_ENV
+ tab shop/dev: npm run dev | a shell
+ agent shop/reviewer: claude, in a worktree of its own
+ environment shop-api at /src/shop/api, with DATABASE_URL, NODE_ENV
+ tab shop-api/api: go run ./cmd/api
```

`+` creates, `~` changes, `-` stops, and `!` is a problem. `hive up` stops
at the first step that fails and reports what it did before it.

`hive down [dir]` stops the agents the file declares and closes the tabs
it declares. Environments stay, and so do the worktrees agents work in,
with any uncommitted work in them. `hive down --plan` says what it would
stop.

## Trust

A repository is code from someone else, and a project file can start any
command and any agent. So `hive up` runs a file only while it has the text
you trusted:

```sh
hive project show        # everything the file would run, and whether it is trusted
hive project trust       # trust it at its current text
hive project trust --revoke
hive project list        # the projects you trusted
```

The first `hive up`, and the first after the file changes, shows what the
file declares (every command, directory and agent) and the plan, then asks
once. `--yes` answers yes in scripts. Off a terminal without `--yes`,
`hive up` refuses and says to run `hive project trust`.

Trust records the SHA-256 of the file's text, so a pull that changes the
file makes it untrusted until you look at it again. Plugins and members
(M27) cannot trust a project. `hive up --plan` and `hive project check`
need no trust.

## In CI and editors

`hive project check [dir]` checks the file (and `hive.local.toml`) as
`hive up` would read it, without a daemon, and exits 1 when it does not
check. `--json` prints the resolved project.

## Remote machines

With `--remote` or `--machine`, `hive up` runs against that machine's
daemon, which reads `hive.toml` on its own disk. Give the project's
directory there, as an absolute path:

```sh
hive --machine build up /home/me/src/shop
```

Values for `[env]` names come from your shell, on this machine.

## API

| Method | Params | Result |
|---|---|---|
| `project.get` | `{dir}` | the project: root, file, digest, trusted, error and the resolved spec |
| `project.list` | `{}` | the trusted projects |
| `project.trust` | `{dir, revoke}` | the project |
| `project.plan` | `{dir, down, vars}` | the plan: actions and problems |
| `project.up` | `{dir, vars}` | the plan and how many of its steps were done |
| `project.down` | `{dir}` | the same |

`dir` is absolute, on the daemon's machine. `vars` carries values for the
names the project declares in `[env]`, and others are ignored. Plans carry
variables' names, never their values. The methods arrived at API level 18.

- **Members:** `project.trust` is closed. The others need the owner role
  across all environments, since they read files at a path the caller
  names and start what the file declares.
- **Plugins:** `project.trust` is closed. `project.up` and `project.down`
  need `hive:process`, and the reads need `hive:read`.
- **Go SDK:** `hiveapi.Client.Project`, `Projects`, `ProjectPlan`,
  `ProjectUp`, `ProjectDown` and `hiveapi.ParseProject`. Trusting is left
  to a person.

## Coming in M30

Gates, workflows and required plugins from the file; `hive project diff`
and `hive project save`; toolchains (Nix, devcontainers, mise, direnv);
secrets from your own store; templates (`hive project init --from`) and a
JSON Schema for editors. See the [roadmap](../ROADMAP.md#m30--projects-as-code--v1110--in-progress).
