package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Instructions is what the model is told about Hive's tools.
const Instructions = `Hive runs coding agents (Claude Code, Codex, OpenCode, Gemini, …) in terminals that keep running in the background. These tools let you start other agents, give them work, wait for them and read their results.

Typical flow: env_list (find the environment of the repository) → agent_start with worktree set to a new branch per agent, so parallel agents never edit the same checkout → agent_prompt with wait=true (or several agent_prompt without wait, then agent_wait on each) → agent_read to collect what each one did.

States: working, blocked (waiting for a decision such as a permission prompt: read it with agent_read, answer with agent_send_keys), done (finished a turn), idle (ready for a prompt), exited. Waits end early when an agent blocks or exits; the outcome says so. An outcome of timeout is not a failure: call agent_wait again. Agents are named by their pane ID (or process ID), as returned by agent_start and agent_list. Never stop agents you did not start.`

// waitDefault bounds waits that do not say, so a tool call returns while
// the client still waits for it; the model can wait again.
const waitDefault = 5 * time.Minute

type idArgs struct {
	ID string `json:"id" desc:"the agent's pane ID (or process ID)"`
}

type listArgs struct {
	All bool `json:"all,omitempty" desc:"include every terminal, not only recognised agents"`
}

type startArgs struct {
	Kind          string   `json:"kind" desc:"the agent: claude, codex, opencode, gemini, cursor-agent, copilot, amp or aider"`
	EnvironmentID string   `json:"environment_id" desc:"the environment to start in (env_list)"`
	Worktree      string   `json:"worktree,omitempty" desc:"a branch: start in a new git worktree on it, with its own environment (recommended for parallel agents)"`
	Base          string   `json:"base,omitempty" desc:"where a new worktree branch starts (default HEAD)"`
	Name          string   `json:"name,omitempty" desc:"a name for its tab and pane"`
	Args          []string `json:"args,omitempty" desc:"extra command-line arguments for the agent"`
}

type promptArgs struct {
	ID             string `json:"id" desc:"the agent's pane ID"`
	Text           string `json:"text" desc:"the prompt"`
	Wait           bool   `json:"wait,omitempty" desc:"wait until the agent finishes the turn"`
	Until          string `json:"until,omitempty" desc:"with wait: done (default) or idle"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" desc:"with wait: give up waiting after this long (default 300); the agent keeps working"`
	ReadLines      int    `json:"read_lines,omitempty" desc:"return the agent's last n lines when the prompt ends (40 or more: full-screen agents keep their input box at the bottom)"`
}

type waitArgs struct {
	ID             string  `json:"id" desc:"the agent's pane ID"`
	Until          string  `json:"until,omitempty" desc:"idle (default: ready for a prompt), done (finished a turn), working, blocked, exited or change"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty" desc:"give up after this long (default 300)"`
	After          *uint64 `json:"after,omitempty" desc:"with until=done: the completion_seq you already saw"`
	ReadLines      int     `json:"read_lines,omitempty" desc:"also return the agent's last n lines"`
}

type readArgs struct {
	ID     string `json:"id" desc:"the agent's (or pane's) ID"`
	Source string `json:"source,omitempty" desc:"visible (the screen, default), recent (with lines scrolled off), recent-unwrapped or history"`
	Lines  int    `json:"lines,omitempty" desc:"only the last n lines (blank runs collapsed)"`
}

type keysArgs struct {
	ID   string   `json:"id" desc:"the agent's (or pane's) ID"`
	Keys []string `json:"keys" desc:"named keys: Enter, Escape, Up, Down, Tab, C-c, y, 1, …"`
}

type renameArgs struct {
	ID   string `json:"id" desc:"the agent's pane ID"`
	Name string `json:"name"`
}

type paneListArgs struct {
	EnvironmentID string `json:"environment_id,omitempty" desc:"only this environment's panes"`
	TabID         string `json:"tab_id,omitempty" desc:"only this tab's panes"`
}

type runArgs struct {
	ID      string `json:"id" desc:"the pane's ID"`
	Command string `json:"command" desc:"a command line, typed and sent with Enter"`
}

type envCreateArgs struct {
	ID   string `json:"id" desc:"a name: lower-case letters, digits and dashes"`
	Path string `json:"path,omitempty" desc:"an existing directory (absolute); empty for a scratch directory Hive manages"`
}

type none struct{}

func seconds(n int) time.Duration {
	if n <= 0 {
		return waitDefault
	}
	return time.Duration(n) * time.Second
}

