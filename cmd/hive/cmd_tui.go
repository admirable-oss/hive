package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/config"
	"github.com/admirable-oss/hive/internal/tui/keymap"
	"github.com/admirable-oss/hive/internal/tui/mux"
	"github.com/admirable-oss/hive/internal/tui/theme"
)

func newUICmd(a *app) *cobra.Command {
	var env string
	cmd := needsDaemon(&cobra.Command{
		Use:     "ui",
		Aliases: []string{"tui"},
		Short:   "Open the multiplexer (the default command)",
		Long: `Open Hive's terminal multiplexer: tabs of split panes running your agents,
a sidebar of environments and agents, and an Overview of every agent.

Press the prefix key (ctrl+b unless configured) and then ? to list every key.
Detaching (prefix d) leaves every agent running. Key bindings, theme and
mouse are set in the [ui], [theme] and [keys] sections of the configuration
file (see ` + "`hive config default`" + `).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runUI(cmd.Context(), a, env) },
	})
	cmd.Flags().StringVar(&env, "env", "", "the environment to show first (default: the one for the current directory)")
	_ = cmd.RegisterFlagCompletionFunc("env", completeEnvironments(a))
	return cmd
}

func runTUI(ctx context.Context, a *app) error { return runUI(ctx, a, "") }

func runUI(ctx context.Context, a *app, env string) error {
	opts, warnings := uiOptions(a.cfg)
	if a.cfgErr != nil {
		opts.Warnings = append(opts.Warnings, fmt.Sprintf("config: %v (using defaults)", a.cfgErr))
	}
	for _, w := range append(a.warnings, warnings...) {
		opts.Warnings = append(opts.Warnings, "config: "+w)
	}
	opts.Env = env
	opts.Cwd, _ = os.Getwd()
	opts.Machine = a.remoteName
	opts.Machines = a.otherMachines()
	defer func() {
		for _, m := range opts.Machines {
			_ = m.Client.Close()
		}
	}()
	return mux.Run(ctx, a.client, opts)
}

// otherMachines are the machines the UI lists beside the one it shows:
// every enabled saved machine, and this one when another is shown.
func (a *app) otherMachines() []mux.Machine {
	var out []mux.Machine
	if a.remote != nil {
		out = append(out, mux.Machine{Name: "", Client: client.NewService(client.Config{SocketPath: a.socket, Name: "tui"})})
	}
	saved, err := a.machines().List()
	if err != nil {
		fmt.Fprintf(a.errOut, "hive: warning: %v\n", err)
	}
	for _, m := range saved {
		if m.Disabled || m.Name == a.remoteName {
			continue
		}
		out = append(out, mux.Machine{Name: m.Name, Client: a.remoteClient(a.target(m.Target("")), "tui")})
	}
	return out
}

// uiOptions builds the multiplexer's options from the configuration, with a
// warning for each binding, theme or colour it could not use (the UI then
// keeps the default for it).
func uiOptions(cfg config.Config) (mux.Options, []string) {
	modes := map[keymap.Mode]map[keymap.Action][]string{}
	for mode, bindings := range cfg.Keys.Modes {
		actions := map[keymap.Action][]string{}
		for name, keys := range bindings {
			act, err := keymap.ParseAction(name)
			if err != nil {
				act = keymap.Action(name) // keymap.New reports it as unknown
			}
			actions[act] = keys
		}
		modes[keymap.Mode(mode)] = actions
	}
	var bound []keymap.Command
	var commands []mux.Command
	for _, c := range cfg.Keys.Commands {
		title := c.Title
		if title == "" {
			title = c.Command
			if c.Type == "plugin_action" {
				title = c.Plugin + " " + c.Action
			}
		}
		bound = append(bound, keymap.Command{Mode: keymap.Mode(c.Mode), Keys: c.Keys, Title: title})
		commands = append(commands, mux.Command{Type: c.Type, Command: c.Command, Plugin: c.Plugin, Action: c.Action, Title: title})
	}
	km, warnings := keymap.New(keymap.Overrides{Prefix: cfg.Keys.Prefix, Modes: modes, Commands: bound})
	var status []mux.StatusSegment
	for _, s := range cfg.UI.StatusRight {
		status = append(status, mux.StatusSegment{Type: s.Type, Text: s.Text, Format: s.Format, Command: s.Command, Interval: s.Interval})
	}

	var custom *theme.Theme
	if len(cfg.Theme.Custom) > 0 {
		t, err := theme.NewCustom(cfg.Theme.Custom)
		if err != nil {
			warnings = append(warnings, err.Error())
		} else {
			custom = &t
		}
	}
	if _, err := theme.Resolve(cfg.Theme.Name, custom, true); err != nil {
		warnings = append(warnings, "theme.name: "+err.Error())
	}

	clip, _ := mux.ParseClipboard(cfg.UI.Clipboard) // checked when the config loaded
	return mux.Options{
		Keymap:        km,
		Theme:         cfg.Theme.Name,
		CustomTheme:   custom,
		HideSidebar:   !cfg.UI.Sidebar,
		SidebarWidth:  cfg.UI.SidebarWidth,
		NoMouse:       !cfg.UI.Mouse,
		Clipboard:     clip,
		ImageMaxBytes: cfg.Images.MaxKB << 10,
		Notify:        cfg.Notify,
		Commands:      commands,
		StatusRight:   status,
	}, warnings
}
