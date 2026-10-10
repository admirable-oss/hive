package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/integration"
)

func newIntegrationCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Install hooks that let agents report their state to Hive",
		Long: `Hooks in an agent's own settings call ` + "`hive pane report-agent`" + ` as it starts a
turn, uses tools, asks for permission and stops, so Hive knows its state (and
its session ID) without reading its screen. Supported: claude (Claude Code's
settings.json) and codex (Codex's hooks.json).

The original file is backed up before the first change, installing twice
changes nothing, and uninstalling removes only Hive's hooks; when nothing
else changed, it puts the original file back byte for byte. The hooks do
nothing outside Hive and never fail the agent.`,
	}
	targets := func(args []string) ([]integration.Integration, error) {
		all := integration.All(a.home, a.getenv)
		if len(args) == 0 {
			return all, nil
		}
		var out []integration.Integration
		for _, id := range args {
			in, err := integration.Find(all, id)
			if err != nil {
				return nil, &usageError{err}
			}
			out = append(out, in)
		}
		return out, nil
	}
	ids := func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var out []cobra.Completion
		for _, in := range integration.All(a.home, a.getenv) {
			out = append(out, cobra.CompletionWithDesc(in.ID, in.Name))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	change := func(use, short, did string, apply func(integration.Integration) (bool, error)) *cobra.Command {
		return &cobra.Command{
			Use:               use + " <agent>...",
			Short:             short,
			Args:              minArgs(1),
			ValidArgsFunction: ids,
			RunE: func(_ *cobra.Command, args []string) error {
				list, err := targets(args)
				if err != nil {
					return err
				}
				type result struct {
					ID      string `json:"id"`
					Path    string `json:"path"`
					Changed bool   `json:"changed"`
				}
				var results []result
				for _, in := range list {
					changed, err := apply(in)
					if err != nil {
						return fmt.Errorf("%s: %w", in.Name, err)
					}
					results = append(results, result{ID: in.ID, Path: in.Path, Changed: changed})
					if a.json {
						continue
					}
					if changed {
						fmt.Fprintf(a.out, "%s: %s (%s)\n", in.Name, did, in.Path)
					} else {
						fmt.Fprintf(a.out, "%s: nothing to change (%s)\n", in.Name, in.Path)
					}
				}
				if a.json {
					return a.printJSON(results)
				}
				return nil
			},
		}
	}
	cmd.AddCommand(
		change("install", "Add Hive's hooks to an agent's settings", "hooks installed", integration.Integration.Install),
		change("uninstall", "Remove Hive's hooks from an agent's settings", "hooks removed", integration.Integration.Uninstall),
		&cobra.Command{
			Use:               "status [agent]...",
			Short:             "Show whether the hooks are installed",
			ValidArgsFunction: ids,
			RunE: func(_ *cobra.Command, args []string) error {
				list, err := targets(args)
				if err != nil {
					return err
				}
				var out []integration.Status
				for _, in := range list {
					st, err := in.Status()
					if err != nil {
						return fmt.Errorf("%s: %w", in.Name, err)
					}
					out = append(out, st)
				}
				return a.emit(out, func() error {
					w := a.table()
					fmt.Fprintln(w, "AGENT\tSTATE\tHOOKS\tFILE")
					for _, st := range out {
						fmt.Fprintf(w, "%s\t%s\t%d/%d\t%s\n", st.ID, st.State, st.Hooks, st.Want, st.Path)
					}
					return w.Flush()
				})
			},
		},
	)
	return cmd
}
