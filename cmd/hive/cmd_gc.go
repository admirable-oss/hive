package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/persist"
)

func newGCCmd(a *app) *cobra.Command {
	var req persist.GCRequest
	cmd := needsDaemon(&cobra.Command{
		Use:   "gc",
		Short: "Remove what Hive no longer needs: old process records and logs, leftovers",
		Long: `Remove the records and output logs of processes that ended longer ago than
retention.exited_processes (7 days by default) and that no pane shows,
leftover shim directories, kept pane history of closed panes, and unreadable
files moved aside long ago. The daemon does this by itself every
retention.gc_interval. Running agents and anything a pane shows are never
touched.`,
		Example: `  hive gc --dry-run
  hive gc --older-than 24h`,
		Args: noArgs,
		RunE: withTimeout(5*time.Minute, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			report, err := client.NewSessions(a.client).GC(ctx, req)
			if err != nil {
				return err
			}
			return a.emit(report, func() error {
				verb := "removed"
				if report.DryRun {
					verb = "would remove"
				}
				for _, id := range report.Processes {
					fmt.Fprintf(a.out, "%s process %s\n", verb, id)
				}
				for _, f := range report.Files {
					fmt.Fprintf(a.out, "%s %s\n", verb, f)
				}
				_, err := fmt.Fprintf(a.out, "%s %d process(es) and %d other file(s), %s\n", verb, len(report.Processes), len(report.Files), formatBytes(report.Bytes))
				return err
			})
		}),
	})
	cmd.Flags().BoolVarP(&req.DryRun, "dry-run", "n", false, "only list what would be removed")
	cmd.Flags().DurationVar(&req.OlderThan, "older-than", 0, "remove processes that ended longer ago than this (default retention.exited_processes)")
	return cmd
}

// formatBytes writes n in binary units.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
