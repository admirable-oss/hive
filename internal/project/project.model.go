// Package project reads a repository's hive.toml, the Hive setup it is
// worked with, and makes the running workspace match it: environments,
// their tabs and panes, the agents they start and the variables they need.
// Nothing in a project file runs until the person trusts its current text.
package project

import (
	"errors"
)

// Version is the project file format this hive reads.
const Version = 1

// The files a project is made of.
const (
	FileName      = "hive.toml"
	LocalFileName = "hive.local.toml"
)

// EnvProjectAgent marks the worktree environment hive up started a
// project's agent in, by the agent's name, so the next hive up starts it
// there again rather than in a new worktree.
const EnvProjectAgent = "HIVE_PROJECT_AGENT"

// MaxFileBytes bounds a project file.
const MaxFileBytes = 1 << 20

// schema is the trust file's schema.
const schema = 1

var (
	// ErrInvalid is a project file that does not parse or does not check.
	ErrInvalid = errors.New("invalid project file")
	// ErrNotFound is a directory with no hive.toml in it or above it.
	ErrNotFound = errors.New("no project file")
	// ErrUntrusted is a project file whose text is not trusted.
	ErrUntrusted = errors.New("project file not trusted")
	// ErrState is a plan that cannot be applied as things are.
	ErrState = errors.New("cannot apply the project file")
	// ErrNewer is state written by a newer hive, which is left alone.
	ErrNewer = errors.New("written by a newer hive")
)

// Spec is a project file, resolved: its environments with their roots,
// and every default filled in. Values from hive.local.toml are not in it.
type Spec struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	// Environments are the project's; the first is the main one, at the
	// file's directory.
	Environments []Environment `json:"environments"`
	// Local says a hive.local.toml is layered over the file.
	Local bool `json:"local,omitempty"`
	// Toolchain is where panes start by default: none, auto or one kind.
	Toolchain Toolchain `json:"toolchain,omitempty"`
	// Gates are branch gates for the project's repository.
	Gates []Gate `json:"gates,omitempty"`
	// Workflows are directories and files of workflows, relative to the
	// root, trusted with the file.
	Workflows []string `json:"workflows,omitempty"`
	// Plugins are the plugins the project needs, by ID.
	Plugins []string `json:"plugins,omitempty"`

	// file is the project file as written, which save and templates
	// render again.
	file fileProject
}

// Gate is a branch gate a project declares: a command that must pass in a
// branch's worktree before the branch is ready.
type Gate struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	// TimeoutMS bounds it; 0 is the branch service's default.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// Toolchain is where a project's panes start: inside a Nix dev shell, a
// devcontainer, mise's or direnv's environment, or none.
type Toolchain string

// The toolchains.
const (
	ToolchainNone         Toolchain = "none"
	ToolchainAuto         Toolchain = "auto"
	ToolchainNix          Toolchain = "nix"
	ToolchainDevcontainer Toolchain = "devcontainer"
	ToolchainMise         Toolchain = "mise"
	ToolchainDirenv       Toolchain = "direnv"
)

// ToolchainKinds are the toolchains a file may name.
var ToolchainKinds = []Toolchain{ToolchainNone, ToolchainAuto, ToolchainNix, ToolchainDevcontainer, ToolchainMise, ToolchainDirenv}

// Environment is one environment a project declares.
type Environment struct {
	ID string `json:"id"`
	// Path is the environment's directory, relative to the project's root;
	// Root is the same, absolute.
	Path string `json:"path"`
	Root string `json:"root"`
	// Vars are plain settings for every pane, from the project file.
	Vars map[string]string `json:"vars,omitempty"`
	// Required and Optional name variables whose values come from
	// hive.local.toml or the shell that runs hive up.
	Required []string `json:"required,omitempty"`
	Optional []string `json:"optional,omitempty"`
	// Secrets are references to the person's secret store, by variable
	// (keychain:, secret-service:, pass:, op://); never values.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Toolchain is the environment's own, or the project's.
	Toolchain Toolchain `json:"toolchain,omitempty"`
	Tabs      []Tab     `json:"tabs,omitempty"`
	Agents    []Agent   `json:"agents,omitempty"`
}

// Split is how a tab's panes share it.
type Split string

const (
	// SplitHorizontal places panes side by side.
	SplitHorizontal Split = "horizontal"
	// SplitVertical stacks them.
	SplitVertical Split = "vertical"
)

