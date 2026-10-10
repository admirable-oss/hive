package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/client"
)

// agentTimeout bounds agent commands.
const agentTimeout = 15 * time.Second

func newAgentCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "See what your coding agents are doing",
		Long: `Hive recognises coding agents (Claude Code, Codex, OpenCode, Gemini, …) in
its terminals and reads their state from their screens: working, blocked
(waiting for your decision), done (finished, not looked at yet), idle or
exited. Commands that take an optional [agent] accept a pane or process ID and
default to $HIVE_PANE_ID.`,
	}
	cmd.AddCommand(
		newAgentListCmd(a),
		agentCmd(a, &cobra.Command{Use: "get [agent]", Short: "Show an agent"}, func(ctx context.Context, ag client.Agents, id string) error {
			x, err := ag.Get(ctx, id)
			if err != nil {
				return err
			}
			return a.emit(x, func() error { return printAgent(a, x) })
		}),
		agentCmd(a, &cobra.Command{
			Use:   "explain [agent]",
			Short: "Show why an agent is in its state: the rule that matched, reports, its screen",
		}, func(ctx context.Context, ag client.Agents, id string) error {
			ex, err := ag.Explain(ctx, id)
			if err != nil {
				return err
			}
			return a.emit(ex, func() error { return printExplanation(a, ex) })
		}),
		newAgentStartCmd(a),
		newAgentPromptCmd(a),
		newAgentWaitCmd(a),
		newAgentReadCmd(a),
		newAgentSendKeysCmd(a),
		newAgentRenameCmd(a),
		agentCmd(a, &cobra.Command{Use: "focus [agent]", Short: "Show an agent's pane in every client"}, func(ctx context.Context, ag client.Agents, id string) error {
			x, err := ag.Focus(ctx, id)
			if err != nil {
				return err
			}
			return a.emit(x, func() error { _, err := fmt.Fprintf(a.out, "focused %s\n", x.PaneID); return err })
		}),
		agentCmd(a, &cobra.Command{Use: "stop [agent]", Short: "Stop an agent's process (its pane stays)"}, func(ctx context.Context, ag client.Agents, id string) error {
			x, err := ag.Stop(ctx, id)
			if err != nil {
				return err
			}
			return a.emit(x, func() error { _, err := fmt.Fprintf(a.out, "stopped %s\n", x.ID); return err })
		}),
		needsDaemon(&cobra.Command{
			Use:               "attach [agent]",
			Short:             "Attach to an agent's terminal (Ctrl+] detaches)",
			Args:              rangeArgs(0, 1),
			ValidArgsFunction: completePanes(a),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := a.paneArg(args)
				if err != nil {
					return err
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), agentTimeout)
				x, err := client.NewAgents(a.client).Get(ctx, id)
				cancel()
				if err != nil {
					return err
				}
				return termAttach(cmd.Context(), a, x.ID)
			},
		}),
		newAgentManifestsCmd(a),
	)
	return cmd
}

