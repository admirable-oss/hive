package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newEnvironmentCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environment",
		Aliases: []string{"env"},
		Short:   "Manage environments (persistent workspaces agents run in)",
	}
	const timeout = 15 * time.Second
	cmd.AddCommand(
		needsDaemon(&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List environments",
			Args:    noArgs,
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				envs, err := a.client.EnvironmentList(ctx)
				if err != nil {
					return err
				}
				return a.emit(envs, func() error {
					w := a.table()
					fmt.Fprintln(w, "ID\tSTATUS\tCREATED\tPATH")
					for _, env := range envs {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", env.ID, env.Status, formatTime(env.CreatedAt), env.Path)
					}
					return w.Flush()
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:   "create <id>",
			Short: "Create an environment",
			Args:  exactArgs(1),
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				env, err := a.client.EnvironmentCreate(ctx, args[0])
				if err != nil {
					return err
				}
				return a.emit(env, func() error {
					fmt.Fprintf(a.out, "created environment %q at %s\n", env.ID, env.Path)
					return nil
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "get <id>",
			Short:             "Show an environment",
			Args:              exactArgs(1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				env, err := a.client.EnvironmentGet(ctx, args[0])
				if err != nil {
					return err
				}
				return a.emit(env, func() error {
					w := a.table()
					fmt.Fprintf(w, "ID\t%s\n", env.ID)
					fmt.Fprintf(w, "Name\t%s\n", env.Name)
					fmt.Fprintf(w, "Status\t%s\n", env.Status)
					fmt.Fprintf(w, "Path\t%s\n", env.Path)
					fmt.Fprintf(w, "Created\t%s\n", formatTime(env.CreatedAt))
					return w.Flush()
				})
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "remove <id>",
			Aliases:           []string{"rm"},
			Short:             "Stop an environment's agents and delete its workspace",
			Args:              exactArgs(1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(timeout+a.cfg.Daemon.ShutdownTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				if err := a.client.EnvironmentRemove(ctx, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "removed environment %q\n", args[0])
				return nil
			}),
		}),
	)
	return cmd
}
