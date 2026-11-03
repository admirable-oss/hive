# Projects: the same Hive for everyone who clones it

A **project file**, `hive.toml` at the root of a repository, declares the
Hive setup the repository is worked with:

- its environments, each in a directory of the repository;
- their tabs and panes, with the commands they run;
- the agents they start;
- the variables and secrets they need;
- the toolchain panes start in (Nix, a devcontainer, mise, direnv);
- its branch gates, workflows and plugins.

`hive up` makes the running workspace match the file, and `hive down`
stops it again.

```sh
hive project init       # write a hive.toml from a template, fitted to the repository
hive project check      # does the file check? (no daemon needed)
hive up --plan          # what hive up would do
hive up                 # do it: asks once to trust the file
hive up                 # again: nothing to do
hive project diff       # how what runs differs from the file
hive project save       # write the tabs you opened back to the file
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
toolchain = "auto"       # start panes in the repository's toolchain
workflows = [".hive/workflows"]
plugins = ["pr-status"]

# Plain settings for every pane. Secrets do not belong here.
[vars]
NODE_ENV = "development"

# Variables the project needs, by name. Their values come from
# hive.local.toml, your secret store or the shell that runs `hive up`.
[env]
required = ["DATABASE_URL"]
optional = ["STRIPE_KEY"]

[[gate]]
name = "test"
command = ["npm", "test"]
timeout = "10m"

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
- The top-level `toolchain`, `vars`, `secrets` and `env` hold in every
  environment. Each `[[environment]]` can set its own `toolchain` and add
  its own `vars`, `secrets`, `env`, `tab` and `agent`.
- The file is checked strictly: unknown keys are errors, and errors point
  at their line.
- Paths (`path`, `cwd`, `workflows`) are relative to the file, and may not
  leave the repository.
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

### Variables and secrets

A project file holds plain settings in `[vars]`, the **names** of what it
needs in `[env]`, and **references** to secrets in `[secrets]`, never their
values. Values that look like secrets are refused in `[vars]`: tokens and
keys in known formats, private keys, URLs with passwords, and names ending
in `_TOKEN`, `_SECRET`, `_PASSWORD`, `_API_KEY` and the like.

A secret reference names a value in your own store:

| Reference | Store | Read with |
|---|---|---|
| `keychain:<service>[/<account>]` | the macOS keychain | `security find-generic-password` |
| `secret-service:<attr>=<value>[,…]` | GNOME Keyring, KWallet (the Secret Service) | `secret-tool lookup` |
| `pass:<path>` | pass (the first line) | `pass show` |
| `op://<vault>/<item>/<field>` | 1Password | `op read` |

References usually belong in `hive.local.toml`, since stores are personal.
A project can put shared ones in `hive.toml` when everyone's stores agree,
such as a team's 1Password vault.

Each variable's value comes from the last of these that has one:

1. the project's `[vars]`;
2. the project's `[secrets]`;
3. the shell that runs `hive up`, for the names the project declares, and
   nothing else from your environment;
4. `hive.local.toml`'s `[secrets]`;
5. `hive.local.toml`'s `[vars]`.

A `required` name with no value or reference stops `hive up` before it
changes anything.

Values go to the environment's variables. Secrets do not: `hive up` reads
them when it starts panes and agents, and gives them only to those
processes. A plan never reads a store, and no plan, file or log holds a
secret's value. Hive keeps a process's variables where it keeps its other
state (`HIVE_HOME`, readable only by you), to restart it after a reboot.
A secret that changes in the store reaches panes started after the change.

### Toolchains

`toolchain` says where panes start:

| Toolchain | Marked by | Panes start |
|---|---|---|
| `nix` | `flake.nix` | in `nix develop`'s environment |
| `devcontainer` | `.devcontainer/devcontainer.json` | inside the container (`devcontainer up`, then `devcontainer exec`) |
| `mise` | `mise.toml`, `.mise.toml`, `.tool-versions` | in `mise exec`'s environment |
| `direnv` | `.envrc` | in `direnv exec`'s environment (after `direnv allow`) |
| `auto` | | in whichever of these the directory has, in this order |
| `none` | | as they are (the default) |

The marker is looked for in the environment's directory and above it, up to
the project's root. The toolchain's program must be on the daemon's PATH.

