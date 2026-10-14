package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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

func (a *app) pluginRegistry() *plugin.RegistryClient {
	return plugin.NewRegistryClient(a.getenv("HIVE_PLUGIN_REGISTRY_URL"))
}

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
one shows everything it will run and asks first. See https://hive.admir-saheta.com/docs/config/plugins/.`,
		Example: `  hive plugin install https://github.com/you/hive-pr-status
  hive plugin search pull requests
  hive plugin install pr-status
  hive plugin link ./my-plugin
  hive plugin run pr-status refresh`,
	}
	cmd.AddCommand(newPluginSearchCmd(a), newPluginInstallCmd(a), newPluginLinkCmd(a), newPluginListCmd(a), newPluginInfoCmd(a),
		newPluginToggleCmd(a, true), newPluginToggleCmd(a, false), newPluginUninstallCmd(a),
		newPluginRunCmd(a), newPluginLogsCmd(a))
	return cmd
}

func newPluginInstallCmd(a *app) *cobra.Command {
	var ref, subdir string
	cmd := &cobra.Command{
		Use:   "install <id-or-git-url-or-path>",
		Short: "Install a catalog plugin by ID or a plugin from git",
		Args:  exactArgs(1),
		RunE: withTimeout(pluginTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			source := args[0]
			var listing *plugin.Listing
			if isPluginID(source) {
				if _, statErr := os.Stat(source); errors.Is(statErr, os.ErrNotExist) {
					if ref != "" {
						return errors.New("--ref applies to Git installs; catalog releases are already versioned")
					}
					if subdir != "" {
						return errors.New("--subdir applies to Git installs; catalog releases declare their package path")
					}
					catalog, err := a.pluginRegistry().Fetch(ctx)
					if err != nil {
						return fmt.Errorf("%w (Git installation remains available: `hive plugin install <git-url>`)", err)
					}
					entry, ok := catalog.Get(source)
					if !ok {
						return fmt.Errorf("plugin %q is not in the registry (search with `hive plugin search`)", source)
					}
					if err := entry.SupportsHive(buildinfo.Get().Version); err != nil {
						return err
					}
					release := entry.Releases[0]
					listing = &entry
					source, ref = entry.Repository, release.Ref
					f, err := store.FetchSubdir(ctx, source, ref, release.Subdir)
					if err != nil {
						return err
					}
					if err := f.VerifyArchiveChecksum(ctx, release.Checksum); err != nil {
						f.Discard()
						return fmt.Errorf("verify plugin %s@%s: %w", entry.ID, release.Version, err)
					}
					if f.Manifest.ID != entry.ID || strings.TrimPrefix(f.Manifest.Version, "v") != strings.TrimPrefix(release.Version, "v") || !strings.EqualFold(f.Commit, release.Commit) {
						f.Discard()
						return fmt.Errorf("registry release %s@%s does not match its manifest or pinned source revision", entry.ID, release.Version)
					}
					return a.installFetchedPlugin(ctx, store, f, listing)
				}
			}
			f, err := store.FetchSubdir(ctx, source, ref, subdir)
			if err != nil {
				return err
			}
			return a.installFetchedPlugin(ctx, store, f, nil)
		}),
	}
	cmd.Flags().StringVar(&ref, "ref", "", "a branch or tag to install (default: the repository's default branch)")
	cmd.Flags().StringVar(&subdir, "subdir", "", "install a plugin package from a repository subdirectory")
	return cmd
}

