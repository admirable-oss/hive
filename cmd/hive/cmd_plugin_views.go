package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
)

// Pane views and workflow triggers (plugin API 2): `views` and `click`
// are for people, `view` and `trigger` for plugins.

func newPluginViewsCmd(a *app) *cobra.Command {
	var width int
	cmd := needsDaemon(&cobra.Command{
		Use:   "views [plugin/view]",
		Short: "List the plugins' pane views, or draw one as text",
		Long: `views lists every view the installed plugins declare. With a view, it draws
what the view shows now, as the TUI does in a pane. A pane shows a view with
` + "`hive pane split --view plugin/view`" + ` or ` + "`hive tab create <env> --view plugin/view`" + `.`,
		Example: `  hive plugin views
  hive plugin views review/queue`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			views, err := client.NewPlugins(a.client).Views(ctx)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return a.emit(views, func() error {
					if len(views) == 0 {
						fmt.Fprintln(a.out, "No plugin declares a view.")
						return nil
					}
					w := a.table()
					fmt.Fprintln(w, "VIEW\tTITLE\tSHOWS")
					for _, v := range views {
						fmt.Fprintf(w, "%s/%s\t%s\t%s\n", v.Plugin, v.View, v.Title, viewState(v))
					}
					return w.Flush()
				})
			}
			id, name, ok := strings.Cut(args[0], "/")
			if !ok {
				return &usageError{fmt.Errorf("%q: write plugin/view", args[0])}
			}
			for _, v := range views {
				if v.Plugin != id || v.View != name {
					continue
				}
				return a.emit(v, func() error {
					fmt.Fprintf(a.out, "%s (%s/%s)\n\n", v.Title, v.Plugin, v.View)
					if v.Doc == nil {
						fmt.Fprintln(a.out, "(nothing yet)")
						return nil
					}
					for _, l := range plugin.DrawView(*v.Doc, width) {
						fmt.Fprintln(a.out, strings.TrimRight(viewLineText(l), " "))
					}
					return nil
				})
			}
			return &codedError{code: exitError, msg: fmt.Sprintf("no plugin view %s (hive plugin views)", args[0])}
		}),
	})
	cmd.Flags().IntVar(&width, "width", 80, "the width to draw it at")
	return cmd
}

func viewState(v plugin.ViewContent) string {
	switch {
	case !v.Available:
		return "(plugin cannot run)"
	case v.Doc == nil:
		return "(nothing yet)"
	}
	return "set " + v.Updated.Local().Format("15:04:05")
}

// viewLineText is a drawn line for a terminal that shows no styles: links
// keep their address.
func viewLineText(l plugin.ViewLine) string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
		if s.URL != "" && s.Text != s.URL {
			b.WriteString(" <" + s.URL + ">")
		}
	}
	return b.String()
}

func newPluginViewCmd(a *app) *cobra.Command {
	var (
		pluginID string
		docPath  string
		clearIt  bool
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "view <view>",
		Short: "Set what one of the plugin's pane views shows (for plugins)",
		Long: `view sets the document one of the plugin's [[contributes.views]] shows: JSON,
{"root": {"kind": "column", "children": [...]}}, from a file or standard input
(--doc -). Nodes are text, row, column, list, table, progress, badge, divider,
button (runs one of the plugin's [[actions]]) and link (https only). --clear
empties the view.`,
		Example: `  echo '{"root":{"kind":"text","text":"3 waiting","style":"bold"}}' | hive plugin view queue --doc -
  hive plugin view queue --clear`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.callingPlugin(pluginID)
			if err != nil {
				return err
			}
			c := client.NewPlugins(a.client)
			if clearIt {
				v, err := c.ClearView(ctx, id, args[0])
				if err != nil {
					return err
				}
				return a.done(map[string]any{"view": v}, "view %s cleared", v.View)
			}
			if docPath == "" {
				return &usageError{errors.New("give --doc (a file, or - for standard input) or --clear")}
			}
			doc, err := readViewDoc(docPath)
			if err != nil {
				return err
			}
			v, err := c.SetView(ctx, plugin.ViewUpdate{Plugin: id, View: args[0], Doc: &doc})
			if err != nil {
				return err
			}
			return a.done(map[string]any{"view": v}, "view %s set", v.View)
		}),
	})
	cmd.Flags().StringVar(&pluginID, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	cmd.Flags().StringVar(&docPath, "doc", "", "the view document (JSON): a file, or - for standard input")
	cmd.Flags().BoolVar(&clearIt, "clear", false, "empty the view")
	return cmd
}

