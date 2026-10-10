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
		newAgentManifestsCmd(a),
	)
	return cmd
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
				for _, w := range res.Warnings {
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
