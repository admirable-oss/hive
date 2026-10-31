package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/runtime"
)

func newPresenceCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "presence",
		Short: "Show who is connected, where they are looking, and who is typing where",
		Long: `List everyone connected to this runtime, you and members (hive access),
with the program they use and the environment, tab and pane they look at, and
which panes someone is typing into. A pane takes input from one person at a
time: while someone types, others' input is refused until they stop for
[access] input_idle, or ` + "`hive pane release`" + `.`,
		Args: noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var res runtime.PresenceList
			if err := a.client.Call(ctx, "presence.list", nil, &res); err != nil {
				return err
			}
			return a.emit(res, func() error {
				w := a.table()
				fmt.Fprintln(w, "WHO\tCLIENT\tENVIRONMENT\tTAB\tPANE\tSINCE")
				for _, e := range res.Presence {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Person.Name, dash(e.Client), dash(e.Environment), dash(e.Tab), dash(e.Pane), e.Since.Local().Format(time.TimeOnly))
				}
				if err := w.Flush(); err != nil {
					return err
				}
				if len(res.Holds) == 0 {
					return nil
				}
				fmt.Fprintln(a.out)
				w = a.table()
				fmt.Fprintln(w, "TYPING\tPANE\tENVIRONMENT\tSINCE")
				for _, h := range res.Holds {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", h.Holder.Name, dash(h.Pane), dash(h.Environment), h.Since.Local().Format(time.TimeOnly))
				}
				return w.Flush()
			})
		}),
	})
}

func newPaneClaimCmd(a *app) *cobra.Command {
	var force bool
	cmd := paneCmd(a, &cobra.Command{
		Use:   "claim [pane]",
		Short: "Take a pane's input: until you release it or stop typing, nobody else types there",
		Long: `Take a pane's input on purpose, as typing into it would. With --force, take it
from whoever holds it (yours to do on your own runtime; a member needs the
owner role in its environment), and they are told.`,
	}, func(ctx context.Context, _ client.Workspace, id string, _ []string) error {
		var res runtime.PaneClaimed
		if err := a.client.Call(ctx, "pane.claim", map[string]any{"pane": id, "force": force}, &res); err != nil {
			return err
		}
		if res.Previous != nil {
			return a.done(map[string]any{"pane": id, "holder": res.Holder, "previous": res.Previous}, "took pane %s from %s", id, res.Previous.Name)
		}
		return a.done(map[string]any{"pane": id, "holder": res.Holder}, "pane %s takes input from %s", id, res.Holder.Name)
	})
	cmd.Flags().BoolVar(&force, "force", false, "take it from whoever holds it")
	return cmd
}

func newPaneReleaseCmd(a *app) *cobra.Command {
	return paneCmd(a, &cobra.Command{
		Use:   "release [pane]",
		Short: "Let go of a pane's input, so someone else may type",
	}, func(ctx context.Context, _ client.Workspace, id string, _ []string) error {
		var res map[string]bool
		if err := a.client.Call(ctx, "pane.release", map[string]string{"pane": id}, &res); err != nil {
			return err
		}
		if !res["released"] {
			return a.done(map[string]any{"pane": id, "released": false}, "you do not hold pane %s", id)
		}
		return a.done(map[string]any{"pane": id, "released": true}, "released pane %s", id)
	})
}
