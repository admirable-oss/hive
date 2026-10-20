package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/plugin"
)

// pluginPolicy is the [plugins] policy as the plugin package takes it.
func (a *app) pluginPolicy() plugin.Policy {
	return plugin.Policy{Allow: a.cfg.Plugins.Allow, Deny: a.cfg.Plugins.Deny}
}

// reloadPlugin restarts one plugin in the daemon, when it runs.
func (a *app) reloadPlugin(ctx context.Context, id string) {
	if _, ok := a.daemonRunning(ctx); !ok {
		return
	}
	if _, err := client.NewPlugins(a.client).ReloadPlugin(ctx, id); err != nil {
		fmt.Fprintf(a.errOut, "hive: warning: the daemon did not reload plugin %s: %v\n", id, err)
	}
}

func newPluginReloadCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "reload [id]",
		Short: "Restart one plugin (or all), reading its manifest, grants and settings again",
		Long: `Reload stops a plugin's commands and starts it afresh: a plugin that failed
(it crashed past its restart limit, or its settings were invalid) gets another
try, with its crash count reset. Without an id every plugin restarts.`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			p := client.NewPlugins(a.client)
			var list []plugin.Status
			var err error
			if len(args) == 1 {
				list, err = p.ReloadPlugin(ctx, args[0])
			} else {
				list, err = p.Reload(ctx)
			}
			if err != nil {
				return err
			}
			return a.emit(list, func() error {
				for _, s := range list {
					fmt.Fprintf(a.out, "%s: %s%s\n", s.ID, s.Health.State, suffixIf(s.LastError, " ("+s.LastError+")"))
				}
				return nil
			})
		}),
	})
}

func suffixIf(cond, s string) string {
	if cond == "" {
		return ""
	}
	return s
}

func newPluginGrantCmd(a *app, grant bool) *cobra.Command {
	use, short := "grant <id> <capability>...", "Grant a plugin optional capabilities it asks for"
	if !grant {
		use, short = "revoke <id> <capability>...", "Take capabilities back from a plugin (a required one stops it)"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long: `Capabilities: ` + strings.Join(plugin.AllCapabilities(), ", ") + `.
hive.* are enforced by the daemon on the plugin's own socket; filesystem.write
and network only under strict isolation; process.exec is declared, not
enforced. ` + "`hive plugin info <id>`" + ` shows what a plugin asks for and holds.`,
		Args: minArgs(2),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id, caps := args[0], args[1:]
			store := a.plugins()
			p, err := store.Get(ctx, id, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			if p.Manifest == nil {
				return fmt.Errorf("plugin %s: %s", id, p.Problem)
			}
			if p.Manifest.Legacy() {
				return fmt.Errorf("plugin %s declares no permissions (no api = 1), so it already holds every capability; its author can add [permissions]", id)
			}
			declared := p.Manifest.Declared()
			granted := slices.Clone(p.Granted)
			for _, c := range caps {
				if !plugin.KnownCapability(c) {
					return &usageError{fmt.Errorf("unknown capability %q (have %s)", c, strings.Join(plugin.AllCapabilities(), ", "))}
				}
				switch {
				case grant && !slices.Contains(declared, c):
					return fmt.Errorf("plugin %s does not ask for %s; a plugin gets only what its manifest lists", id, c)
				case grant:
					granted = append(granted, c)
				default:
					granted = slices.DeleteFunc(granted, func(g string) bool { return g == c })
				}
			}
			in, err := store.SetGranted(id, granted)
			if err != nil {
				return err
			}
			a.reloadPlugin(ctx, id)
			g := plugin.Decide(p.Manifest, in.Granted, a.pluginPolicy())
			verb := "granted"
			if !grant {
				verb = "revoked"
			}
			msg := fmt.Sprintf("%s %s for %s; it holds %s", verb, strings.Join(caps, ", "), id, cmpStr(strings.Join(g.Effective, ", "), "nothing"))
			if problem := g.Problem(id); problem != "" {
				msg += "; it cannot run: " + problem
			}
			return a.done(map[string]any{"plugin": in, "grant": g}, "%s", msg)
		}),
	}
}

func newPluginCancelCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "cancel <id> <run>",
		Short: "Stop an action run in progress (`hive plugin info <id>` lists runs)",
		Args:  exactArgs(2),
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			if err := client.NewPlugins(a.client).Cancel(ctx, args[0], args[1]); err != nil {
				return err
			}
			return a.done(map[string]any{"plugin": args[0], "run": args[1]}, "canceled %s", args[1])
		}),
	})
}

func newPluginCapabilitiesCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Show the plugin API this hive runs: versions, capabilities, events and contributions",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			d := plugin.NewHost(plugin.HostConfig{Version: buildinfo.Get().Version}).Discovery("")
			if _, ok := a.daemonRunning(ctx); ok {
				if live, err := client.NewPlugins(a.client).Capabilities(ctx); err == nil {
					d = live
				}
			}
			return a.emit(d, func() error {
				fmt.Fprintf(a.out, "plugin API %d (runs %v), hive %s\n", d.API, d.SupportedAPIs, d.HiveVersion)
				fmt.Fprintf(a.out, "isolation: %s\n", strings.Join(d.Isolation, ", "))
				if d.Plugin != "" {
					fmt.Fprintf(a.out, "this plugin: %s, holding %s\n", d.Plugin, cmpStr(strings.Join(d.Granted, ", "), "nothing"))
				}
				fmt.Fprintln(a.out, "capabilities:")
				for _, c := range d.Capabilities {
					fmt.Fprintf(a.out, "  %-17s %s (enforced: %s)\n", c.Name, c.Description, c.Enforced)
				}
				fmt.Fprintf(a.out, "contributions: %s\n", strings.Join(d.Contributions, ", "))
				fmt.Fprintf(a.out, "events: %s\n", strings.Join(d.Events, ", "))
				return nil
			})
		}),
	}
}

func newPluginContributionsCmd(a *app) *cobra.Command {
	return needsDaemon(&cobra.Command{
		Use:   "contributions",
		Short: "List what plugins add to the UI, and which you turned off ([plugins] hidden)",
		Args:  noArgs,
		RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			c, err := client.NewPlugins(a.client).Contributions(ctx)
			if err != nil {
				return err
			}
			return a.emit(c, func() error {
				if len(c.Declared) == 0 {
					_, err := fmt.Fprintln(a.out, "no plugin contributes to the UI")
					return err
				}
				w := a.table()
				fmt.Fprintln(w, "KEY\tTITLE\tWHERE\tSTATE")
				for _, d := range c.Declared {
					where := d.Scope
					switch d.Kind {
					case plugin.ContribAction:
						where = strings.Join(d.Contexts, ", ")
					case plugin.ContribLink:
						where = d.URL
					case plugin.ContribStatus:
						where = "tab bar"
					case plugin.ContribPalette, plugin.ContribConfig:
						where = "palette"
					}
					state := "on"
					switch {
					case d.Hidden:
						state = "hidden"
					case !d.Available:
						state = "plugin not running"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Key, d.Title, where, state)
				}
				return w.Flush()
			})
		}),
	})
}

func newPluginConfigCmd(a *app) *cobra.Command {
	var set, unset []string
	cmd := &cobra.Command{
		Use:   "config <id>",
		Short: "Show or change a plugin's settings (checked against its schema)",
		Long: `Without flags, config shows each declared setting: its type, value and
description. --set name=value changes one (typed by the schema), --unset name
returns it to its default. The plugin restarts on its new settings.`,
		Example: `  hive plugin config pr-status
  hive plugin config pr-status --set interval=60 --set repo=me/app
  hive plugin config pr-status --unset interval`,
		Args: exactArgs(1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			id := args[0]
			store := a.plugins()
			view, err := store.Config(ctx, id, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			if len(set)+len(unset) > 0 {
				values := map[string]any{}
				for _, kv := range set {
					name, raw, ok := strings.Cut(kv, "=")
					if !ok {
						return &usageError{fmt.Errorf("--set %q: want name=value", kv)}
					}
					i := slices.IndexFunc(view.Settings, func(s plugin.Setting) bool { return s.Name == name })
					if i < 0 {
						return fmt.Errorf("%s has no setting %q", id, name)
					}
					v, err := plugin.ParseSetting(view.Settings[i].Type, raw)
					if err != nil {
						return fmt.Errorf("--set %s: %w", name, err)
					}
					values[name] = v
				}
				if view, err = store.SetConfig(ctx, id, buildinfo.Get().Version, values, unset); err != nil {
					return err
				}
				a.reloadPlugin(ctx, id)
			}
			return a.emit(view, func() error {
				if len(view.Settings) == 0 {
					fmt.Fprintf(a.out, "%s declares no settings\n", id)
				}
				w := a.table()
				fmt.Fprintln(w, "SETTING\tTYPE\tVALUE\tDESCRIPTION")
				for _, s := range view.Settings {
					v, ok := view.Values[s.Name]
					value := "(unset)"
					if ok {
						value = fmt.Sprint(v)
					}
					if s.Required {
						value += " *"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, s.Type, value, s.Description)
				}
				_ = w.Flush()
				for _, warn := range view.Warnings {
					fmt.Fprintf(a.out, "warning: %s\n", warn)
				}
				if view.Error != "" {
					fmt.Fprintf(a.out, "problem: %s\n", view.Error)
				}
				fmt.Fprintf(a.out, "file: %s\n", view.Path)
				return nil
			})
		}),
	}
	cmd.Flags().StringArrayVar(&set, "set", nil, "set a setting: name=value (repeatable)")
	cmd.Flags().StringArrayVar(&unset, "unset", nil, "return a setting to its default (repeatable)")
	return cmd
}