func newAgentStartCmd(a *app) *cobra.Command {
	var (
		req  client.AgentStartRequest
		vars []string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "start --kind <kind> --env <env> [--worktree <branch>] [-- args...]",
		Short: "Start an agent in a new tab (prints its pane ID)",
		Long: `Start an agent by its manifest (` + "`hive agent manifests`" + ` lists them) in a new tab of an
environment. With --worktree, it starts on that branch in a new git worktree
of the environment's repository, with its own environment, so agents working
in parallel never edit the same checkout.`,
		Example: `  hive agent start --kind claude --env api
  hive agent start --kind codex --env api --worktree fix/login -- --model o4-mini`,
		Args: argsBeforeDash(0, 0),
		RunE: withTimeout(time.Minute, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			_, req.Args = splitAtDash(cmd, args)
			if req.EnvironmentID == "" {
				req.EnvironmentID = a.getenv("HIVE_ENV_ID")
			}
			if req.Kind == "" || req.EnvironmentID == "" {
				return &usageError{fmt.Errorf("--kind and --env are required (--env defaults to $HIVE_ENV_ID)")}
			}
			env, err := parseVars(vars)
			if err != nil {
				return err
			}
			req.Env = env
			res, err := client.NewAgents(a.client).Start(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(res, func() error {
				_, err := fmt.Fprintln(a.out, res.Pane.ID)
				return err
			})
		}),
	})
	cmd.Flags().StringVarP(&req.Kind, "kind", "k", "", "the agent: claude, codex, … (hive agent manifests)")
	cmd.Flags().StringVar(&req.EnvironmentID, "env", "", "the environment (default $HIVE_ENV_ID)")
	cmd.Flags().StringVarP(&req.Worktree, "worktree", "w", "", "start on this branch in a new worktree")
	cmd.Flags().StringVar(&req.Base, "base", "", "where a new worktree branch starts (default HEAD)")
	cmd.Flags().StringVar(&req.Name, "name", "", "a name for its tab and pane (default the kind)")
	cmd.Flags().StringArrayVarP(&vars, "var", "e", nil, "a variable for the agent, as KEY=VALUE (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("env", completeEnvironments(a))
	return cmd
}

// agentOutcome turns an outcome into the exit status: 0 when it got what
// it waited for, 4 on a timeout, 5 when the agent blocked, stalled or
// exited.
func agentOutcome(o agent.Outcome) error {
	switch o {
	case agent.OutcomeReached, agent.OutcomeStarted:
		return nil
	case agent.OutcomeTimeout:
		return &codedError{code: exitTimeout, msg: "timed out"}
	}
	return &codedError{code: exitAgent, msg: "the agent " + string(o)}
}

func printOutcome(a *app, o agent.Outcome, x agent.Agent, output []string) error {
	for _, l := range output {
		fmt.Fprintln(a.out, l)
	}
	if o != agent.OutcomeReached && o != agent.OutcomeStarted {
		return nil // the exit status says it
	}
	_, err := fmt.Fprintf(a.errOut, "%s is %s\n", cmpStr(x.PaneID, x.ID), x.State)
	return err
}

func newAgentPromptCmd(a *app) *cobra.Command {
	var (
		o     client.PromptOptions
		until string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "prompt [agent] <text | ->",
		Short: "Send an agent a prompt, and optionally wait for its answer",
		Long: `Paste a prompt into an agent and send it. The prompt arrives whole (as a
bracketed paste where the agent supports it) and is never interleaved with
another prompt to the same agent. A blocked agent is refused: answer its
question first. "-" reads the prompt from standard input.

The command returns once the agent reacts. With --wait it returns when the
agent finishes (--until done) or is ready again (--until idle), and --read N
prints its last N lines. Exit status: 0 done, 4 timed out, 5 the agent blocked
on a decision, stalled (did not react, or stopped changing its screen while
working) or exited.`,
		Example: `  hive agent prompt "$P" "Fix the failing test in auth_test.go" --wait --read 40
  git diff | hive agent prompt "$P" - --wait --timeout 20m`,
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completePanes(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.paneArg(args[:len(args)-1])
			if err != nil {
				return err
			}
			text := args[len(args)-1]
			if text == "-" {
				data, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
				if err != nil {
					return err
				}
				text = strings.TrimRight(string(data), "\n")
			}
			o.Until = agent.Until(until)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			res, err := client.NewAgents(a.client).Prompt(ctx, id, text, o)
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error { return printOutcome(a, res.Outcome, res.Agent, res.Output) }); err != nil {
				return err
			}
			return agentOutcome(res.Outcome)
		},
	})
	cmd.Flags().BoolVar(&o.Wait, "wait", false, "wait for the agent to finish")
	cmd.Flags().StringVar(&until, "until", "done", "with --wait: done (finished the turn) or idle (ready for a prompt)")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 0, "give up after this long (default never)")
	cmd.Flags().DurationVar(&o.StartTimeout, "start-timeout", 0, "how long the agent gets to react (default 20s)")
	cmd.Flags().DurationVar(&o.StallTimeout, "stall-timeout", 0, "how long it may work without changing its screen (default 10m; negative: forever)")
	cmd.Flags().IntVar(&o.Read, "read", 0, "print the agent's last n lines when done")
	return cmd
}

