package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
func (a *app) reloadPlugins(ctx context.Context) error {
	if _, ok := a.daemonRunning(ctx); !ok {
		return nil
	}
	if _, err := client.NewPlugins(a.client).Reload(ctx); err != nil {
		fmt.Fprintf(a.errOut, "hive: warning: the daemon did not reload its plugins: %v (`hive daemon restart` does)\n", err)
		return err
	}
	return nil
}

func (a *app) checkPluginReady(ctx context.Context, id string) error {
	if _, ok := a.daemonRunning(ctx); !ok {
		return nil
	}
	statuses, err := client.NewPlugins(a.client).List(ctx)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		if status.ID != id {
			continue
		}
		if status.LastError != "" {
			return fmt.Errorf("plugin %s did not load: %s", id, status.LastError)
		}
		return nil
	}
	return fmt.Errorf("plugin %s was not loaded by the daemon", id)
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
		newPluginRunCmd(a), newPluginLogsCmd(a), newPluginValidateCmd(a), newPluginInitCmd(a), newPluginUpdateCmd(a), newPluginRollbackCmd(a),
		newPluginPublishCmd(a), newPluginTestCmd(a),
		newPluginReloadCmd(a), newPluginGrantCmd(a, true), newPluginGrantCmd(a, false), newPluginDoctorCmd(a), newPluginConfigCmd(a),
		newPluginContributionsCmd(a), newPluginCapabilitiesCmd(a), newPluginCancelCmd(a), newPluginExportCmd(a), newPluginImportCmd(a),
		newPluginCardCmd(a), newPluginStatusCmd(a), newPluginReadyCmd(a), newPluginRequestCmd(a))
	return cmd
}

func newPluginInitCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "init <id> [directory]",
		Short:   "Scaffold a valid shell-based Hive plugin",
		Args:    rangeArgs(1, 2),
		Example: "  hive plugin init my-plugin\n  hive plugin init my-plugin ./plugins/my-plugin",
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			if !plugin.ValidID(id) {
				return fmt.Errorf("invalid plugin ID %q: use lower-case letters, digits, and dashes", id)
			}
			dir := id
			if len(args) == 2 {
				dir = args[1]
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			if err := os.Mkdir(abs, 0o755); err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("destination %s already exists; choose a new directory", abs)
				}
				return err
			}
			files := map[string]struct {
				body string
				mode os.FileMode
			}{
				plugin.ManifestFile: {body: fmt.Sprintf(`api = 2
id = %q
name = %q
version = "0.1.0"
description = "A Hive plugin."
min_hive_version = "1.7.0"
# Plugin API 2 runs sandboxed: the plugin reads its own files and the
# system's, writes its own directories, and reaches the network or runs
# other programs only as [permissions] say.

[permissions]
# What it may do (see `+"`hive plugin capabilities`"+`).
required = ["hive.read"]
optional = []
# To reach the network or run programs, request "network" or
# "process.exec" and list exactly what:
# [permissions.network]
# hosts = ["api.example.com:443"]
# [permissions.process]
# exec = ["git"]

# What `+"`hive plugin test`"+` and `+"`hive plugin publish`"+` run.
[test]
command = ["sh", "./hello.sh"]

[[actions]]
id = "hello"
title = "Hello from %s"
command = ["sh", "./hello.sh"]

# Offer the action in the command palette.
[[contributes.palette]]
action = "hello"
`, id, id, id), mode: 0o644},
				"hello.sh":  {body: "#!/bin/sh\nprintf '%s\\n' 'Hello from Hive plugin'\n", mode: 0o755},
				"README.md": {body: fmt.Sprintf("# %s\n\nA Hive plugin scaffold.\n\n## Validate\n\n```sh\nhive plugin validate .\n```\n\n## Try it\n\n```sh\nhive plugin link .\nhive plugin run %s hello\n```\n\nEdit `hello.sh` and `hive-plugin.toml` to customize this plugin. It runs sandboxed (plugin API 2): it reaches the network and runs other programs only as `[permissions]` say.\n\n## Test and publish\n\n```sh\nhive plugin test\nhive plugin publish --dry-run\n```\n", id, id), mode: 0o644},
			}
			created := make([]string, 0, len(files))
			for name, file := range files {
				path := filepath.Join(abs, name)
				if err := os.WriteFile(path, []byte(file.body), file.mode); err != nil {
					for _, createdPath := range created {
						_ = os.Remove(createdPath)
					}
					_ = os.Remove(abs)
					return fmt.Errorf("write plugin scaffold: %w", err)
				}
				created = append(created, path)
			}
			if _, err := plugin.LoadManifest(abs); err != nil {
				for _, createdPath := range created {
					_ = os.Remove(createdPath)
				}
				_ = os.Remove(abs)
				return fmt.Errorf("validate generated plugin scaffold: %w", err)
			}
			return a.done(map[string]any{"id": id, "directory": abs}, "created plugin scaffold %s in %s", id, abs)
		},
	}
}

func newPluginValidateCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "validate [dir]",
		Short: "Validate a plugin manifest and runtime requirements",
		Args:  rangeArgs(0, 1),
		Example: `  hive plugin validate
  hive plugin validate ./my-plugin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			manifest, err := plugin.LoadManifest(dir)
			if err != nil {
				return err
			}
			if err := manifest.Supported(cmd.Context(), buildinfo.Get().Version); err != nil {
				return err
			}
			if manifest.Legacy() {
				fmt.Fprintf(a.errOut, "warning: no api = %d and [permissions]: the plugin will hold every capability\n", plugin.APIVersion)
			}
			for _, e := range manifest.Events {
				if !plugin.KnownEvent(e.On) {
					fmt.Fprintf(a.errOut, "warning: [[events]] on %q never fires in plugin API %d (see `hive plugin capabilities`)\n", e.On, plugin.APIVersion)
				}
			}
			switch {
			case manifest.API >= 2 && !manifest.Wasm() && manifest.Isolation != plugin.IsolationNone && !plugin.SandboxAvailable():
				fmt.Fprintf(a.errOut, "warning: this system has no sandbox for plugins (sandbox-exec on macOS, bwrap on Linux): the plugin runs here only once granted %s\n", plugin.CapUnconfined)
			case manifest.Isolation == "strict" && !plugin.SandboxAvailable():
				fmt.Fprintln(a.errOut, "warning: isolation = \"strict\" needs sandbox-exec (macOS) or bwrap (Linux), which this system lacks")
			}
			return a.emit(map[string]any{"valid": true, "manifest": manifest}, func() error {
				fmt.Fprintf(a.out, "valid %s %s\n", manifest.ID, manifest.Version)
				fmt.Fprint(a.out, manifest.Preview(dir))
				return nil
			})
		},
	}
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
					release, ok := entry.LatestFor(buildinfo.Get().Version)
					switch {
					case !ok && entry.Releases[0].Revoked != nil && !slices.ContainsFunc(entry.Releases, func(r plugin.Release) bool { return r.Revoked == nil }):
						return fmt.Errorf("every release of %s is revoked (%s); it cannot be installed", entry.ID, revocationText(*entry.Releases[0].Revoked))
					case !ok:
						return fmt.Errorf("no release of %s runs on this hive %s (plugin API %s); update hive (`hive update`)", entry.ID, buildinfo.Get().Version, pluginAPIs())
					}
					if err := a.checkReleaseSignature(catalog, entry, release); err != nil {
						return err
					}
					a.warnListing(entry)
					listing = &entry
					source, ref = entry.Repository, release.Ref
					fmt.Fprintf(a.errOut, "Fetching catalog plugin %s@%s…\n", entry.ID, release.Version)
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
					f.Version, f.Checksum, f.Registry, f.Trust = release.Version, release.Checksum, true, plugin.TrustOf(entry, release)
					return a.installFetchedPlugin(ctx, store, f, listing)
				}
			}
			fmt.Fprintln(a.errOut, "Fetching plugin source…")
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

func newPluginUpdateCmd(a *app) *cobra.Command {
	return &cobra.Command{Use: "update [id]", Short: "Update registry-installed plugins", Args: rangeArgs(0, 1), RunE: withTimeout(pluginTimeout, func(ctx context.Context, _ *cobra.Command, args []string) error {
		store := a.plugins()
		if err := store.Recover(); err != nil {
			return err
		}
		installed, err := store.List(ctx, buildinfo.Get().Version)
		if err != nil {
			return err
		}
		wanted := map[string]bool{}
		if len(args) == 1 {
			var match *plugin.Plugin
			for i := range installed {
				if installed[i].ID == args[0] {
					match = &installed[i]
					break
				}
			}
			if match == nil {
				return fmt.Errorf("%w %q", plugin.ErrNotFound, args[0])
			}
			if !match.Registry {
				return fmt.Errorf("%s is not registry-installed; update is unavailable", match.ID)
			}
			wanted[args[0]] = true
		} else {
			for _, p := range installed {
				if p.Registry {
					wanted[p.ID] = true
				}
			}
		}
		if len(wanted) == 0 {
			return a.done(map[string]any{"updated": []plugin.Installed{}}, "no registry-installed plugins")
		}
		catalog, err := a.pluginRegistry().Fetch(ctx)
		if err != nil {
			return fmt.Errorf("cannot update plugins while the registry is unavailable: %w", err)
		}
		updated := make([]plugin.Installed, 0, len(wanted))
		for _, p := range installed {
			if !wanted[p.ID] {
				continue
			}
			if !p.Registry {
				return fmt.Errorf("%s is not registry-installed; update is unavailable", p.ID)
			}
			revoked, isRevoked := catalog.Revocation(p.ID, p.Version)
			entry, ok := catalog.Get(p.ID)
			switch {
			case !ok && isRevoked:
				return fmt.Errorf("installed plugin %s %s is revoked (%s) and no longer listed; uninstall it with `hive plugin uninstall %s`", p.ID, p.Version, revocationText(revoked), p.ID)
			case !ok:
				return fmt.Errorf("installed plugin %s is no longer listed; refusing an unverified update", p.ID)
			}
			if err := entry.SupportsHive(buildinfo.Get().Version); err != nil {
				return err
			}
			// A revoked release is left whatever its record says: moving off
			// it, to a release the registry vouches for, is what matters.
			if isRevoked {
				fmt.Fprintf(a.errOut, "hive: the installed %s %s is revoked: %s\n", p.ID, p.Version, revocationText(revoked))
			} else if err := verifyInstalledRelease(p.Installed, entry); err != nil {
				return err
			}
			release, ok := entry.LatestFor(buildinfo.Get().Version)
			if !ok {
				if _, installable := entry.Latest(); !installable {
					return fmt.Errorf("every release of %s is revoked; disable or uninstall it (`hive plugin uninstall %s`)", p.ID, p.ID)
				}
				fmt.Fprintf(a.errOut, "%s: no newer release runs on this hive %s\n", p.ID, buildinfo.Get().Version)
				continue
			}
			a.warnListing(entry)
			if release.Version == p.Version || strings.EqualFold(release.Commit, p.Commit) {
				fmt.Fprintf(a.errOut, "%s is already current (%s)\n", p.ID, p.Version)
				continue
			}
			if err := a.checkReleaseSignature(catalog, entry, release); err != nil {
				return err
			}
			if err := plugin.CheckContinuity(a.cfg.Plugins.RequireSignature, p.Installed, entry, release); err != nil {
				return err
			}
			fmt.Fprintf(a.errOut, "Fetching %s %s…\n", p.ID, release.Version)
			f, err := store.FetchSubdir(ctx, entry.Repository, release.Ref, release.Subdir)
			if err != nil {
				return err
			}
			if err := f.VerifyArchiveChecksum(ctx, release.Checksum); err != nil {
				f.Discard()
				return fmt.Errorf("verify plugin %s@%s: %w", p.ID, release.Version, err)
			}
			if f.Manifest.ID != p.ID || strings.TrimPrefix(f.Manifest.Version, "v") != strings.TrimPrefix(release.Version, "v") || !strings.EqualFold(f.Commit, release.Commit) {
				f.Discard()
				return fmt.Errorf("registry release %s@%s does not match its manifest or pinned source revision", p.ID, release.Version)
			}
			f.Version, f.Checksum, f.Registry, f.Trust = release.Version, release.Checksum, true, plugin.TrustOf(entry, release)
			fmt.Fprintf(a.errOut, "Update %s %s → %s\n%s%s", p.ID, p.Version, release.Version, f.Preview(), trustPreview(f.Trust))
			if p.Manifest != nil {
				if change := plugin.ComparePermissions(p.Manifest, f.Manifest); !change.Empty() {
					fmt.Fprintf(a.errOut, "\nPermission changes:\n%s", change)
				}
			}
			if !a.confirm("Install this release?") {
				f.Discard()
				return fmt.Errorf("%w (run again with --yes to update)", plugin.ErrDeclined)
			}
			in, err := store.Update(ctx, f, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			reloadErr := a.reloadPlugins(ctx)
			if reloadErr == nil && in.Enabled {
				reloadErr = a.checkPluginReady(ctx, in.ID)
			}
			if reloadErr != nil {
				if _, rollbackErr := store.Rollback(in.ID); rollbackErr != nil {
					return fmt.Errorf("plugin %s@%s reload failed: %w; automatic rollback failed: %w", in.ID, in.Version, reloadErr, rollbackErr)
				}
				_ = a.reloadPlugins(ctx)
				return fmt.Errorf("plugin %s update was rolled back because the daemon could not load it: %w", in.ID, reloadErr)
			}
			updated = append(updated, in)
		}
		if len(updated) == 0 {
			return a.done(map[string]any{"updated": updated}, "no plugin updates available")
		}
		return a.done(map[string]any{"updated": updated}, "updated %d plugin(s)", len(updated))
	})}
}

func verifyInstalledRelease(installed plugin.Installed, listing plugin.Listing) error {
	if installed.ID != listing.ID || !installed.Registry || installed.Version == "" || installed.Commit == "" || installed.Checksum == "" {
		return fmt.Errorf("installed metadata for %s is incomplete or does not identify a registry release; reinstall it from the catalog", installed.ID)
	}
	for _, release := range listing.Releases {
		if release.Version == installed.Version && strings.EqualFold(release.Commit, installed.Commit) && strings.EqualFold(release.Checksum, installed.Checksum) {
			return nil
		}
	}
	return fmt.Errorf("installed release metadata for %s@%s does not match the registry; refusing to update", installed.ID, installed.Version)
}

func newPluginRollbackCmd(a *app) *cobra.Command {
	return &cobra.Command{Use: "rollback <id>", Short: "Restore the last known-good registry release", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		store := a.plugins()
		in, err := store.Rollback(args[0])
		if err != nil {
			return err
		}
		_ = a.reloadPlugins(cmd.Context())
		return a.done(map[string]any{"plugin": in}, "rolled back %s to %s", in.ID, in.Version)
	}}
}

func (a *app) installFetchedPlugin(ctx context.Context, store *plugin.Store, f *plugin.Fetched, listing *plugin.Listing) error {
	if listing != nil {
		fmt.Fprintf(a.errOut, "Catalog listing: %s (%s)\n  license: %s\n  repository: %s\n  pinned commit: %s\n%s", listing.Name, listing.ID, cmpStr(listing.License, "unspecified"), listing.Repository, f.Commit, trustPreview(f.Trust))
	}
	fmt.Fprint(a.errOut, f.Preview())
	if problem := plugin.Decide(f.Manifest, plugin.Approve(f.Manifest, nil), a.pluginPolicy()).Problem(f.Manifest.ID); strings.Contains(problem, "denied") {
		f.Discard()
		return fmt.Errorf("not installed: plugin %s %s", f.Manifest.ID, problem)
	}
	if !a.confirm("Install it?") {
		f.Discard()
		return fmt.Errorf("%w (run again with --yes to install it)", plugin.ErrDeclined)
	}
	in, err := store.Install(ctx, f, buildinfo.Get().Version)
	if err != nil {
		return err
	}
	_ = a.reloadPlugins(ctx)
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
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, latestVersion(p), strings.Join(p.Categories, ","), listingStatus(p), p.Description)
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
			in, m, err := a.plugins().Link(ctx, args[0], buildinfo.Get().Version)
			if err != nil {
				return err
			}
			fmt.Fprint(a.errOut, m.Preview(in.Source))
			_ = a.reloadPlugins(ctx)
			return a.done(map[string]any{"plugin": in}, "linked plugin %s to %s", in.ID, in.Source)
		}),
	}
}

// pluginRow is a plugin as `plugin list` shows it.
type pluginRow struct {
	plugin.Plugin
	Running   int    `json:"running"`
	LastError string `json:"last_error,omitempty"`
	// Health, Grant, Isolation and Runs are the daemon's view; Live says
	// the daemon runs it.
	Health    *plugin.Health   `json:"health,omitempty"`
	Grant     plugin.Grant     `json:"grant"`
	Isolation string           `json:"isolation,omitempty"`
	Sandbox   string           `json:"sandbox,omitempty"`
	Runs      []plugin.RunInfo `json:"runs,omitempty"`
	Live      bool             `json:"live"`
}

func (a *app) pluginRows(ctx context.Context) ([]pluginRow, error) {
	list, err := a.plugins().List(ctx, buildinfo.Get().Version)
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
		s, ok := live[p.ID]
		row := pluginRow{Plugin: p, Running: s.Running, LastError: s.LastError, Live: ok, Isolation: s.Isolation, Sandbox: s.Sandbox, Runs: s.Runs}
		if ok {
			row.Health, row.Grant = &s.Health, s.Grant
		} else if p.Manifest != nil {
			row.Grant = plugin.Decide(p.Manifest, p.Granted, a.pluginPolicy())
		}
		rows = append(rows, row)
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
				fmt.Fprintln(w, "ID\tVERSION\tTIER\tSTATE\tSOURCE")
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
					case r.Health != nil && r.Health.State == plugin.StateReady:
						state = "ready"
					case r.Health != nil:
						state = r.Health.State + suffixIf(r.LastError, ": "+r.LastError)
					case r.LastError != "":
						state = "error: " + r.LastError
					}
					src := r.Source
					if r.Linked {
						src += " (linked)"
					}
					if r.Revoked != nil {
						state = "REVOKED (" + r.Revoked.Reason + "); " + state
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID, version, cmpStr(r.Tier, "unchecked"), state, src)
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
					fmt.Fprint(a.out, a.trustLines(r.Trust, r.Sandbox))
					if r.Manifest != nil && !r.Manifest.Legacy() {
						fmt.Fprintf(a.out, "  holds: %s\n", cmpStr(strings.Join(r.Grant.Effective, ", "), "nothing"))
						if len(r.Grant.Ungranted) > 0 {
							fmt.Fprintf(a.out, "  may ask for: %s (`hive plugin grant %s …`)\n", strings.Join(r.Grant.Ungranted, ", "), r.ID)
						}
						if len(r.Grant.Denied) > 0 {
							fmt.Fprintf(a.out, "  denied by [plugins] deny: %s\n", strings.Join(r.Grant.Denied, ", "))
						}
					}
					if r.Problem != "" {
						fmt.Fprintf(a.out, "  problem: %s\n", r.Problem)
					}
					if h := r.Health; h != nil {
						fmt.Fprintf(a.out, "  health: %s since %s (isolation: %s)\n", h.State, h.Since.Format(time.DateTime), r.Isolation)
						fmt.Fprintf(a.out, "  events: %d delivered, %d failed, %d dropped; actions: %d run, %d failed, %d timed out\n",
							h.Events.Delivered, h.Events.Failed, h.Events.Dropped, h.Actions.Run, h.Actions.Failed, h.Actions.TimedOut)
						if h.Crashes+h.Restarts > 0 {
							fmt.Fprintf(a.out, "  restarts: %d; crashes in the window: %d\n", h.Restarts, h.Crashes)
						}
						for _, w := range h.Warnings {
							fmt.Fprintf(a.out, "  warning: %s\n", w)
						}
						for _, req := range h.Requests {
							fmt.Fprintf(a.out, "  asks for %s%s\n", req.Capability, suffixIf(req.Reason, ": "+req.Reason))
						}
					}
					for _, run := range r.Runs {
						fmt.Fprintf(a.out, "  running: %s (%s, since %s)\n", run.ID, run.Action, run.Started.Format(time.TimeOnly))
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
				fmt.Fprintf(a.out, "  latest: %s\n  license: %s\n  platforms: %s\n  Hive: %s\n  plugin API: %s\n", latestVersion(listing), cmpStr(listing.License, "unspecified"), cmpStr(strings.Join(listing.Platforms, ", "), "all"), hiveRange(listing.MinHiveVersion, listing.MaxHiveVersion), cmpStr(listing.API, "unspecified"))
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
				if listing.Publisher != "" {
					fmt.Fprintf(a.out, "  publisher: %s\n", listing.Publisher)
				}
				fmt.Fprintf(a.out, "  tier: %s (%s)\n", listing.Tier, plugin.TierMeaning(listing.Tier))
				fmt.Fprintln(a.out, "  releases:")
				for _, release := range listing.Releases {
					fmt.Fprintf(a.out, "    %s  %s  %s\n", release.Version, release.PublishedAt.Format(time.DateOnly), release.Commit[:12])
					if release.Revoked != nil {
						fmt.Fprintf(a.out, "      %s\n", a.red("revoked: "+revocationText(*release.Revoked)))
					}
					switch {
					case release.SignatureError != "":
						fmt.Fprintf(a.out, "      %s\n", a.red("signature: "+release.SignatureError))
					case release.SignedBy != "":
						fmt.Fprintf(a.out, "      signed by: %s\n", release.SignedBy)
					case catalog.Schema >= 2:
						fmt.Fprintln(a.out, "      not signed")
					}
					if release.Provenance != nil {
						fmt.Fprintf(a.out, "      built by: %s\n", release.Provenance)
					}
					for _, t := range release.Tested {
						fmt.Fprintf(a.out, "      tested with Hive %s: %s (%s)\n", t.Hive, t.Result, t.At.Format(time.DateOnly))
					}
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

// latestVersion is the newest installable release's version, as listings
// show it.
func latestVersion(p plugin.Listing) string {
	if r, ok := p.Latest(); ok {
		return r.Version
	}
	return "none (revoked)"
}

// revocationText is a revocation's reason and advisory, for messages.
func revocationText(r plugin.Revocation) string {
	return r.Reason + suffixIf(r.Advisory, "; see "+r.Advisory)
}

// warnListing says so when a plugin about to be installed is deprecated or
// archived: it installs, but its maintainers no longer stand behind it.
func (a *app) warnListing(p plugin.Listing) {
	switch {
	case p.Archived:
		fmt.Fprintf(a.errOut, "hive: %s is archived: it is no longer maintained and will get no fixes, security fixes included\n", p.ID)
	case p.Deprecated:
		fmt.Fprintf(a.errOut, "hive: %s is deprecated by its maintainers; look for a replacement (`hive plugin info %s`)\n", p.ID, p.ID)
	}
}

// checkReleaseSignature applies [plugins] require_signature to the release
// about to be installed, and says so when it installs unsigned.
func (a *app) checkReleaseSignature(catalog plugin.Registry, entry plugin.Listing, release plugin.Release) error {
	warning, err := plugin.CheckSignature(a.cfg.Plugins.RequireSignature, catalog.Schema, entry, release)
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintf(a.errOut, "hive: warning: %s\n", warning)
	}
	return nil
}

// trustPreview is a release's trust, for the install and update previews.
func trustPreview(t plugin.Trust) string {
	if t.Tier == "" {
		return ""
	}
	s := fmt.Sprintf("  tier: %s (%s)\n", t.Tier, plugin.TierMeaning(t.Tier))
	if t.Publisher != "" {
		s += fmt.Sprintf("  publisher: %s\n", t.Publisher)
	}
	s += fmt.Sprintf("  signed by: %s\n", cmpStr(t.SignedBy, "nobody (not signed)"))
	if t.Provenance != nil {
		s += fmt.Sprintf("  built by: %s\n", t.Provenance)
	}
	return s
}

// trustLines is an installed plugin's trust, for `plugin info`.
func (a *app) trustLines(t plugin.Trust, sandbox string) string {
	s := fmt.Sprintf("  tier: %s (%s)\n", cmpStr(t.Tier, "unchecked"), plugin.TierMeaning(t.Tier))
	if t.Publisher != "" {
		s += fmt.Sprintf("  publisher: %s\n", t.Publisher)
	}
	if t.Tier != plugin.TierUnlisted && t.Tier != "" {
		s += fmt.Sprintf("  signed by: %s\n", cmpStr(t.SignedBy, "nobody (not signed)"))
	}
	if t.Provenance != nil {
		s += fmt.Sprintf("  built by: %s\n", t.Provenance)
	}
	if sandbox != "" {
		s += fmt.Sprintf("  sandbox: %s\n", sandbox)
	}
	if t.Revoked != nil {
		s += "  " + a.red("revoked: "+revocationText(*t.Revoked)) + "\n"
	}
	if !t.CheckedAt.IsZero() {
		s += fmt.Sprintf("  checked against the registry: %s\n", t.CheckedAt.Local().Format(time.DateTime))
	}
	return s
}

// red marks what must not be missed, a revocation, when the output is a
// terminal that takes color.
func (a *app) red(s string) string {
	if !useColor(a.out, "auto") {
		return s
	}
	return "\x1b[31m" + s + "\x1b[0m"
}

// pluginAPIs lists the plugin APIs this hive runs.
func pluginAPIs() string {
	apis := make([]string, 0, len(plugin.SupportedAPIs))
	for _, v := range plugin.SupportedAPIs {
		apis = append(apis, strconv.Itoa(v))
	}
	return strings.Join(apis, ", ")
}

func listingStatus(p plugin.Listing) string {
	switch {
	case p.Archived:
		return "archived"
	case p.Deprecated:
		return "deprecated"
	case p.Tier != "":
		return p.Tier
	case p.Official:
		return plugin.TierOfficial
	case p.Verified:
		return plugin.TierVerified
	default:
		return plugin.TierCommunity
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
			_ = a.reloadPlugins(ctx)
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
			if _, err := store.Get(ctx, args[0], buildinfo.Get().Version); err != nil {
				return err
			}
			if deleteData && !a.confirm("Permanently delete this plugin's data too?") {
				return fmt.Errorf("plugin data kept; rerun with --delete-data --yes to delete it")
			}
			if _, err := store.SetEnabled(args[0], false); err != nil {
				return err
			}
			_ = a.reloadPlugins(ctx) // its commands stop before its files go
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
			if res.Async {
				return a.done(map[string]any{"run": res.Run, "async": true}, "started %s (`hive plugin cancel %s %s` stops it; `hive plugin logs %s` shows it)", res.Run, req.Plugin, res.Run, req.Plugin)
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
