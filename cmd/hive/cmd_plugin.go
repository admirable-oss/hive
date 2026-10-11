package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
)

// pluginTimeout allows for cloning a plugin.
const pluginTimeout = 5 * time.Minute

func (a *app) plugins() *plugin.Store { return plugin.NewStore(filepath.Join(a.base, "plugins")) }

// reloadPlugins tells a running daemon that plugins changed; one that is
// not running reads them when it starts.
func (a *app) reloadPlugins(ctx context.Context) {
	if _, ok := a.daemonRunning(ctx); !ok {
		return
	}
	if _, err := client.NewPlugins(a.client).Reload(ctx); err != nil {
		fmt.Fprintf(a.errOut, "hive: warning: the daemon did not reload its plugins: %v (`hive daemon restart` does)\n", err)
	}
}

func newPluginCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"plugins"},
		Short:   "Install and manage plugins: programs Hive runs for you on startup, on events and on demand",
		Long: `A plugin is a git repository (or a directory) with a hive-plugin.toml that
says what to run: [[startup]] commands while the daemon runs, [[events]]
commands when something happens (worktree.created, agent.state, …), and
[[actions]] you run by name or bind to keys. Plugins run as you; installing
one shows everything it will run and asks first. See docs/plugins.md.`,
		Example: `  hive plugin install https://github.com/you/hive-pr-status
  hive plugin link ./my-plugin
  hive plugin run pr-status refresh`,
	}
	cmd.AddCommand(newPluginInstallCmd(a), newPluginLinkCmd(a), newPluginListCmd(a), newPluginInfoCmd(a),
		newPluginToggleCmd(a, true), newPluginToggleCmd(a, false), newPluginUninstallCmd(a),
		newPluginRunCmd(a), newPluginLogsCmd(a))
	return cmd
}

func newPluginInstallCmd(a *app) *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "install <git-url-or-path>",
		Short: "Install a plugin from git, after showing what it runs",
		Args:  exactArgs(1),
		RunE: withTimeout(pluginTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			f, err := store.Fetch(ctx, args[0], ref)
			if err != nil {
				return err
			}
			fmt.Fprint(a.errOut, f.Preview())
			if !a.confirm("Install it?") {
				f.Discard()
				return fmt.Errorf("%w (run again with --yes to install it)", plugin.ErrDeclined)
			}
			in, err := store.Install(f, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			a.reloadPlugins(ctx)
			return a.done(map[string]any{"plugin": in}, "installed plugin %s (%s)", in.ID, in.Source)
		}),
	}
	cmd.Flags().StringVar(&ref, "ref", "", "a branch or tag to install (default: the repository's default branch)")
	return cmd
}

func newPluginLinkCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "link <dir>",
		Short: "Install a plugin from a directory as it is, for developing it (changes apply on reload)",
		Args:  exactArgs(1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			in, m, err := a.plugins().Link(args[0], buildinfo.Get().Version)
			if err != nil {
				return err
			}
			fmt.Fprint(a.errOut, m.Preview(in.Source))
			a.reloadPlugins(ctx)
			return a.done(map[string]any{"plugin": in}, "linked plugin %s to %s", in.ID, in.Source)
		}),
	}
}

// pluginRow is a plugin as `plugin list` shows it.
type pluginRow struct {
	plugin.Plugin
	Running   int    `json:"running"`
	LastError string `json:"last_error,omitempty"`
}

func (a *app) pluginRows(ctx context.Context) ([]pluginRow, error) {
	list, err := a.plugins().List(buildinfo.Get().Version)
	if err != nil {
		return nil, err
	}
	live := map[string]plugin.Status{}
	if _, ok := a.daemonRunning(ctx); ok {
		if st, err := client.NewPlugins(a.client).List(ctx); err == nil {
			for _, s := range st {
				live[s.ID] = s
			}
		}
	}
	rows := make([]pluginRow, 0, len(list))
	for _, p := range list {
		s := live[p.ID]
		rows = append(rows, pluginRow{Plugin: p, Running: s.Running, LastError: s.LastError})
	}
	return rows, nil
}

func newPluginListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed plugins",
		Args:    noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			rows, err := a.pluginRows(ctx)
			if err != nil {
				return err
			}
			return a.emit(rows, func() error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(a.out, "no plugins (install one with `hive plugin install <git-url>`)")
					return err
				}
				w := a.table()
				fmt.Fprintln(w, "ID\tVERSION\tSTATE\tSOURCE")
				for _, r := range rows {
					version, state := "-", "enabled"
					if r.Manifest != nil {
						version = r.Manifest.Version
					}
					switch {
					case r.Problem != "":
						state = "broken: " + r.Problem
					case !r.Enabled:
						state = "disabled"
					case r.LastError != "":
						state = "error: " + r.LastError
					}
					src := r.Source
					if r.Linked {
						src += " (linked)"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.ID, version, state, src)
				}
				return w.Flush()
			})
		}),
	}
}

func newPluginInfoCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "info <id>",
		Short: "Show what a plugin runs",
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			rows, err := a.pluginRows(ctx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if r.ID != args[0] {
					continue
				}
				return a.emit(r, func() error {
					if r.Manifest != nil {
						fmt.Fprint(a.out, r.Manifest.Preview(r.Source))
					}
					fmt.Fprintf(a.out, "  enabled: %v; running commands: %d\n", r.Enabled, r.Running)
					if r.Problem != "" {
						fmt.Fprintf(a.out, "  problem: %s\n", r.Problem)
					}
					if r.LastError != "" {
						fmt.Fprintf(a.out, "  last error: %s\n", r.LastError)
					}
					return nil
				})
			}
			return fmt.Errorf("%w %q", plugin.ErrNotFound, args[0])
		}),
	}
}

func newPluginToggleCmd(a *app, on bool) *cobra.Command {
	use, short, verb := "enable <id>", "Turn a plugin on", "enabled"
	if !on {
		use, short, verb = "disable <id>", "Turn a plugin off without uninstalling it", "disabled"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  exactArgs(1),
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			in, err := a.plugins().SetEnabled(args[0], on)
			if err != nil {
				return err
			}
			a.reloadPlugins(ctx)
			return a.done(map[string]any{"plugin": in}, "%s plugin %s", verb, in.ID)
		}),
	}
}

func newPluginUninstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Stop a plugin and remove it and its data (a linked plugin's directory is kept)",
		Args:    exactArgs(1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			if _, err := store.SetEnabled(args[0], false); err != nil {
				return err
			}
			a.reloadPlugins(ctx) // its commands stop before its files go
			if err := store.Uninstall(args[0]); err != nil {
				return err
			}
			return a.done(map[string]any{"plugin": args[0]}, "uninstalled plugin %s", args[0])
		}),
	}
}

func newPluginRunCmd(a *app) *cobra.Command {
	var req client.PluginRun
	cmd := needsDaemon(&cobra.Command{
		Use:   "run <id> <action>",
		Short: "Run a plugin's action and print what it printed (exit status: the action's)",
		Args:  exactArgs(2),
		RunE: withTimeout(time.Hour, func(ctx context.Context, _ *cobra.Command, args []string) error {
			req.Plugin, req.Action = args[0], args[1]
			if req.Pane == "" {
				req.Pane = a.getenv("HIVE_PANE_ID")
			}
			if req.Environment == "" {
				req.Environment = a.getenv("HIVE_ENV_ID")
			}
			res, err := client.NewPlugins(a.client).Run(ctx, req)
			if err != nil {
				return err
			}
			if err := a.emit(res, func() error { _, err := fmt.Fprint(a.out, res.Output); return err }); err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return &codedError{code: exitError, msg: fmt.Sprintf("%s %s exited with status %d", req.Plugin, req.Action, res.ExitCode)}
			}
			return nil
		}),
	})
	cmd.Flags().StringVar(&req.Environment, "env", "", "the environment it is about (default $HIVE_ENV_ID)")
	cmd.Flags().StringVar(&req.Pane, "pane", "", "the pane it is about (default $HIVE_PANE_ID)")
	return cmd
}

func newPluginLogsCmd(a *app) *cobra.Command {
	var (
		lines  int
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Show what a plugin's commands printed",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.ContainsAny(args[0], "/\\.") {
				return &usageError{errors.New("not a plugin id")}
			}
			return tailLocalFile(cmd.Context(), filepath.Join(a.root, "logs", "plugins", args[0]+".log"), lines, follow, a.out)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of lines to show (0 for all)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	return cmd
}