func newAgentWaitCmd(a *app) *cobra.Command {
	var (
		o     client.WaitOptions
		until string
		after int64
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "wait [agent]",
		Short: "Wait until an agent is idle, done, blocked, working or exited",
		Long: `Wait for an agent's state. --until idle waits until it is ready for a prompt;
done until it finishes a turn (one finished and not looked at yet counts,
unless --after gives the completion count you already saw); change for any
change. The wait also ends when the agent blocks on a decision or exits.
Exit status: 0 reached, 4 timed out, 5 blocked, stalled or exited.`,
		Args:              rangeArgs(0, 1),
		ValidArgsFunction: completePanes(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.paneArg(args)
			if err != nil {
				return err
			}
			o.Until = agent.Until(until)
			if cmd.Flags().Changed("after") {
				n := uint64(max(after, 0))
				o.After = &n
			}
			res, err := client.NewAgents(a.client).Wait(cmd.Context(), id, o)
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error { return printOutcome(a, res.Outcome, res.Agent, nil) }); err != nil {
				return err
			}
			return agentOutcome(res.Outcome)
		},
	})
	cmd.Flags().StringVar(&until, "until", "idle", "idle, done, working, blocked, exited or change")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 0, "give up after this long (default never)")
	cmd.Flags().DurationVar(&o.Stall, "stall", 0, "end when the agent works this long without changing its screen")
	cmd.Flags().Int64Var(&after, "after", 0, "with --until done: a completion count already seen (completion_seq)")
	return cmd
}

func newAgentReadCmd(a *app) *cobra.Command {
	var req client.ReadRequest
	cmd := agentCmd(a, &cobra.Command{
		Use:   "read [agent]",
		Short: "Print what an agent shows",
		Long: `Print what an agent shows. --source picks what:
  visible           the screen (default)
  recent            the screen and the lines that scrolled off above it
  recent-unwrapped  the same, with lines the terminal wrapped joined again
  history           only the lines that scrolled off`,
	}, func(ctx context.Context, ag client.Agents, id string) error {
		lines, err := ag.Read(ctx, id, req)
		if err != nil {
			return err
		}
		return a.emit(lines, func() error {
			for _, l := range lines {
				fmt.Fprintln(a.out, l)
			}
			return nil
		})
	})
	cmd.Flags().StringVarP(&req.Source, "source", "s", "visible", "visible, recent, recent-unwrapped or history")
	cmd.Flags().IntVarP(&req.Lines, "lines", "n", 0, "only the last n lines")
	cmd.Flags().BoolVar(&req.ANSI, "ansi", false, "keep colours as escape sequences")
	return cmd
}

func newAgentSendKeysCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "send-keys <agent> <key>...",
		Short: "Press keys in an agent: Enter, Escape, Up, C-c, y, …",
		Long: `Press named keys in an agent, for example to answer a permission dialog:
` + "`hive agent send-keys \"$P\" Down Enter`" + `. Keys are named as for ` + "`hive pane send-keys`" + `.`,
		Args:              minArgs(2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			return client.NewAgents(a.client).SendKeys(ctx, args[0], args[1:])
		}),
	})
}

func newAgentRenameCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:               "rename [agent] <name>",
		Short:             "Rename an agent's pane",
		Args:              rangeArgs(1, 2),
		ValidArgsFunction: completePanes(a),
		RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.paneArg(args[:len(args)-1])
			if err != nil {
				return err
			}
			x, err := client.NewAgents(a.client).Rename(ctx, id, args[len(args)-1])
			if err != nil {
				return err
			}
			return a.emit(x, func() error { return printAgent(a, x) })
		}),
	})
}

