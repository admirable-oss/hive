package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newEventsCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "events [type-prefix...]",
		Short: "Print daemon events as JSON lines until interrupted",
		Long: `Print what happens in the daemon as it happens, one JSON object per line:
environment.created, environment.removed, process.started, process.exited,
process.recovered. Give type prefixes to filter, e.g. ` + "`hive events process.`" + `.

An events_lost event means this reader fell behind and missed some; re-read
the state you track (hive ps list) when you see one.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			stream, err := a.client.Events(cmd.Context(), args...)
			if err != nil {
				return err
			}
			defer stream.Close()
			go func() { <-cmd.Context().Done(); _ = stream.Close() }()
			enc := json.NewEncoder(a.out)
			for {
				ev, err := stream.Next()
				if err != nil {
					if errors.Is(err, io.EOF) || cmd.Context().Err() != nil {
						return nil
					}
					return fmt.Errorf("events: %w", err)
				}
				if err := enc.Encode(ev); err != nil {
					return err
				}
			}
		},
	})
}
