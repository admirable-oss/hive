package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/tui"
)

func newUICmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "ui",
		Aliases: []string{"tui"},
		Short:   "Open the interactive dashboard (the default command)",
		Args:    noArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return runTUI(cmd.Context(), a) },
	})
}

func runTUI(_ context.Context, a *app) error {
	return tui.Run(a.client)
}