// agentCmd completes cmd as a command on an optional [agent] argument
// (default $HIVE_PANE_ID).
func agentCmd(a *app, cmd *cobra.Command, run func(ctx context.Context, ag client.Agents, id string) error) *cobra.Command {
	cmd.Args = rangeArgs(0, 1)
	cmd.ValidArgsFunction = completePanes(a)
	cmd.RunE = withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
		id, err := a.paneArg(args)
		if err != nil {
			return err
		}
		return run(ctx, client.NewAgents(a.client), id)
	})
	return needsDaemon(cmd)
}

func newAgentListCmd(a *app) *cobra.Command {
	var all bool
	cmd := needsDaemon(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List agents and their states",
		Args:    noArgs,
		RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			res, err := client.NewAgents(a.client).List(ctx, all)
			if err != nil {
				return err
			}
			return a.emit(res, func() error {
				w := a.table()
				fmt.Fprintln(w, "PANE\tPROCESS\tENV\tKIND\tSTATE\tSINCE\tWHY")
				for _, x := range res.Agents {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", cmpStr(x.PaneID, "-"), x.ID, x.EnvironmentID,
						cmpStr(x.Kind, "-"), x.State, ago(x.Since), cmpStr(x.Reason, "-"))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include terminals no manifest recognises")
	return cmd
}

func newAgentManifestsCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "manifests",
		Short: "List the agent manifests the daemon uses",
		Long: `Manifests teach Hive to recognise an agent and read its state. Built-in ones
can be replaced or added to by TOML files in ` + "`hive config path`" + `'s directory,
under agent-detection/; run ` + "`hive server reload-agent-manifests`" + ` after editing.`,
		Args: noArgs,
		RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			ms, err := client.NewAgents(a.client).Manifests(ctx)
			if err != nil {
				return err
			}
			return a.emit(ms, func() error {
				w := a.table()
				fmt.Fprintln(w, "ID\tNAME\tPROCESS\tRULES\tSOURCE")
				for _, m := range ms {
					src := m.Source
					if m.Synthetic {
						src += " (synthetic)"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", m.ID, m.Name, strings.Join(m.Detect.Process, ","), len(m.Rules), src)
				}
				return w.Flush()
			})
		}),
	})
}

func newReloadManifestsCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "reload-agent-manifests",
		Short: "Read the agent manifests again and re-detect every agent",
		Args:  noArgs,
		RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			res, err := client.NewAgents(a.client).Reload(ctx)
			if err != nil {
				return err
			}
			return a.emit(res, func() error {
				for _, w := range res.Warnings { // stderr: warnings are not output
					fmt.Fprintf(a.errOut, "hive: warning: %s\n", w)
				}
				fmt.Fprintf(a.out, "loaded %d agent manifests: %s\n", len(res.Manifests), strings.Join(res.Manifests, ", "))
				return nil
			})
		}),
	})
}