// HiveTools are the tools `hive mcp` serves, over c.
func HiveTools(c *hiveapi.Client) []Tool {
	read := func(ctx context.Context, id string, n int) []string {
		if n <= 0 {
			return nil
		}
		lines, _ := c.Read(ctx, id, hiveapi.ReadOptions{Source: "recent", Lines: 10000})
		return agent.Tail(agent.Compact(lines), n)
	}
	ro := &Annotations{ReadOnly: true}
	tools := []Tool{
		NewTool("agent_list", "List the coding agents in Hive with their states (working, blocked, done, idle, exited), environments and pane IDs.",
			func(ctx context.Context, a listArgs) (any, error) { return c.Agents(ctx, a.All) }),
		NewTool("agent_get", "Show one agent: its kind, state, why, and completion_seq (the number of turns it finished).",
			func(ctx context.Context, a idArgs) (any, error) { return c.Agent(ctx, a.ID) }),
		NewTool("agent_start", "Start a coding agent in a new tab. Set worktree to a new branch name to give it its own git checkout; do this for every agent working in parallel on one repository. Returns the agent with its pane ID, which names it in the other tools.",
			func(ctx context.Context, a startArgs) (any, error) {
				res, err := c.StartAgent(ctx, hiveapi.StartAgent{
					Kind: a.Kind, EnvironmentID: a.EnvironmentID, Worktree: a.Worktree, Base: a.Base, Name: a.Name, Args: a.Args,
				})
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"id": res.Pane.ID, "process_id": res.Agent.ID, "kind": res.Agent.Kind,
					"environment_id": res.Environment.ID, "path": res.Environment.Path, "state": res.Agent.State,
				}, nil
			}),
		NewTool("agent_prompt", "Send an agent a prompt. It waits for the agent to be ready, pastes the prompt whole and submits it, and returns once the agent starts working; with wait=true, once it finishes. Refused while the agent is blocked on a decision. The outcome is reached/started, blocked, stalled, exited or timeout.",
			func(ctx context.Context, a promptArgs) (any, error) {
				o := hiveapi.PromptOptions{Wait: a.Wait, Until: hiveapi.Until(a.Until), Read: a.ReadLines}
				if a.Wait {
					o.Timeout = seconds(a.TimeoutSeconds)
				}
				return c.Prompt(ctx, a.ID, a.Text, o)
			}),
		NewTool("agent_wait", "Wait for an agent: until idle (ready for a prompt), done (finished a turn), working, blocked, exited or any change. Ends early when the agent blocks or exits. A timeout outcome means it is still going: wait again.",
			func(ctx context.Context, a waitArgs) (any, error) {
				res, err := c.Wait(ctx, a.ID, hiveapi.WaitOptions{Until: hiveapi.Until(a.Until), After: a.After, Timeout: seconds(a.TimeoutSeconds)})
				if err != nil {
					return nil, err
				}
				return map[string]any{"outcome": res.Outcome, "agent": res.Agent, "output": read(ctx, a.ID, a.ReadLines)}, nil
			}),
		NewTool("agent_read", "Read what an agent shows: its screen, or with source=recent-unwrapped its recent output as plain lines. Use it to collect an agent's results or see what a blocked agent asks.",
			func(ctx context.Context, a readArgs) (any, error) {
				lines, err := c.Read(ctx, a.ID, hiveapi.ReadOptions{Source: a.Source, Lines: readAll(a)})
				return readText(lines, err, a.Lines)
			}),
		NewTool("agent_send_keys", "Press keys in an agent, for example to answer a permission prompt (read it first): [\"1\"], [\"Down\", \"Enter\"], [\"Escape\"].",
			func(ctx context.Context, a keysArgs) (any, error) {
				if err := c.SendKeys(ctx, a.ID, a.Keys...); err != nil {
					return nil, err
				}
				return "ok", nil
			}),
		NewTool("agent_explain", "Show why Hive thinks an agent is in its state: the rule that matched its screen, any report, and the screen.",
			func(ctx context.Context, a idArgs) (any, error) { return c.Explain(ctx, a.ID) }),
		NewTool("agent_rename", "Rename an agent's pane.",
			func(ctx context.Context, a renameArgs) (any, error) { return c.RenameAgent(ctx, a.ID, a.Name) }),
		NewTool("agent_stop", "Stop an agent's process. Only stop agents you started.",
			func(ctx context.Context, a idArgs) (any, error) { return c.StopAgent(ctx, a.ID) }),
		NewTool("pane_list", "List panes, with the process each one runs.",
			func(ctx context.Context, a paneListArgs) (any, error) { return c.Panes(ctx, a.EnvironmentID, a.TabID) }),
		NewTool("pane_read", "Read what a pane shows.",
			func(ctx context.Context, a readArgs) (any, error) {
				lines, err := c.ReadPane(ctx, a.ID, hiveapi.ReadOptions{Source: a.Source, Lines: readAll(a)})
				return readText(lines, err, a.Lines)
			}),
		NewTool("pane_run", "Type a command line into a pane (a shell) and press Enter.",
			func(ctx context.Context, a runArgs) (any, error) {
				if err := c.RunInPane(ctx, a.ID, a.Command); err != nil {
					return nil, err
				}
				return "ok", nil
			}),
		NewTool("env_list", "List Hive's environments: each is a directory (a repository or worktree) that agents run in.",
			func(ctx context.Context, _ none) (any, error) { return c.Environments(ctx) }),
		NewTool("env_create", "Create an environment for a directory.",
			func(ctx context.Context, a envCreateArgs) (any, error) {
				return c.CreateEnvironment(ctx, a.ID, a.Path, nil)
			}),
		NewTool("snapshot", "Everything at once: environments, tabs, panes, processes and agents with their states.",
			func(ctx context.Context, _ none) (any, error) { return c.Snapshot(ctx) }),
	}
	for i := range tools {
		switch tools[i].Name {
		case "agent_list", "agent_get", "agent_wait", "agent_read", "agent_explain", "pane_list", "pane_read", "env_list", "snapshot":
			tools[i].Annotations = ro
		case "agent_stop":
			tools[i].Annotations = &Annotations{Destructive: true}
		}
	}
	return tools
}

// readText is a read as the model sees it: blank runs collapsed (agents'
// screens are mostly empty rows), then the last n lines.
func readText(lines []string, err error, n int) (any, error) {
	if err != nil {
		return nil, err
	}
	return strings.Join(agent.Tail(agent.Compact(lines), n), "\n"), nil
}

// readAll asks for enough lines that n remain once blank runs are gone:
// history reads take every line kept, up to the daemon's limit.
func readAll(a readArgs) int {
	if a.Source == "" || a.Source == "visible" {
		return 0 // the screen
	}
	return 10000
}

// Describe is a one-line summary of the tools, for logs.
func Describe(tools []Tool) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return fmt.Sprintf("%d tools: %s", len(tools), strings.Join(names, ", "))
}
