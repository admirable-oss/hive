package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

// Shell completion queries the daemon briefly and never starts it: a Tab
// press must stay instant.
const completeTimeout = 300 * time.Millisecond

func completeEnvironments(a *app) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		envs, err := a.client.EnvironmentList(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := make([]cobra.Completion, 0, len(envs))
		for _, e := range envs {
			out = append(out, cobra.CompletionWithDesc(e.ID, e.Path))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProcesses offers process IDs, only running ones when activeOnly.
func completeProcesses(a *app, activeOnly bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), completeTimeout)
		defer cancel()
		procs, err := a.client.ProcessList(ctx, "")
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, p := range procs {
			if activeOnly && !p.Active() {
				continue
			}
			out = append(out, cobra.CompletionWithDesc(p.ID, p.EnvironmentID+" · "+summary(p)+" · "+string(p.Status)))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
