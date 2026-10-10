package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
)

func newAPICmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Describe the daemon's API, or read the whole workspace at once",
		Long: `Every command talks to the daemon through a JSON API over its socket
($HIVE_SOCKET_PATH inside a pane). These print it as JSON, for tools and
agents: ` + "`schema`" + ` lists every method with JSON Schemas of its params and result;
` + "`snapshot`" + ` is every environment, tab, pane, process and agent in one reply.
Every other command prints its result as JSON with --json.`,
	}
	cmd.AddCommand(
		needsDaemon(&cobra.Command{
			Use:   "schema",
			Short: "Print the daemon's methods and their JSON Schemas",
			Args:  noArgs,
			RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				s, err := client.NewAgents(a.client).Schema(ctx)
				if err != nil {
					return err
				}
				return a.printJSON(s)
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:   "snapshot",
			Short: "Print the whole workspace as JSON",
			Args:  noArgs,
			RunE: withTimeout(agentTimeout, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				s, err := client.NewAgents(a.client).Snapshot(ctx)
				if err != nil {
					return err
				}
				return a.printJSON(s)
			}),
		}),
	)
	return cmd
}
