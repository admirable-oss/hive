package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/session"
)

func newSessionCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sessions (separate daemons with their own environments)",
		Long: `A session is a separate Hive: its own daemon, socket, environments and agents.
Commands use the session named by --session, else $HIVE_SESSION, else
"default". Agents see their session in HIVE_SESSION, so hive commands they
run act on it. A named session starts on first use.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List sessions and whether their daemon runs",
			Args:    noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				infos, err := session.List(a.base)
				if err != nil {
					return err
				}
				type row struct {
					session.Info
					Current bool `json:"current"`
					Running bool `json:"running"`
					PID     int  `json:"pid,omitempty"`
				}
				rows := make([]row, 0, len(infos))
				for _, info := range infos {
					r := row{Info: info, Current: info.Name == a.session}
					b, err := a.forSession(info.Name)
					if err != nil {
						return err
					}
					ctx, cancel := context.WithTimeout(cmd.Context(), time.Second)
					if st, ok := b.daemonRunning(ctx); ok {
						r.Running, r.PID = true, st.PID
					}
					cancel()
					_ = b.client.Close()
					rows = append(rows, r)
				}
				return a.emit(rows, func() error {
					w := a.table()
					fmt.Fprintln(w, "\tNAME\tDAEMON\tROOT")
					for _, r := range rows {
						mark, state := "", "stopped"
						if r.Current {
							mark = "*"
						}
						if r.Running {
							state = fmt.Sprintf("running (pid %d)", r.PID)
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", mark, r.Name, state, r.Root)
					}
					return w.Flush()
				})
			},
		},
		newSessionStopCmd(a),
		newSessionRemoveCmd(a),
		needsDaemon(&cobra.Command{
			Use:   "save",
			Short: "Save a snapshot of the workspace now (it is also saved on every change)",
			Long: `Save the session's workspace to session.json now: environments, tabs,
layout, focus, and each pane's command, working directory and agent session.
The daemon also saves it after every change, on a timer (session.snapshot_interval)
and when it stops; this is for scripts and before a planned reboot.`,
			Args: noArgs,
			RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				res, err := client.NewSessions(a.client).Save(ctx)
				if err != nil {
					return err
				}
				what := "saved"
				if !res.Written {
					what = "unchanged since the last snapshot"
				}
				return a.emit(res, func() error {
					_, err := fmt.Fprintf(a.out, "%s: %s\n", res.Path, what)
					return err
				})
			}),
		}),
		newSessionRestoreCmd(a),
	)
	return cmd
}

func newSessionRestoreCmd(a *app) *cobra.Command {
	var fresh bool
	cmd := needsDaemon(&cobra.Command{
		Use:   "restore",
		Short: "Start panes whose processes were lost (a reboot, a killed shim) again",
		Long: `Start every pane whose process went down with the host or its shim again, in
place, where it was working. Agents resume their conversations (see [resume]
in their manifests) unless --fresh is given. The daemon does this by itself
when it starts, unless session.restore is off in the config.`,
		Args: noArgs,
		RunE: withTimeout(5*time.Minute, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			var resume *bool
			if fresh {
				resume = new(bool)
			}
			report, err := client.NewSessions(a.client).Restore(ctx, resume)
			if err != nil {
				return err
			}
			return a.emit(report, func() error {
				if len(report.Panes) == 0 {
					_, err := fmt.Fprintln(a.out, "nothing to restore: no pane lost its process")
					return err
				}
				w := a.table()
				fmt.Fprintln(w, "PANE\tENV\tAGENT\tRESUMED\tCOMMAND\tCWD")
				for _, p := range report.Panes {
					command := strings.Join(p.Command, " ")
					if p.Typed != "" {
						command += " → " + p.Typed
					}
					if p.Error != "" {
						command = "error: " + p.Error
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Pane, p.Environment, cmpStr(p.Agent, "-"), cmpStr(p.Resumed, "-"), command, cmpStr(p.Cwd, "-"))
				}
				return w.Flush()
			})
		}),
	})
	cmd.Flags().BoolVar(&fresh, "fresh", false, "start agents fresh instead of resuming their conversations")
	return cmd
}

func newSessionStopCmd(a *app) *cobra.Command {
	var keepAgents bool
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a session's daemon and its agents",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := a.forSession(args[0])
			if err != nil {
				return &usageError{err}
			}
			return stopDaemon(cmd.Context(), b, !keepAgents)
		},
	}
	cmd.Flags().BoolVar(&keepAgents, "keep-agents", false, "leave agents running for the session's next daemon")
	return cmd
}

func newSessionRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Stop a session's agents and delete everything it stored",
		Long: `Stop a session's agents and delete its directory: its environments,
logs and managed workspaces. Directories of yours that environments ran in
are never touched. The default session cannot be removed.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := session.Normalize(args[0])
			if name == session.Default {
				return &usageError{errors.New("the default session cannot be removed")}
			}
			b, err := a.forSession(name)
			if err != nil {
				return &usageError{err}
			}
			if _, err := os.Stat(b.root); errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("no session %q", name)
			}
			// Agents may outlive a stopped daemon (under shims); a daemon
			// adopts them so they can be stopped before their files go.
			if hasRunningShims(b.root) {
				ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
				err := b.startDaemon(ctx)
				cancel()
				if err != nil {
					return fmt.Errorf("start the session's daemon to stop its agents: %w", err)
				}
			}
			if err := stopDaemon(cmd.Context(), b, true); err != nil {
				return err
			}
			if err := os.RemoveAll(b.root); err != nil {
				return err
			}
			return a.done(map[string]any{"session": name}, "removed session %q", name)
		},
	}
}

// forSession is a copy of a talking to another session.
func (a *app) forSession(name string) (*app, error) {
	b := *a
	b.client = nil
	if err := b.useSession(name); err != nil {
		return nil, err
	}
	return &b, nil
}

// hasRunningShims reports whether root has agent shims left (their run
// directories are removed when an agent is reaped).
func hasRunningShims(root string) bool {
	entries, err := os.ReadDir(filepath.Join(root, "run"))
	return err == nil && len(entries) > 0
}
