# Quick start

From nothing to three agents working in parallel, in about five minutes.

## 1. Install

```sh
curl -fsSL https://hive.admir-saheta.com/install.sh | sh   # macOS and Linux; into ~/.local/bin
brew install --cask admirable-oss/tap/hive                    # or Homebrew
nix run github:admirable-oss/hive                             # or Nix
go install github.com/admirable-oss/hive/cmd/hive@latest      # or from source (Go 1.26+)
```

`HIVE_CHANNEL=preview` makes the installer take release candidates too, and
`HIVE_VERSION=v1.0.0` installs a given release. Later, `hive update` keeps
you current.

## 2. Open the multiplexer

```sh
hive
```

The daemon starts by itself, then the multiplexer opens, showing the
environment for the directory you are in if there is one. It works like tmux: press the prefix (**Ctrl+B**),
then a key. `Ctrl+B ?` lists every binding, and `Ctrl+B d` detaches, leaving
every agent running. `hive daemon install` starts the daemon at login too.

## 3. Start agents

From a shell (any shell, including one inside a pane):

```sh
cd ~/src/app
hive env create app --cwd .                         # an environment for the repository
hive agent start --kind claude --env app            # Claude Code in a new tab
hive agent start --kind codex --env app --worktree fix/login   # Codex on its own branch and checkout
```

Each agent is a pane you can watch and type into. Hive reads what each one is
doing from its screen, and the sidebar shows it: ● working, ◆ blocked (it
needs you), ✓ done. You get a notification when one needs you, unless you are
looking at it.

For exact states and resumable conversations, let the agents report to Hive
through their own hooks:

```sh
hive integration install claude
hive integration install codex
```

## 4. Drive agents from scripts, or from other agents

```sh
hive agent prompt <agent> "Add a test for the login redirect" --wait --read 40
hive agent wait <agent> --until done
hive agent list --json
```

An agent can do the same: `claude mcp add hive -- hive mcp` gives Claude Code
Hive's tools, and `hive --skill` prints a skill that teaches agents to use them.

## 5. Close the laptop

Agents keep running when you close the terminal, when SSH drops, and when the
daemon crashes or is upgraded. After a reboot, the next `hive` command restores
every pane where it was working, and agents resume their conversations
([persistence](persistence.md)).

## Where next

- [Concepts](concepts.md): sessions, environments, tabs, panes, agents.
- [Key bindings](keybindings.md) and the [configuration reference](config.md).
- [Agent guides](agents.md): Claude Code, Codex and the others.
- [Remote machines](remote.md): `hive --remote` and saved machines.
- [Plugins](plugins.md): extend Hive, plus key commands, sidebar tokens and the status bar.
- [Coming from herdr](migrating-from-herdr.md).
