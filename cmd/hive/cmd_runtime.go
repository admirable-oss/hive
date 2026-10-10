package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/protocol"
)

func newStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runtime status (exit code 3 when the daemon is not running)",
		Args:  noArgs,
		RunE:  withTimeout(5*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error { return showStatus(ctx, a) }),
	}
}

func showStatus(ctx context.Context, a *app) error {
	st, ok := a.daemonRunning(ctx)
	if !ok {
		_ = a.emit(map[string]string{"status": "not running", "socket": a.socket}, func() error {
			fmt.Fprintln(a.out, "hive daemon: not running")
			return nil
		})
		return &codedError{code: exitNotRunning}
	}
	return a.emit(st, func() error {
		w := a.table()
		fmt.Fprintf(w, "Status\t%s\n", st.Status)
		if st.PID != 0 {
			fmt.Fprintf(w, "PID\t%d\n", st.PID)
		}
		fmt.Fprintf(w, "Version\t%s\n", daemonVersion(st))
		fmt.Fprintf(w, "Socket\t%s\n", st.Socket)
		fmt.Fprintf(w, "Started\t%s (%s ago)\n", formatTime(st.StartedAt), time.Since(st.StartedAt).Round(time.Second))
		fmt.Fprintf(w, "Logs\t%s\n", a.daemonLog())
		if st.AgentsSurviveRestart {
			fmt.Fprintf(w, "Agents\tkeep running across daemon restarts\n")
		}
		if cli := buildinfo.Get().Version; st.Version != cli {
			fmt.Fprintf(w, "Note\tthis CLI is %s; run `hive daemon restart` to match\n", cli)
		}
		return w.Flush()
	})
}

func newPingCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Check that the daemon answers (exit code 3 when it does not)",
		Args:  noArgs,
		RunE: withTimeout(5*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			start := time.Now()
			if err := a.client.Ping(ctx); err != nil {
				return &codedError{code: exitNotRunning, msg: err.Error()}
			}
			rtt := time.Since(start).Round(time.Microsecond)
			return a.done(map[string]any{"rtt_us": rtt.Microseconds()}, "pong (%s)", rtt)
		}),
	}
}

func newStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon and every agent it runs",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return stopDaemon(cmd.Context(), a, true) },
	}
}

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI and daemon versions",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			ctx, cancel := context.WithTimeout(cmd.Context(), 500*time.Millisecond)
			defer cancel()
			st, running := a.daemonRunning(ctx)
			out := struct {
				buildinfo.Info
				Protocol string         `json:"protocol_version"`
				Daemon   *client.Status `json:"daemon,omitempty"`
			}{Info: info, Protocol: protocol.Version}
			if running {
				out.Daemon = &st
			}
			return a.emit(out, func() error {
				fmt.Fprintln(a.out, info.String())
				if running {
					fmt.Fprintf(a.out, "daemon %s", daemonVersion(st))
					if st.PID != 0 {
						fmt.Fprintf(a.out, " (pid %d)", st.PID)
					}
					fmt.Fprintln(a.out)
				}
				return nil
			})
		},
	}
}

// daemonVersion names the daemon's build. Daemons older than v0.2 do not
// report one.
func daemonVersion(st client.Status) string {
	if st.Version == "" {
		return "unknown (older than v0.2; run `hive daemon restart`)"
	}
	return st.Version
}