func newPaneReportAgentCmd(a *app) *cobra.Command {
	var (
		state, sessionID, message string
		ttl                       time.Duration
		hook                      bool
	)
	cmd := &cobra.Command{
		Use:   "report-agent [pane]",
		Short: "Report the state of the agent in a pane (for agents' hooks)",
		Long: `Tell Hive what the agent in a pane is doing. A report beats what Hive reads
from the screen until its TTL passes or another report replaces it.

With --hook, the command is safe to call from an agent's hooks: it reads the
hook's JSON from standard input (for its session_id), does nothing outside a
Hive pane, never starts the daemon, and always exits 0.
` + "`hive integration install`" + ` sets these hooks up.`,
		Example: `  hive pane report-agent --state blocked --message "needs a decision"
  echo '{"session_id":"abc"}' | hive pane report-agent --hook --state idle`,
		Args:              rangeArgs(0, 1),
		ValidArgsFunction: completePanes(a),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			r := client.AgentReport{State: agent.State(state), TTL: ttl, SessionID: sessionID, Message: message}
			if !hook {
				id, err := a.paneArg(args)
				if err != nil {
					return err
				}
				if err := a.ensureDaemon(ctx); err != nil {
					return err
				}
				x, err := client.NewAgents(a.client).Report(ctx, id, r)
				if err != nil {
					return err
				}
				return a.emit(x, func() error { return printAgent(a, x) })
			}
			if r.SessionID == "" {
				r.SessionID = hookSessionID(os.Stdin)
			}
			id, err := a.paneArg(args)
			if err != nil {
				return nil // not in a Hive pane
			}
			if _, err := client.NewAgents(a.client).Report(ctx, id, r); err != nil && a.getenv("HIVE_DEBUG_HOOKS") != "" {
				fmt.Fprintf(a.errOut, "hive: report-agent: %v\n", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "working, blocked, idle or done (required)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "how long the report holds (default 10m)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "the agent's own session ID, for resuming it")
	cmd.Flags().StringVar(&message, "message", "", "a few words on why")
	cmd.Flags().BoolVar(&hook, "hook", false, "called from an agent hook: read its JSON on stdin, never fail")
	_ = cmd.MarkFlagRequired("state")
	return cmd
}

// hookSessionID reads an agent hook's JSON input and returns its session
// ID, "" when there is none. It never blocks on a terminal.
func hookSessionID(in *os.File) string {
	if fi, err := in.Stat(); err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return ""
	}
	var v map[string]any
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	for _, k := range []string{"session_id", "sessionId", "thread_id", "thread-id"} {
		if s, ok := v[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func printAgent(a *app, x agent.Agent) error {
	w := a.table()
	row := func(k, v string) { fmt.Fprintf(w, "%s\t%s\n", k, v) }
	row("process", x.ID)
	row("pane", cmpStr(x.PaneID, "-"))
	row("environment", x.EnvironmentID)
	row("kind", cmpStr(x.Kind, "- (not a recognised agent)"))
	row("state", fmt.Sprintf("%s since %s (%s)", x.State, ago(x.Since), cmpStr(string(x.Source), "-")))
	if x.Reason != "" {
		row("why", x.Reason)
	}
	if x.SessionID != "" {
		row("session id", x.SessionID)
	}
	return w.Flush()
}

func printExplanation(a *app, ex agent.Explanation) error {
	if err := printAgent(a, ex.Agent); err != nil {
		return err
	}
	w := a.table()
	if ex.Manifest != "" {
		fmt.Fprintf(w, "manifest\t%s (%s, detected by %s)\n", ex.Manifest, ex.Source, ex.Detected)
	} else {
		fmt.Fprintln(w, "manifest\tnone: screen activity decides")
	}
	if ex.Verdict.Rule >= 0 {
		fmt.Fprintf(w, "rule\t#%d %s → %s, %s matched %q\n", ex.Verdict.Rule, cmpStr(ex.Verdict.Description, "-"), ex.Verdict.State, ex.Verdict.Region, ex.Verdict.Text)
	} else {
		fmt.Fprintln(w, "rule\tnone matched")
	}
	if r := ex.Report; r != nil {
		fmt.Fprintf(w, "report\t%s until %s %s\n", r.State, r.Until.Local().Format(time.TimeOnly), r.Message)
	}
	fmt.Fprintf(w, "quiet for\t%s\n", ex.Quiet.Round(100*time.Millisecond))
	fmt.Fprintf(w, "worked since seen\t%t\n", ex.Worked)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "screen:")
	for y, l := range ex.Screen {
		fmt.Fprintf(a.out, "%3d│%s\n", y, l)
	}
	return nil
}

// ago is how long ago t was, roughly.
func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Local().Format(time.DateOnly)
}
