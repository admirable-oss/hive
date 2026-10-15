package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
)

// The commands here are for plugins: they act on the plugin running them
// ($HIVE_PLUGIN_ID; on its own socket the daemon knows anyway).

func (a *app) callingPlugin(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if id := a.getenv("HIVE_PLUGIN_ID"); id != "" {
		return id, nil
	}
	return "", &usageError{fmt.Errorf("not run by a plugin: give --plugin")}
}

func newPluginCardCmd(a *app) *cobra.Command {
	var (
		u       plugin.CardUpdate
		rows    []string
		links   []string
		clearIt bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "card <card>",
		Short: "Show rows on a sidebar card the plugin declares (for plugins)",
		Long: `card sets what one of the plugin's [[contributes.cards]] shows, for one
environment (--env) or one agent's pane (--pane), as the card's scope says.
Rows are label=text, optionally prefixed with a tone (info, success, warning,
error) and a colon. The card disappears after --ttl unless set again, or at
once with --clear.`,
		Example: `  hive plugin card ci --env "$ENV" --row "success:main=passing" --row "warning:PR #12=2 failing" \
      --link "Pipeline=https://ci.example.com/run/88"
  hive plugin card ci --env "$ENV" --clear`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.callingPlugin(u.Plugin)
			if err != nil {
				return err
			}
			u.Plugin, u.Card = id, args[0]
			if u.Environment == "" && u.Pane == "" {
				u.Environment = a.getenv("HIVE_ENV_ID")
			}
			if !clearIt {
				for _, r := range rows {
					row, err := parseCardRow(r)
					if err != nil {
						return &usageError{err}
					}
					u.Rows = append(u.Rows, row)
				}
				for _, l := range links {
					title, url, ok := strings.Cut(l, "=")
					if !ok {
						return &usageError{fmt.Errorf("--link %q: want title=url", l)}
					}
					u.Links = append(u.Links, plugin.Link{Title: title, URL: url})
				}
			}
			c, err := client.NewPlugins(a.client).SetCard(ctx, u)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"card": c}, "card %s: %d row(s)", c.Card, len(c.Rows))
		}),
	})
	cmd.Flags().StringVar(&u.Plugin, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	cmd.Flags().StringVar(&u.Environment, "env", "", "the environment it is about (default $HIVE_ENV_ID)")
	cmd.Flags().StringVar(&u.Pane, "pane", "", "the agent's pane it is about (agent cards)")
	cmd.Flags().StringArrayVar(&rows, "row", nil, "a row: [tone:]label=text (repeatable, at most 6)")
	cmd.Flags().StringArrayVar(&links, "link", nil, "a link: title=https://… (repeatable, at most 3)")
	cmd.Flags().DurationVar(&u.TTL, "ttl", 0, "how long it shows (default 30m)")
	cmd.Flags().BoolVar(&clearIt, "clear", false, "remove the card")
	return cmd
}

func parseCardRow(s string) (plugin.CardRow, error) {
	var row plugin.CardRow
	if tone, rest, ok := strings.Cut(s, ":"); ok && slices.Contains(plugin.Tones, tone) && tone != "" {
		row.Tone, s = tone, rest
	}
	label, text, ok := strings.Cut(s, "=")
	if !ok {
		return row, fmt.Errorf("--row %q: want [tone:]label=text", s)
	}
	row.Label, row.Text = label, text
	return row, nil
}

func newPluginStatusCmd(a *app) *cobra.Command {
	var u plugin.StatusUpdate
	cmd := needsDaemon(&cobra.Command{
		Use:   "status <indicator> [text]",
		Short: "Show text on a tab-bar indicator the plugin declares (for plugins; no text clears it)",
		Args:  rangeArgs(1, 2),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.callingPlugin(u.Plugin)
			if err != nil {
				return err
			}
			u.Plugin, u.Indicator = id, args[0]
			if len(args) == 2 {
				u.Text = args[1]
			}
			c, err := client.NewPlugins(a.client).SetStatus(ctx, u)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"status": c}, "status %s: %s", c.Indicator, cmpStr(c.Text, "(cleared)"))
		}),
	})
	cmd.Flags().StringVar(&u.Plugin, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	cmd.Flags().StringVar(&u.Tone, "tone", "", "info, success, warning or error")
	cmd.Flags().DurationVar(&u.TTL, "ttl", 0, "how long it shows (default 30m)")
	return cmd
}

func newPluginReadyCmd(a *app) *cobra.Command {
	var id string
	cmd := needsDaemon(&cobra.Command{
		Use:   "ready",
		Short: "Say the plugin's startup is done (for plugins; otherwise 2s of uptime counts)",
		Args:  noArgs,
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			p, err := a.callingPlugin(id)
			if err != nil {
				return err
			}
			if err := client.NewPlugins(a.client).Ready(ctx, p); err != nil {
				return err
			}
			return a.done(map[string]any{"plugin": p}, "%s is ready", p)
		}),
	})
	cmd.Flags().StringVar(&id, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	return cmd
}

func newPluginRequestCmd(a *app) *cobra.Command {
	var id string
	cmd := needsDaemon(&cobra.Command{
		Use:   "request <capability> [reason]",
		Short: "Ask the user for an optional capability (for plugins; the user grants it)",
		Args:  rangeArgs(1, 2),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			p, err := a.callingPlugin(id)
			if err != nil {
				return err
			}
			reason := ""
			if len(args) == 2 {
				reason = args[1]
			}
			r, err := client.NewPlugins(a.client).Request(ctx, p, args[0], reason)
			if err != nil {
				return err
			}
			return a.done(map[string]any{"request": r}, "asked the user for %s (`hive plugin grant %s %s`)", r.Capability, p, r.Capability)
		}),
	})
	cmd.Flags().StringVar(&id, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	return cmd
}