// doctorCheck is one finding of `hive plugin doctor`.
type doctorCheck struct {
	Plugin string `json:"plugin"`
	Level  string `json:"level"` // ok, warn, error
	Check  string `json:"check"`
	Detail string `json:"detail"`
}

func newPluginDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [id]",
		Short: "Check plugins for installation, compatibility, permission, settings and health problems",
		Args:  rangeArgs(0, 1),
		RunE: withTimeout(time.Minute, func(ctx context.Context, _ *cobra.Command, args []string) error {
			store := a.plugins()
			if err := store.Recover(); err != nil {
				return err
			}
			list, err := store.List(ctx, buildinfo.Get().Version)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				list = slices.DeleteFunc(list, func(p plugin.Plugin) bool { return p.ID != args[0] })
				if len(list) == 0 {
					return fmt.Errorf("%w %q", plugin.ErrNotFound, args[0])
				}
			}
			live := map[string]plugin.Status{}
			_, daemon := a.daemonRunning(ctx)
			if daemon {
				if st, err := client.NewPlugins(a.client).List(ctx); err == nil {
					for _, s := range st {
						live[s.ID] = s
					}
				}
			}
			// The registry's view of registry-installed plugins: revoked,
			// delisted, deprecated or archived releases.
			var catalog *plugin.Registry
			var catalogErr error
			if slices.ContainsFunc(list, func(p plugin.Plugin) bool { return p.Registry }) {
				c, err := a.pluginRegistry().Fetch(ctx)
				catalog, catalogErr = &c, err
			}
			var checks []doctorCheck
			for _, p := range list {
				checks = append(checks, a.doctorPlugin(ctx, store, p, live, daemon)...)
				if p.Registry {
					checks = append(checks, doctorRegistry(p, catalog, catalogErr))
				}
			}
			failed := slices.ContainsFunc(checks, func(c doctorCheck) bool { return c.Level == "error" })
			err = a.emit(checks, func() error {
				if len(list) == 0 {
					_, err := fmt.Fprintln(a.out, "no plugins installed")
					return err
				}
				current := ""
				for _, c := range checks {
					if c.Plugin != current {
						current = c.Plugin
						fmt.Fprintf(a.out, "%s\n", c.Plugin)
					}
					mark := map[string]string{"ok": "✓", "warn": "!", "error": "✗"}[c.Level]
					fmt.Fprintf(a.out, "  %s %s: %s\n", mark, c.Check, c.Detail)
				}
				return nil
			})
			if err == nil && failed {
				return &codedError{code: exitError, msg: "plugin doctor found problems"}
			}
			return err
		}),
	}
}

// doctorRegistry checks a registry-installed plugin's release against the
// registry: still listed, not revoked, still maintained.
func doctorRegistry(p plugin.Plugin, catalog *plugin.Registry, catalogErr error) doctorCheck {
	check := func(level, format string, args ...any) doctorCheck {
		return doctorCheck{Plugin: p.ID, Level: level, Check: "registry", Detail: fmt.Sprintf(format, args...)}
	}
	if catalogErr != nil {
		return check("warn", "cannot reach the registry, so whether %s is revoked is unknown: %v", p.Version, catalogErr)
	}
	if r, ok := catalog.Revocation(p.ID, p.Version); ok {
		return check("error", "%s is revoked: %s; update (`hive plugin update %s`) or uninstall it", p.Version, revocationText(r), p.ID)
	}
	listing, ok := catalog.Get(p.ID)
	switch {
	case !ok:
		return check("warn", "no longer listed in the registry: no updates or fixes will come")
	case listing.Archived:
		return check("warn", "archived: no longer maintained, and no fixes will come")
	case listing.Deprecated:
		return check("warn", "deprecated by its maintainers; look for a replacement")
	}
	if latest, ok := listing.Latest(); ok && latest.Version != p.Version {
		return check("ok", "listed; %s installed, %s available (`hive plugin update %s`)", p.Version, latest.Version, p.ID)
	}
	return check("ok", "listed; %s is current", p.Version)
}

