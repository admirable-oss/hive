package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/pane"
)

func newTabCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tab",
		Short: "Create, rename, focus and close tabs (layouts of panes)",
	}
	ws := func() client.Workspace { return client.NewWorkspace(a.client) }
	cmd.AddCommand(
		needsDaemon(&cobra.Command{
			Use:               "list [env]",
			Aliases:           []string{"ls"},
			Short:             "List tabs (all environments when none is given)",
			Args:              rangeArgs(0, 1),
			ValidArgsFunction: completeEnvironments(a),
			RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				envID := ""
				if len(args) > 0 {
					envID = args[0]
				}
				tabs, err := ws().TabList(ctx, envID)
				if err != nil {
					return err
				}
				return a.emit(tabs, func() error {
					w := a.table()
					fmt.Fprintln(w, "ID\tENV\tNAME\tPANES\tSIZE\tFOCUSED")
					for _, t := range tabs {
						n := len(t.Popups)
						if t.Layout != nil {
							n += len(t.Layout.Panes())
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%dx%d\t%s\n", t.ID, t.EnvironmentID, t.Name, n, t.Width, t.Height, cmpStr(t.Focused, "-"))
					}
					return w.Flush()
				})
			}),
		}),
		newTabCreateCmd(a),
		needsDaemon(&cobra.Command{
			Use:   "rename <tab> <name>",
			Short: "Rename a tab",
			Args:  exactArgs(2),
			RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				t, err := ws().TabRename(ctx, args[0], args[1])
				if err != nil {
					return err
				}
				return a.emit(t, func() error { fmt.Fprintf(a.out, "renamed tab %s to %q\n", t.ID, t.Name); return nil })
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:   "focus <tab>",
			Short: "Make a tab its environment's active one",
			Args:  exactArgs(1),
			RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				t, err := ws().TabFocus(ctx, args[0])
				if err != nil {
					return err
				}
				return a.emit(t, func() error { fmt.Fprintln(a.out, t.ID); return nil })
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:   "close <tab>",
			Short: "Close a tab and stop its panes' processes",
			Args:  exactArgs(1),
			RunE: withTimeout(paneTimeout+a.cfg.Daemon.ShutdownTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
				if err := ws().TabClose(ctx, args[0]); err != nil {
					return err
				}
				return a.done(map[string]any{"id": args[0]}, "closed tab %s", args[0])
			}),
		}),
	)
	return cmd
}

func newTabCreateCmd(a *app) *cobra.Command {
	var (
		name string
		spec paneSpecFlags
	)
	cmd := needsDaemon(&cobra.Command{
		Use:     "create <env> [-- command [args...]]",
		Aliases: []string{"new"},
		Short:   "Open a tab with one pane (prints the tab and pane IDs)",
		Example: `  hive tab create api --name agents -- claude
  read TAB PANE < <(hive tab create api)`,
		Args:              argsBeforeDash(1, 1),
		ValidArgsFunction: completeEnvironments(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, cmd *cobra.Command, args []string) error {
			before, command := splitAtDash(cmd, args)
			s, err := spec.spec(command)
			if err != nil {
				return err
			}
			res, err := client.NewWorkspace(a.client).TabCreate(ctx, pane.CreateTabRequest{EnvironmentID: before[0], Name: name, Pane: s})
			if err != nil {
				return err
			}
			return a.emit(res, func() error { fmt.Fprintf(a.out, "%s %s\n", res.Tab.ID, res.Pane.ID); return nil })
		}),
	})
	cmd.Flags().StringVar(&name, "name", "", "the tab's name")
	spec.register(cmd, "pane-name")
	return cmd
}

func newLayoutCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "layout",
		Short: "Save and recreate workspaces (environments, tabs, splits, commands) as JSON",
	}
	var output string
	export := needsDaemon(&cobra.Command{
		Use:   "export [env...]",
		Short: "Describe environments' tabs and panes as a layout file",
		Long: `Describe environments' tabs and panes (all environments when none are
given) as JSON that ` + "`hive layout apply`" + ` recreates: directories, variables,
splits with their ratios, and what each pane runs. Popups are left out.`,
		ValidArgsFunction: completeEnvironments(a),
		RunE: withTimeout(paneTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			spec, err := client.NewWorkspace(a.client).LayoutExport(ctx, args...)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if output == "" || output == "-" {
				_, err = a.out.Write(data)
				return err
			}
			return os.WriteFile(output, data, 0o600)
		}),
	})
	export.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of standard output")

	apply := needsDaemon(&cobra.Command{
		Use:   "apply <file|->",
		Short: "Create the environments, tabs and panes a layout file describes",
		Long: `Create what a layout file describes. Missing environments are created and
every tab is added (existing tabs are kept). When a pane cannot start, the
tabs this apply created are closed again.`,
		Args: exactArgs(1),
		RunE: withTimeout(2*time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			spec, err := readLayout(args[0])
			if err != nil {
				return err
			}
			res, err := client.NewWorkspace(a.client).LayoutApply(ctx, spec)
			if err != nil {
				return err
			}
			return a.emit(res, func() error {
				for _, id := range res.CreatedEnvironments {
					fmt.Fprintf(a.out, "created environment %q\n", id)
				}
				fmt.Fprintf(a.out, "opened %d tabs with %d panes\n", len(res.Tabs), res.Panes)
				return nil
			})
		}),
	})
	cmd.AddCommand(export, apply)
	return cmd
}

// readLayout reads a layout file ("-" is standard input). Relative roots
// are taken relative to the file's directory, so a layout can live in the
// project it describes.
func readLayout(path string) (pane.LayoutSpec, error) {
	var (
		data []byte
		err  error
		dir  string
	)
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
		dir, _ = os.Getwd()
	} else {
		data, err = os.ReadFile(path)
		dir = filepath.Dir(path)
	}
	if err != nil {
		return pane.LayoutSpec{}, err
	}
	var spec pane.LayoutSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return spec, &usageError{fmt.Errorf("%s: invalid JSON at byte %d: %w", path, se.Offset, err)}
		}
		return spec, &usageError{fmt.Errorf("%s: %w", path, err)}
	}
	for i, e := range spec.Environments {
		if e.Root != "" && !filepath.IsAbs(e.Root) {
			abs, err := filepath.Abs(filepath.Join(dir, e.Root))
			if err != nil {
				return spec, err
			}
			spec.Environments[i].Root = abs
		}
	}
	return spec, nil
}