func readViewDoc(path string) (plugin.ViewDoc, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return plugin.ViewDoc{}, &usageError{err}
		}
		defer f.Close()
		r = f
	}
	var doc plugin.ViewDoc
	dec := json.NewDecoder(io.LimitReader(r, plugin.MaxViewBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return doc, &usageError{fmt.Errorf("the view document: %w", err)}
	}
	return doc, nil
}

func newPluginClickCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:     "click <plugin/view> <button>",
		Short:   "Press a button on a plugin's view: run the action it names",
		Example: `  hive plugin click review/queue approve-12`,
		Args:    exactArgs(2),
		RunE: withTimeout(time.Hour, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, view, ok := strings.Cut(args[0], "/")
			if !ok {
				return &usageError{fmt.Errorf("%q: write plugin/view", args[0])}
			}
			res, err := client.NewPlugins(a.client).ClickView(ctx, plugin.ViewClick{
				Plugin: id, View: view, Node: args[1],
				Environment: a.getenv("HIVE_ENV_ID"), Pane: a.getenv("HIVE_PANE_ID"),
			})
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error { _, err := fmt.Fprint(a.out, res.Output); return err }); err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return &codedError{code: exitError, msg: fmt.Sprintf("%s's button %s: the action exited with status %d", args[0], args[1], res.ExitCode)}
			}
			return nil
		}),
	})
}

func newPluginTriggerCmd(a *app) *cobra.Command {
	var (
		pluginID string
		payload  string
		fields   []string
	)
	cmd := needsDaemon(&cobra.Command{
		Use:   "trigger <name>",
		Short: "Fire one of the plugin's workflow triggers (for plugins; prints the runs it started)",
		Long: `trigger fires one of the plugin's [[contributes.triggers]]. Workflows whose
[[on]] says plugin = "<plugin>/<name>", and whose match fits the payload, run;
a shared workflow runs only once you trusted it.`,
		Example: `  hive plugin trigger deploy-finished --field state=error --field url=https://preview.example.org
  hive plugin trigger deploy-finished --payload '{"state": "ready", "attempt": 2}'`,
		Args: exactArgs(1),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, err := a.callingPlugin(pluginID)
			if err != nil {
				return err
			}
			data := map[string]any{}
			if payload != "" {
				if err := json.Unmarshal([]byte(payload), &data); err != nil {
					return &usageError{fmt.Errorf("--payload: a JSON object: %w", err)}
				}
			}
			for _, f := range fields {
				k, v, ok := strings.Cut(f, "=")
				if !ok || k == "" {
					return &usageError{fmt.Errorf("--field %q: write key=value", f)}
				}
				data[k] = v
			}
			res, err := client.NewWorkflows(a.client).Trigger(ctx, id, args[0], data)
			if err != nil {
				return workflowErr(err)
			}
			return a.emit(res, func() error {
				if len(res.Runs) == 0 {
					fmt.Fprintln(a.errOut, "No workflow runs on it.")
				}
				for _, r := range res.Runs {
					fmt.Fprintln(a.out, r)
				}
				return nil
			})
		}),
	})
	cmd.Flags().StringVar(&pluginID, "plugin", "", "the plugin (default $HIVE_PLUGIN_ID)")
	cmd.Flags().StringVar(&payload, "payload", "", "the payload, a JSON object")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "a payload field, key=value (repeatable; added to --payload)")
	return cmd
}
