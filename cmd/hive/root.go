package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/skills"
)

// Command annotations read by the root's pre-run hook.
const (
	// annDaemon marks commands that talk to the daemon; it is started on
	// demand when autostart is enabled.
	annDaemon = "hive/daemon"
)

func newRootCmd(a *app) *cobra.Command {
	var skill bool
	root := &cobra.Command{
		Use:   "hive",
		Short: "Run coding agents that keep working when you close the terminal",
		Long: `Hive runs your coding agents (Claude Code, Codex, OpenCode, anything)
under a background daemon, so they survive closed terminals and dropped SSH
sessions. Run hive with no arguments to open the multiplexer.

Data lives in ~/.hive (set HIVE_HOME to change it). Configuration is read from
~/.config/hive/config.toml (see ` + "`hive config`" + `).`,
		Args:          noArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Annotations:   map[string]string{annDaemon: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if skill {
				_, err := fmt.Fprint(a.out, skills.Hive)
				return err
			}
			return runTUI(cmd.Context(), a)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if skill {
				return nil // printing the skill needs nothing else
			}
			if cmd.Name() != "daemon" { // the daemon logs its own warnings
				a.reportConfig()
			}
			if cmd.Annotations[annDaemon] == "true" {
				ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
				defer cancel()
				return a.ensureDaemon(ctx)
			}
			return nil
		},
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print machine-readable JSON")
	root.Flags().BoolVar(&skill, "skill", false, "print the skill that teaches agents to drive other agents through Hive (SKILL.md)")
	// Read in execute before parsing; declared so cobra accepts and
	// documents it.
	root.PersistentFlags().String("session", "", "the session to use (default $HIVE_SESSION, else \"default\")")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
	root.CompletionOptions.HiddenDefaultCmd = false

	root.AddGroup(
		&cobra.Group{ID: "agents", Title: "Agents:"},
		&cobra.Group{ID: "workspace", Title: "Workspace:"},
		&cobra.Group{ID: "runtime", Title: "Runtime:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("agents",
		newUICmd(a),
		newAgentCmd(a),
		newIntegrationCmd(a),
		newMCPCmd(a),
		newProcessCmd(a),
		newTerminalCmd(a),
		newEventsCmd(a),
		newDemoCmd(a),
	)
	add("workspace",
		newEnvironmentCmd(a),
		newTabCmd(a),
		newPaneCmd(a),
		newLayoutCmd(a),
		newWorktreeCmd(a),
	)
	add("runtime",
		newSessionCmd(a),
		newDaemonCmd(a),
		newStatusCmd(a),
		newPingCmd(a),
		newStopCmd(a),
		newConfigCmd(a),
		newAPICmd(a),
		newVersionCmd(a),
	)
	root.SetHelpCommandGroupID("runtime")
	root.SetCompletionCommandGroupID("runtime")
	return root
}

// needsDaemon marks cmd (and so its subcommands' parent check) as talking
// to the daemon.
func needsDaemon(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[annDaemon] = "true"
	return cmd
}

// withTimeout bounds a command's run; agents themselves are never affected.
func withTimeout(d time.Duration, run func(ctx context.Context, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), d)
		defer cancel()
		return run(ctx, cmd, args)
	}
}

// exactArgs is cobra.ExactArgs with usage errors that map to exit code 2.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}
}

func rangeArgs(lo, hi int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.RangeArgs(lo, hi)(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}
}

func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MinimumNArgs(n)(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}
}

// noArgs is cobra.NoArgs with usage errors that map to exit code 2 (it also
// reports unknown subcommands).
func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &usageError{err}
	}
	return nil
}