func (a *app) installFetchedPlugin(ctx context.Context, store *plugin.Store, f *plugin.Fetched, listing *plugin.Listing) error {
	if listing != nil {
		fmt.Fprintf(a.errOut, "Catalog listing: %s (%s)\n  license: %s\n  repository: %s\n  pinned commit: %s\n", listing.Name, listing.ID, cmpStr(listing.License, "unspecified"), listing.Repository, f.Commit)
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
}

func isPluginID(s string) bool {
	return !strings.Contains(s, "://") && plugin.ValidID(s)
}

func newPluginSearchCmd(a *app) *cobra.Command {
	var category string
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the public plugin catalog",
		Args:  minArgs(0),
		RunE: withTimeout(20*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			catalog, err := a.pluginRegistry().Fetch(ctx)
			if err != nil {
				return err
			}
			entries := catalog.Search(query, category)
			return a.emit(entries, func() error {
				if len(entries) == 0 {
					_, err := fmt.Fprintln(a.out, "no plugins found")
					return err
				}
				w := a.table()
				fmt.Fprintln(w, "ID\tNAME\tLATEST\tCATEGORY\tSTATUS\tDESCRIPTION")
				for _, p := range entries {
					latest := p.Releases[0].Version
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, latest, strings.Join(p.Categories, ","), listingStatus(p), p.Description)
				}
				return w.Flush()
			})
		}),
	}
	cmd.Flags().StringVar(&category, "category", "", "filter by category")
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
			catalog, err := a.pluginRegistry().Fetch(ctx)
			if err != nil {
				return fmt.Errorf("%w (installed plugins remain available with `hive plugin list`)", err)
			}
			listing, ok := catalog.Get(args[0])
			if !ok {
				return fmt.Errorf("%w %q", plugin.ErrNotFound, args[0])
			}
			return a.emit(listing, func() error {
				fmt.Fprintf(a.out, "%s (%s)\n", listing.Name, listing.ID)
				fmt.Fprintf(a.out, "  %s\n", listing.Description)
				fmt.Fprintf(a.out, "  latest: %s\n  license: %s\n  platforms: %s\n  Hive: %s\n  plugin API: %s\n", listing.Releases[0].Version, cmpStr(listing.License, "unspecified"), cmpStr(strings.Join(listing.Platforms, ", "), "all"), hiveRange(listing.MinHiveVersion, listing.MaxHiveVersion), cmpStr(listing.API, "unspecified"))
				fmt.Fprintf(a.out, "  repository: %s\n", listing.Repository)
				if listing.Documentation != "" {
					fmt.Fprintf(a.out, "  docs: %s\n", listing.Documentation)
				}
				if listing.Issues != "" {
					fmt.Fprintf(a.out, "  issues: %s\n", listing.Issues)
				}
				if listing.Install != "" {
					fmt.Fprintf(a.out, "  installation: %s\n", listing.Install)
				}
				if listing.Configuration != "" {
					fmt.Fprintf(a.out, "  configuration: %s\n", listing.Configuration)
				}
				for _, screenshot := range listing.Screenshots {
					fmt.Fprintf(a.out, "  screenshot: %s\n", screenshot)
				}
				fmt.Fprintf(a.out, "  maintainers: %s\n", maintainerNames(listing.Maintainers))
				fmt.Fprintf(a.out, "  status: %s\n", listingStatus(listing))
				fmt.Fprintln(a.out, "  releases:")
				for _, release := range listing.Releases {
					fmt.Fprintf(a.out, "    %s  %s  %s\n", release.Version, release.PublishedAt.Format(time.DateOnly), release.Commit[:12])
					if release.Changelog != "" {
						fmt.Fprintf(a.out, "      %s\n", release.Changelog)
					}
				}
				return nil
			})
		}),
	}
}

func hiveRange(minVersion, maxVersion string) string {
	if minVersion == "" && maxVersion == "" {
		return "any"
	}
	if maxVersion == "" {
		return ">=" + minVersion
	}
	if minVersion == "" {
		return "<=" + maxVersion
	}
	return minVersion + " to " + maxVersion
}

func maintainerNames(maintainers []plugin.Maintainer) string {
	names := make([]string, 0, len(maintainers))
	for _, m := range maintainers {
		names = append(names, m.Name)
	}
	return cmpStr(strings.Join(names, ", "), "unspecified")
}

func listingStatus(p plugin.Listing) string {
	switch {
	case p.Archived:
		return "archived"
	case p.Deprecated:
		return "deprecated"
	case p.Official:
		return "official"
	case p.Verified:
		return "verified"
	default:
		return "community"
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
	var deleteData bool
	cmd := &cobra.Command{
		Use:     "uninstall <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Stop and remove a plugin while preserving its data",
		Args:    exactArgs(1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			if _, err := store.Get(args[0], buildinfo.Get().Version); err != nil {
				return err
			}
			if deleteData && !a.confirm("Permanently delete this plugin's data too?") {
				return fmt.Errorf("plugin data kept; rerun with --delete-data --yes to delete it")
			}
			if _, err := store.SetEnabled(args[0], false); err != nil {
				return err
			}
			a.reloadPlugins(ctx) // its commands stop before its files go
			if err := store.Uninstall(args[0], deleteData); err != nil {
				return err
			}
			if deleteData {
				return a.done(map[string]any{"plugin": args[0], "data_deleted": true}, "uninstalled plugin %s and deleted its data", args[0])
			}
			return a.done(map[string]any{"plugin": args[0], "data_deleted": false}, "uninstalled plugin %s (data kept under %s)", args[0], store.DataDir(args[0]))
		}),
	}
	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "also permanently delete the plugin's data directory")
	return cmd
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
