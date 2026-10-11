package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/metadata"
)

// newReportMetadataCmd is `hive pane report-metadata` and `hive env
// report-metadata`: tokens and a title for the sidebar.
func newReportMetadataCmd(a *app, onEnv bool) *cobra.Command {
	var (
		tokens []string
		title  string
		ttl    time.Duration
		reset  bool
	)
	what, noun, use, envVar := "pane", "pane", "report-metadata [pane]", "HIVE_PANE_ID"
	if onEnv {
		what, noun, use, envVar = "environment", "env", "report-metadata [env]", "HIVE_ENV_ID"
	}
	cmd := needsDaemon(&cobra.Command{
		Use:   use,
		Short: fmt.Sprintf("Put tokens and a title on a %s, for the sidebar", what),
		Long: fmt.Sprintf(`Show short tokens (a PR's state, a test count) beside a %s in the
multiplexer's sidebar, or give it a title. A token is key=text; reporting a
key again replaces its text, and an empty text removes it. Everything
expires after --ttl unless reported again, so a reporter that stops leaves
nothing stale. Without a %s, $%s is used: a program in a pane (an
agent, a plugin's command) can report about its own.`, what, what, envVar),
		Example: strings.ReplaceAll(`  hive NOUN report-metadata --token pr="#12 ✓" --token tests=40/40 --ttl 10m
  hive NOUN report-metadata --token pr=     # remove the token
  hive NOUN report-metadata --clear`, "NOUN", noun),
		Args: rangeArgs(0, 1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			if id == "" {
				id = a.getenv(envVar)
			}
			if id == "" {
				return &usageError{fmt.Errorf("no %s given (and $%s is not set)", what, envVar)}
			}
			rep := metadata.Report{TTL: ttl, Clear: reset, Source: a.getenv("HIVE_PLUGIN_ID")}
			if onEnv {
				rep.Environment = id
			} else {
				rep.Pane = id
			}
			if len(tokens) > 0 {
				rep.Tokens = map[string]string{}
			}
			for _, t := range tokens {
				k, v, ok := strings.Cut(t, "=")
				if !ok {
					return &usageError{fmt.Errorf("--token %q: want key=text", t)}
				}
				rep.Tokens[k] = v
			}
			if cmd.Flags().Changed("title") {
				rep.Title = &title
			}
			if rep.Tokens == nil && rep.Title == nil && !reset {
				return &usageError{errors.New("nothing to report: give --token, --title or --clear")}
			}
			e, err := client.NewPlugins(a.client).ReportMetadata(ctx, rep)
			if err != nil {
				return err
			}
			return a.emit(e, func() error { return printMetadata(a, []metadata.Entry{e}) })
		}),
	})
	cmd.Flags().StringArrayVar(&tokens, "token", nil, "a token as key=text (repeatable; key= removes it)")
	cmd.Flags().StringVar(&title, "title", "", "a title for it (\"\" removes it)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "how long it shows unless reported again (default 30m)")
	cmd.Flags().BoolVar(&reset, "clear", false, "remove its tokens and title first")
	return cmd
}

func printMetadata(a *app, list []metadata.Entry) error {
	w := a.table()
	fmt.Fprintln(w, "TARGET\tTITLE\tTOKENS")
	for _, e := range list {
		target := "pane " + e.Pane
		if e.Environment != "" {
			target = "env " + e.Environment
		}
		var toks []string
		for _, t := range e.Tokens {
			toks = append(toks, t.Key+"="+t.Text)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", target, cmpStr(e.Title, "-"), cmpStr(strings.Join(toks, "  "), "-"))
	}
	return w.Flush()
}

func newMetadataCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "metadata",
		Short: "List the tokens and titles on panes and environments (see report-metadata)",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			list, err := client.NewPlugins(a.client).Metadata(ctx)
			if err != nil {
				return err
			}
			return a.emit(list, func() error { return printMetadata(a, list) })
		}),
	})
}