func (a *app) doctorPlugin(ctx context.Context, store *plugin.Store, p plugin.Plugin, live map[string]plugin.Status, daemon bool) []doctorCheck {
	var out []doctorCheck
	add := func(level, check, format string, args ...any) {
		out = append(out, doctorCheck{Plugin: p.ID, Level: level, Check: check, Detail: fmt.Sprintf(format, args...)})
	}
	if p.Manifest == nil || p.Problem != "" {
		add("error", "manifest", "%s", cmpStr(p.Problem, "no manifest"))
		if strings.Contains(p.Problem, plugin.ErrUnsupportedAPI.Error()) {
			add("error", "api", "this hive runs plugin API %d; update hive or install an older release", plugin.APIVersion)
		}
		return out
	}
	m := p.Manifest
	add("ok", "manifest", "%s %s, plugin API %d", m.ID, m.Version, m.EffectiveAPI())
	if err := m.Supported(ctx, buildinfo.Get().Version); err != nil {
		add("error", "compatibility", "%v", err)
	} else {
		add("ok", "compatibility", "runs on this system and hive %s", buildinfo.Get().Version)
	}
	if m.Legacy() {
		add("warn", "permissions", "declares no plugin API or permissions, so it holds every capability")
	}
	g := plugin.Decide(m, p.Granted, a.pluginPolicy())
	if problem := g.Problem(p.ID); problem != "" {
		add("error", "permissions", "%s", problem)
	} else if !m.Legacy() {
		add("ok", "permissions", "holds %s", cmpStr(strings.Join(g.Effective, ", "), "nothing"))
	}
	if len(g.Ungranted) > 0 {
		add("warn", "permissions", "may ask for %s (not granted: `hive plugin grant %s …`)", strings.Join(g.Ungranted, ", "), p.ID)
	}
	forced := a.cfg.Plugins.Isolation == plugin.IsolationStrict
	strict := forced || (a.cfg.Plugins.Isolation != plugin.IsolationStandard && m.Isolation == "strict" && plugin.SandboxAvailable())
	switch {
	case forced && !plugin.SandboxAvailable():
		add("error", "isolation", "[plugins] isolation is strict, but this system has no sandbox tool (sandbox-exec on macOS, bwrap on Linux)")
	case strict:
		add("ok", "isolation", "runs sandboxed (strict)")
	default:
		add("warn", "isolation", "runs as you, unsandboxed: declared filesystem.write, network and process.exec are not enforced")
	}
	for _, e := range m.Events {
		if !plugin.KnownEvent(e.On) {
			add("warn", "events", "handles %q, which this hive never publishes", e.On)
		}
	}
	if view, err := store.Config(ctx, p.ID, buildinfo.Get().Version); err == nil {
		switch {
		case view.Error != "":
			add("error", "settings", "%s (`hive plugin config %s`)", view.Error, p.ID)
		case len(view.Settings) > 0:
			add("ok", "settings", "%d setting(s) valid", len(view.Settings))
		}
		for _, w := range view.Warnings {
			add("warn", "settings", "%s", w)
		}
	}
	switch {
	case !p.Enabled:
		add("warn", "state", "disabled (`hive plugin enable %s`)", p.ID)
	case !daemon:
		add("warn", "state", "the daemon is not running, so its health is unknown")
	default:
		st, ok := live[p.ID]
		if !ok {
			add("error", "state", "enabled but not loaded by the daemon (`hive plugin reload %s`)", p.ID)
			break
		}
		h := st.Health
		switch h.State {
		case plugin.StateReady:
			add("ok", "state", "ready since %s", h.Since.Format(time.TimeOnly))
		case plugin.StateStarting:
			add("warn", "state", "still starting (since %s)", h.LoadedAt.Format(time.TimeOnly))
		case plugin.StateDegraded:
			add("warn", "state", "degraded: %s", h.LastError)
		default:
			add("error", "state", "%s: %s", h.State, cmpStr(h.LastError, st.LastError))
		}
		if h.Crashes > 0 {
			add("warn", "crashes", "%d crash(es) in the restart window, %d restart(s) since loading", h.Crashes, h.Restarts)
		}
		if h.Events.Dropped > 0 {
			add("warn", "events", "%d event(s) dropped: its handlers are too slow (event_concurrency, or a shorter handler)", h.Events.Dropped)
		}
		if h.Events.Failed+h.Actions.Failed+h.Actions.TimedOut > 0 {
			add("warn", "failures", "%d event command(s) and %d action(s) failed, %d action(s) timed out", h.Events.Failed, h.Actions.Failed, h.Actions.TimedOut)
		}
		for _, r := range h.Requests {
			add("warn", "requests", "asks for %s%s (`hive plugin grant %s %s`)", r.Capability, suffixIf(r.Reason, ": "+r.Reason), p.ID, r.Capability)
		}
		for _, w := range h.Warnings {
			if !strings.Contains(w, "never publishes") && !strings.Contains(w, "no plugin API") && !strings.Contains(w, "no longer declared") {
				add("warn", "health", "%s", w)
			}
		}
	}
	add("ok", "logs", "`hive plugin logs %s`", p.ID)
	return out
}