// Tab is a tab and its panes.
type Tab struct {
	Name  string `json:"name"`
	Split Split  `json:"split"`
	Panes []Pane `json:"panes"`
}

// Pane is one pane of a tab.
type Pane struct {
	Name string `json:"name,omitempty"`
	// Command is the pane's argv; empty runs the shell.
	Command []string `json:"command,omitempty"`
	// Cwd is relative to the environment's root.
	Cwd string `json:"cwd,omitempty"`
}

// Agent is an agent an environment starts.
type Agent struct {
	Name string `json:"name"`
	// Kinds are agent kinds in order of preference: the first installed
	// one starts.
	Kinds []string `json:"kinds"`
	// Isolate starts it in a worktree of its own, on a branch named by
	// [branches] agent_pattern; Worktree names the branch instead.
	Isolate  bool     `json:"isolate,omitempty"`
	Worktree string   `json:"worktree,omitempty"`
	Base     string   `json:"base,omitempty"`
	Args     []string `json:"args,omitempty"`
	// Skip is set by hive.local.toml's start = false.
	Skip bool `json:"skip,omitempty"`
}

// Project is a project file as the daemon sees it.
type Project struct {
	// Root is the directory the file is in.
	Root string `json:"root"`
	File string `json:"file"`
	// LocalFile is hive.local.toml's path, when there is one.
	LocalFile string `json:"local_file,omitempty"`
	// Digest is the SHA-256 of what trust covers: the file, and a
	// hive.local.toml that git tracks.
	Digest  string `json:"digest"`
	Trusted bool   `json:"trusted"`
	// Error says why the file does not parse; Spec is nil then.
	Error string `json:"error,omitempty"`
	Spec  *Spec  `json:"spec,omitempty"`
}

// Op is what one step of a plan does.
type Op string

// The steps of plans.
const (
	OpCreateEnvironment Op = "create-environment"
	OpSetVars           Op = "set-vars"
	OpCreateTab         Op = "create-tab"
	OpStartAgent        Op = "start-agent"
	OpRestartAgent      Op = "restart-agent"
	OpStopAgent         Op = "stop-agent"
	OpCloseTab          Op = "close-tab"
)

// Action is one step of a plan. Variable values are never in it, only
// their names.
type Action struct {
	Op          Op     `json:"op"`
	Environment string `json:"environment"`
	// Name is the tab's or the agent's.
	Name string `json:"name,omitempty"`
	// ID is the tab's or the agent's, for steps on what exists.
	ID string `json:"id,omitempty"`
	// Root is a new environment's directory.
	Root string `json:"root,omitempty"`
	// Vars names the variables a step sets.
	Vars []string `json:"vars,omitempty"`
	// Commands are a new tab's panes, as they run ("" for a shell).
	Commands []string `json:"commands,omitempty"`
	// Kind, Worktree and Isolate describe an agent to start.
	Kind     string `json:"kind,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Isolate  bool   `json:"isolate,omitempty"`
	// In is the worktree environment an agent starts in again: the one an
	// earlier hive up made for it, which hive down kept.
	In string `json:"in,omitempty"`
	// Toolchain is the one a new tab's panes or an agent start in, and
	// ToolchainCached says its environment is ready.
	Toolchain       Toolchain `json:"toolchain,omitempty"`
	ToolchainCached bool      `json:"toolchain_cached,omitempty"`
	// Secrets name the variables read from the person's secret store for
	// a new tab's panes or an agent.
	Secrets []string `json:"secrets,omitempty"`

	// What applying needs, kept off the wire.
	values map[string]string
	tab    *Tab
	agent  *Agent
	// tabID is the tab a stopped agent runs in, which down closes.
	tabID string
	// tc and refs are the toolchain and the secret references (by
	// variable) new panes start with.
	tc   Resolved
	refs map[string]string
}

// Plan is what makes the workspace match a project file, or, for down,
// what stops it.
type Plan struct {
	Root    string   `json:"root"`
	Down    bool     `json:"down,omitempty"`
	Actions []Action `json:"actions"`
	// Present counts what the file declares and already runs.
	Present int `json:"present"`
	// Plugins are the plugins the project needs that are not installed:
	// hive up installs them from the catalog first.
	Plugins []string `json:"plugins,omitempty"`
	// Problems stop the plan: nothing is changed while there are any.
	Problems []string `json:"problems,omitempty"`
}

// Applied is what up or down did.
type Applied struct {
	Plan Plan `json:"plan"`
	// Done counts the plan's actions carried out, in order.
	Done int `json:"done"`
}