For Nix, mise and direnv, `hive up` runs the toolchain once to learn the
variables it sets, keeps them in `HIVE_HOME/projects/toolchains`, and starts
panes with them. The cache is keyed on the toolchain's files (`flake.nix`
and `flake.lock`, `mise.toml`, `.envrc`, …), so a change to them makes it
again. After the first time, panes open as fast as without a toolchain.
`hive up --plan` says whether a toolchain's environment is ready
("made first" when it is not).

A devcontainer runs its panes inside the container. Agents always run on
the host, where Hive can tell what they are doing.

`toolchain` in `hive.local.toml` replaces the project's for you, for
example `"none"` to start everything on the host.

### Gates, workflows and plugins

- **`[[gate]]`** declares [branch gates](branches.md#gates) for the
  project's repository: commands that must pass in a branch's worktree
  before `hive branch ready` marks it ready. They add to `[[branches.gate]]`
  in your configuration, while the file is trusted.
- **`workflows`** lists [workflow](workflows.md) directories and files. Their
  triggers fire while the project file is trusted, at the text of the
  workflow files when you trusted it: they need no `hive workflow trust`
  of their own, and changing one makes the whole project untrusted.
- **`plugins`** lists the plugins the project needs, by ID. `hive up`
  installs the missing ones from the catalog first, each after its preview
  and a yes (`--yes` answers it).

## hive.local.toml

Your own values and preferences, beside `hive.toml`. `hive project init`
adds it to `.gitignore`.

```toml
version = 1
toolchain = "none"           # your own toolchain, for every environment

[vars]                       # values for [env] names, and your own settings
DATABASE_URL = "postgres://localhost/shop"

[secrets]                    # references to your own secret store
STRIPE_KEY = "keychain:shop-stripe"

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
| a plugin | it is installed | install it from the catalog |

What already runs is left alone, so `hive up` twice changes nothing. It
matches by name, so a tab you closed or an agent you stopped is made again
by the next `hive up`. Changing a tab that exists is not its job: see
`hive project diff` and `save` below.

Some differences stop the plan instead, and nothing is changed while there
are any:

- an environment with the project's ID at another directory, or one that
  is a worktree;
- an agent kind no manifest knows, or none of an agent's kinds installed;
- a `required` variable with no value;
- a toolchain the file names that is not there, or not installed.

```
$ hive up --plan
+ environment shop at /src/shop, with DATABASE_URL, NODE_ENV
+ tab shop/dev: npm run dev | a shell (in nix's environment, made first)
+ agent shop/reviewer: claude, in a worktree of its own (with secrets STRIPE_KEY)
+ environment shop-api at /src/shop/api, with DATABASE_URL, NODE_ENV
+ tab shop-api/api: go run ./cmd/api
```

`+` creates, `~` changes, `-` stops, and `!` is a problem. Before it
changes anything, `hive up` makes the toolchains' environments and reads
the secrets, so a failure there leaves the workspace as it was. After that
it stops at the first step that fails, and reports what it did before it.

`hive down [dir]` stops the agents the file declares and closes the tabs
it declares. Environments stay, and so do the worktrees agents work in,
with any uncommitted work in them. `hive down --plan` says what it would
stop.

## Diff and save

```
$ hive project diff
missing  tab shop/logs
changed  tab shop/dev: pane 1 runs npm start, declared npm run dev
extra    tab shop/scratch: htop | a shell
extra    agent hive-codex-3f2a/helper: codex
```

- **missing**: the file declares it, and it does not run (`hive up` makes
  it).
- **extra**: it runs, and the file does not declare it.
- **changed**: a declared tab whose panes differ: their number, commands,
  directories or split.

`hive project save` writes the tabs you opened (extra) and the panes of
declared tabs you changed back to `hive.toml`. It removes nothing, and
leaves agents to you. A tab is written as a row or a column of panes, so a
layout of mixed splits is flattened. The file is written in its canonical
form, which does not keep comments: `save` lists the changes and asks
first (`--yes` answers yes), and `--print` shows the new text without
writing it. A trusted file stays trusted at the text you saved.

## Templates

`hive project init [dir]` writes a `hive.toml` from a template, fitted to
what is in the directory:

| Template | Writes |
|---|---|
| `blank` (the default) | a tab with a shell (and the dev server, when `package.json` has a `dev` script), `toolchain = "auto"` when there is one, and the tests as a gate |
| `agents` | `blank`, with a reviewer agent and a fixer agent in a worktree of its own |
| `test-watcher` | `blank`, with the tests running in a tab of their own and a fixer agent |
| `monorepo` | an environment per package (directories with `go.mod`, `package.json`, `Cargo.toml` or `pyproject.toml`, at the top or under `packages/`, `apps/`, `services/`, …), each with a shell and an agent |

The tests are found from `go.mod` (`go test ./...`), `package.json` (a
`test` script), `Cargo.toml`, `pyproject.toml` or a `Makefile` with a
`test` target.

`--from` also takes:

- `<plugin>/<template>`: a template an installed plugin ships;
- a file: another project's `hive.toml`, or a directory with one.

`hive project init --list` lists them all. `init` never writes over a
`hive.toml` without `--force`.

### Templates in plugins

A plugin ships templates in its package, and declares them in its manifest
(any plugin API):

```toml
[[contributes.templates]]
name = "service"
title = "A Go service with a reviewer"
file = "templates/service.toml"
```

`hive project init --from my-plugin/service` writes it, after checking it
as `hive up` would read it. A team publishes its templates as a plugin in
the registry, like any other ([docs/plugins/publishing.md](plugins/publishing.md)).

## Editors

`hive project schema` prints the JSON Schema of `hive.toml` (`--local` for
`hive.local.toml`). TOML language servers use it for completion and checks.
For Taplo and Even Better TOML:

```sh
mkdir -p .hive && hive project schema > .hive/hive.schema.json
```

Then add this line at the top of `hive.toml`:

```toml
#:schema ./.hive/hive.schema.json
```

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

Trust covers `hive.toml`, a `hive.local.toml` that git tracks, and the
workflow files the project lists. The first `hive up`, and the first after
any of them changes, shows what the file declares and the plan, then asks
once:

- the declaration: every command, directory, agent, gate and workflow;
- the plan: what `hive up` would do.

`--yes` answers yes in scripts. Off a terminal without `--yes`, `hive up`
refuses and says to run `hive project trust`.

Trust records a SHA-256 of that text, so a pull that changes any of it
makes the project untrusted until you look again. Plugins and members (M27)
cannot trust or save a project. `hive up --plan`, `hive project diff` and
`hive project check` need no trust.

## In CI and editors

`hive project check [dir]` checks the file (and `hive.local.toml`) as
`hive up` would read it, without a daemon, and exits 1 when it does not
check. `--json` prints the resolved project.

## Remote machines

With `--remote` or `--machine`, `hive up` runs against that machine's
daemon, which reads `hive.toml`, toolchains and secret stores on its own
machine. Give the project's directory there, as an absolute path:

```sh
hive --machine build up /home/me/src/shop
```

Values for `[env]` names come from your shell, on this machine. Plugins the
project needs are installed there, with `hive --machine build plugin
install <id>`.

## API

| Method | Params | Result |
|---|---|---|
| `project.get` | `{dir}` | the project: root, file, digest, trusted, error and the resolved spec |
| `project.list` | `{}` | the trusted projects |
| `project.trust` | `{dir, revoke}` | the project |
| `project.plan` | `{dir, down, vars}` | the plan: actions, plugins to install and problems |
| `project.up` | `{dir, vars}` | the plan and how many of its steps were done |
| `project.down` | `{dir}` | the same |
| `project.diff` | `{dir, vars}` | what is missing, extra or changed |
| `project.save` | `{dir, write}` | the new text, its changes, and whether it was written |

`dir` is absolute, on the daemon's machine. `vars` carries values for the
names the project declares in `[env]`, and others are ignored. Plans carry
the names of variables and secrets, never their values. The methods arrived
at API level 18.

- **Members:** `project.trust` and `project.save` are closed. The others
  need the owner role across all environments, since they read files at a
  path the caller names and start what the file declares.
- **Plugins:** `project.trust` and `project.save` are closed.
  `project.up` and `project.down` need `hive:process`, and the reads need
  `hive:read`.
- **Go SDK:** `hiveapi.Client.Project`, `Projects`, `ProjectPlan`,
  `ProjectUp`, `ProjectDown`, `DiffProject` and `hiveapi.ParseProject`.
  Trusting and saving are left to a person.
