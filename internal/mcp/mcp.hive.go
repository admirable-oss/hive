package mcp

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/task"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Instructions is what the model is told about Hive's tools.
const Instructions = `Hive runs coding agents (Claude Code, Codex, OpenCode, Gemini, …) in terminals that keep running in the background. These tools let you start other agents, give them work, wait for them and read their results.

For work in several steps, write a plan and run it with task_run: tasks with prompts, needs between them, isolate=true so each gets its own worktree, and approve=true for steps the user must allow. Hive runs them in parallel within limits and keeps each result; task_wait with until=attention returns when a task needs a person.

Typical flow: env_list (find the environment of the repository) → agent_start with worktree set to a new branch per agent, so parallel agents never edit the same checkout → agent_prompt with wait=true (or several agent_prompt without wait, then agent_wait on each) → agent_read to collect what each one did.

Each worktree's branch is followed: branch_list shows which branches overlap or would conflict, branch_diff reads a branch's changes, branch_comment sends review comments to the agent on it, and branch_ready runs its tests and other gates. Pushing, merging and pull requests are left to the user.

Workflows are the user's reusable steps (agents, commands, plugin actions), started by triggers or by hand: workflow_list shows them and their inputs, workflow_run runs one by name, workflow_wait waits for a run and returns its report (each step's outcome and output), and workflow_runs lists recent runs. Only run a workflow the user asked for.

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
	Isolate       bool     `json:"isolate,omitempty" desc:"start in a new git worktree on a branch Hive names (when worktree names none)"`
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

type planRunArgs struct {
	Plan string `json:"plan" desc:"the plan file's TOML: name, max_parallel, [defaults] and [[task]] tables with id, prompt, kind, environment, needs, isolate, until, approve (see hive task run --help)"`
}

type planArgs struct {
	ID   string `json:"id" desc:"the plan's ID (or a unique prefix)"`
	Task string `json:"task,omitempty" desc:"one of its tasks"`
}

type planWaitArgs struct {
	ID             string `json:"id" desc:"the plan's ID (or a unique prefix)"`
	Task           string `json:"task,omitempty" desc:"wait for this task only"`
	Until          string `json:"until,omitempty" desc:"finished (default) or attention (also when a task waits for approval or its agent is blocked)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" desc:"give up after this long (default 300); the plan keeps running"`
}

type approveArgs struct {
	ID   string `json:"id" desc:"the plan's ID (or a unique prefix)"`
	Task string `json:"task,omitempty" desc:"the task waiting for approval (may be left out when one waits)"`
	Deny bool   `json:"deny,omitempty" desc:"refuse: cancel the task instead"`
	Note string `json:"note,omitempty" desc:"why"`
}

type branchListArgs struct {
	Repo string `json:"repo,omitempty" desc:"only this repository's branches (its main working tree)"`
	All  bool   `json:"all,omitempty" desc:"include cleaned and removed branches"`
}

type branchArgs struct {
	Ref string `json:"ref" desc:"the branch: its ID, environment, agent ID, plan/task or name"`
}

type branchDiffArgs struct {
	Ref   string   `json:"ref" desc:"the branch: its ID, environment, agent ID, plan/task or name"`
	Paths []string `json:"paths,omitempty" desc:"only these files"`
	Stat  bool     `json:"stat,omitempty" desc:"only the files and their line counts"`
}

type branchConflictsArgs struct {
	Ref  string `json:"ref" desc:"the branch"`
	With string `json:"with,omitempty" desc:"another branch to merge with (default: its base)"`
}

type branchCommentArgs struct {
	Ref  string `json:"ref" desc:"the branch"`
	Body string `json:"body" desc:"the comment"`
	Path string `json:"path,omitempty" desc:"the file it is about"`
	Line int    `json:"line,omitempty" desc:"the line it is about"`
	Send bool   `json:"send,omitempty" desc:"send it, with the comments not sent yet, to the branch's agent now"`
}

type workflowRunArgs struct {
	Name   string            `json:"name" desc:"the workflow's name (workflow_list)"`
	Inputs map[string]string `json:"inputs,omitempty" desc:"its inputs, by name"`
	Dir    string            `json:"dir,omitempty" desc:"the directory its steps run in unless they name an environment (absolute)"`
}

type workflowRunsArgs struct {
	Workflow string `json:"workflow,omitempty" desc:"only this workflow's runs"`
	Limit    int    `json:"limit,omitempty" desc:"at most this many (default 20)"`
}

type workflowRunRef struct {
	ID string `json:"id" desc:"the run's ID (or a unique prefix)"`
}

type workflowWaitArgs struct {
	ID             string `json:"id" desc:"the run's ID (or a unique prefix)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" desc:"give up after this long (default 300); the run keeps going"`
}

// mcpPatchBytes bounds a diff a model reads.
const mcpPatchBytes = 128 << 10

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
					Kind: a.Kind, EnvironmentID: a.EnvironmentID, Worktree: a.Worktree, Isolate: a.Isolate, Base: a.Base, Name: a.Name, Args: a.Args,
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
		NewTool("task_run", "Run a plan: tasks for agents with dependencies (needs), run in parallel within limits, each in its own worktree with isolate=true, with results kept. Prefer it to starting and prompting agents one by one for work in several steps. Returns the plan with its ID.",
			func(ctx context.Context, a planRunArgs) (any, error) {
				p, err := c.RunPlanFile(ctx, a.Plan, "plan")
				if err != nil {
					return nil, err
				}
				return planBrief(p), nil
			}),
		NewTool("task_list", "List plans, newest first, with their progress and the tasks waiting for a person.",
			func(ctx context.Context, _ none) (any, error) { return c.Plans(ctx) }),
		NewTool("task_get", "Show a plan's tasks and their states; with task, that task in full: its log and result (last screen, changed files, summary).",
			func(ctx context.Context, a planArgs) (any, error) {
				p, err := c.Plan(ctx, a.ID)
				if err != nil || a.Task == "" {
					return planBrief(p), err
				}
				t := p.Task(a.Task)
				if t == nil {
					return nil, fmt.Errorf("plan %s has no task %q", p.ID, a.Task)
				}
				return t, nil
			}),
		NewTool("task_wait", "Wait for a plan (or one task) to finish; with until=attention also when a task needs a person (approve it with task_approve, or read a blocked agent with agent_read and answer with agent_send_keys). Returns the outcome (finished, attention, timeout) and the plan's report. A timeout means it still runs: wait again.",
			func(ctx context.Context, a planWaitArgs) (any, error) {
				res, err := c.WaitPlan(ctx, a.ID, hiveapi.PlanWaitOptions{Task: a.Task, Until: task.WaitUntil(a.Until), Timeout: seconds(a.TimeoutSeconds)})
				if err != nil {
					return nil, err
				}
				return map[string]any{"outcome": res.Outcome, "report": hiveapi.NewReport(res.Plan)}, nil
			}),
		NewTool("task_cancel", "Cancel a plan, or one of its tasks: running agents are stopped, and tasks that need a cancelled one are skipped.",
			func(ctx context.Context, a planArgs) (any, error) {
				p, err := c.CancelPlan(ctx, a.ID, a.Task)
				return planBrief(p), err
			}),
		NewTool("task_retry", "Run a plan's failed, cancelled and skipped tasks again (or one task, with what was skipped because of it), in the worktrees they had.",
			func(ctx context.Context, a planArgs) (any, error) {
				p, err := c.RetryPlan(ctx, a.ID, a.Task)
				return planBrief(p), err
			}),
		NewTool("task_approve", "Approve a task that waits for approval, so it starts; deny=true cancels it. Only approve when the user asked you to.",
			func(ctx context.Context, a approveArgs) (any, error) {
				p, err := c.ApproveTask(ctx, a.ID, a.Task, hiveapi.ApproveOptions{Deny: a.Deny, Note: a.Note, By: "mcp"})
				return planBrief(p), err
			}),
		NewTool("branch_list", "List the branches of agents' worktrees: their state (created, working, ready, merged), their changes, and the other branches they overlap or conflict with.",
			func(ctx context.Context, a branchListArgs) (any, error) { return c.Branches(ctx, a.Repo, a.All) }),
		NewTool("branch_get", "Show one branch: its base, changes, overlaps and conflicts, gates and review comments.",
			func(ctx context.Context, a branchArgs) (any, error) { return c.Branch(ctx, a.Ref) }),
		NewTool("branch_diff", "Read a branch's changes against its base, uncommitted ones included, as a unified diff (or only the files with stat).",
			func(ctx context.Context, a branchDiffArgs) (any, error) {
				return c.BranchDiff(ctx, hiveapi.DiffRequest{Ref: a.Ref, Paths: a.Paths, Stat: a.Stat, Limit: mcpPatchBytes})
			}),
		NewTool("branch_conflicts", "Show what merging a branch with its base (or another branch) would conflict on, with the conflict markers, without merging anything.",
			func(ctx context.Context, a branchConflictsArgs) (any, error) {
				return c.BranchConflicts(ctx, a.Ref, a.With)
			}),
		NewTool("branch_comment", "Add a review comment to a branch; with send, the comments not sent yet go to the agent working on it as one prompt.",
			func(ctx context.Context, a branchCommentArgs) (any, error) {
				return c.CommentBranch(ctx, hiveapi.CommentRequest{Ref: a.Ref, Body: a.Body, Path: a.Path, Line: a.Line, Send: a.Send, By: "mcp"})
			}),
		NewTool("branch_ready", "Run a branch's configured gates (tests, lint, build) in its worktree; it is ready when they all pass. Returns the branch with each gate's result.",
			func(ctx context.Context, a branchArgs) (any, error) { return c.ReadyBranch(ctx, a.Ref, nil) }),
		NewTool("workflow_list", "List the user's workflows: their inputs, triggers, steps and latest run.",
			func(ctx context.Context, _ none) (any, error) { return c.Workflows(ctx) }),
		NewTool("workflow_run", "Run one of the user's workflows by name, with its inputs. Returns the run with its ID; workflow_wait waits for it. Only run a workflow the user asked for.",
			func(ctx context.Context, a workflowRunArgs) (any, error) {
				r, err := c.RunWorkflow(ctx, a.Name, hiveapi.WorkflowRunOptions{Inputs: a.Inputs, Dir: a.Dir, By: "mcp"})
				if err != nil {
					return nil, err
				}
				return r.Summary(), nil
			}),
		NewTool("workflow_runs", "List workflow runs, newest first, with their states and the steps running.",
			func(ctx context.Context, a workflowRunsArgs) (any, error) {
				return c.WorkflowRuns(ctx, a.Workflow, cmp.Or(a.Limit, 20))
			}),
		NewTool("workflow_status", "Show a workflow run's report: its trigger, inputs, and each step's outcome, output, outputs and summary.",
			func(ctx context.Context, a workflowRunRef) (any, error) {
				r, err := c.WorkflowRun(ctx, a.ID)
				if err != nil {
					return nil, err
				}
				return hiveapi.NewWorkflowReport(r), nil
			}),
		NewTool("workflow_wait", "Wait for a workflow run to finish. Returns the outcome (finished or timeout) and its report. A timeout means it still runs: wait again.",
			func(ctx context.Context, a workflowWaitArgs) (any, error) {
				res, err := c.WaitWorkflowRun(ctx, a.ID, seconds(a.TimeoutSeconds))
				if err != nil {
					return nil, err
				}
				return map[string]any{"outcome": res.Outcome, "report": hiveapi.NewWorkflowReport(res.Run)}, nil
			}),
		NewTool("workflow_cancel", "Cancel a workflow run: its agents' tasks are cancelled and its commands stopped.",
			func(ctx context.Context, a workflowRunRef) (any, error) {
				r, err := c.CancelWorkflowRun(ctx, a.ID)
				if err != nil {
					return nil, err
				}
				return r.Summary(), nil
			}),
	}
	for i := range tools {
		switch tools[i].Name {
		case "agent_list", "agent_get", "agent_wait", "agent_read", "agent_explain", "pane_list", "pane_read", "env_list", "snapshot",
			"task_list", "task_get", "task_wait", "branch_list", "branch_get", "branch_diff", "branch_conflicts",
			"workflow_list", "workflow_runs", "workflow_status", "workflow_wait":
			tools[i].Annotations = ro
		case "agent_stop", "task_cancel", "workflow_cancel":
			tools[i].Annotations = &Annotations{Destructive: true}
		}
	}
	return tools
}

// planBrief is a plan without its tasks' logs and results, which task_get
// with a task shows.
func planBrief(p hiveapi.Plan) map[string]any {
	tasks := make([]task.TaskView, len(p.Tasks))
	for i := range p.Tasks {
		tasks[i] = p.Tasks[i].View(&p)
	}
	return map[string]any{"id": p.ID, "name": p.Name, "state": p.State, "rev": p.Rev, "reason": p.Reason, "tasks": tasks}
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
