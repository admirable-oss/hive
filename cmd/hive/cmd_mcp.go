package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/mcp"
	"github.com/admirable-oss/hive/pkg/hiveapi"
)

func newMCPCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "mcp",
		Short: "Serve Hive's agent, pane and environment tools over MCP (stdio)",
		Long: `Run a Model Context Protocol server on standard input and output, so an
agent can drive Hive: start other agents (in their own worktrees), prompt them,
wait for them and read their results. Add it to Claude Code with:

  claude mcp add hive -- hive mcp

Inside a Hive pane it serves that pane's session; elsewhere the default one
(or --session), starting the daemon when needed.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := &mcp.Server{
				Name:         "hive",
				Version:      buildinfo.Get().Version,
				Instructions: mcp.Instructions,
				Tools:        mcp.HiveTools(hiveapi.New(a.client)),
			}
			return srv.Serve(cmd.Context(), os.Stdin, a.out)
		},
	})
}
