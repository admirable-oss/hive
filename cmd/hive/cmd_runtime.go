package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/update"
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
	var verify bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the CLI and daemon versions",
		Long: `Print the CLI and daemon versions.

--verify downloads the signed release this hive claims to be, checks its
signature and checksum as ` + "`hive update`" + ` does, and compares the binary in it
with this one byte for byte. A hive built from source, or by Nix, is not the
release binary and says so.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			if verify {
				return a.verifyBuild(cmd.Context(), info)
			}
			wait := 500 * time.Millisecond
			if a.remote != nil {
				wait = 15 * time.Second // an ssh connection first
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), wait)
			defer cancel()
			st, running := a.daemonRunning(ctx)
			out := struct {
				buildinfo.Info
				Protocol string         `json:"protocol_version"`
				APILevel int            `json:"api_level"`
				Daemon   *client.Status `json:"daemon,omitempty"`
			}{Info: info, Protocol: protocol.Version, APILevel: protocol.APILevel}
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
	cmd.Flags().BoolVar(&verify, "verify", false, "check that this binary is the signed release it claims to be")
	return cmd
}

// verifyBuild compares the running binary with its signed release.
func (a *app) verifyBuild(ctx context.Context, info buildinfo.Info) error {
	k := buildinfo.ReleaseKey()
	if k == "" {
		return fmt.Errorf("hive %s was built from source (it carries no release key): there is no signed release to compare it with", info.Version)
	}
	key, err := update.ParseKey(k)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fmt.Errorf("locate the hive binary: %w", err)
	}
	bin, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	v, err := update.Source{PublicKey: key}.VerifyBinary(ctx, update.ReleaseTag(info.Version), goruntime.GOOS, goruntime.GOARCH, bin)
	if err != nil {
		return fmt.Errorf("verify against release %s: %w", update.ReleaseTag(info.Version), err)
	}
	out := struct {
		update.Verification
		Path string `json:"path"`
	}{v, exe}
	if err := a.emit(out, func() error {
		if v.Identical {
			_, err := fmt.Fprintf(a.out, "%s is hive %s, byte for byte the signed release (%s, sha256 %s)\n", exe, v.Tag, v.Archive, v.SHA256)
			return err
		}
		_, err := fmt.Fprintf(a.out, "%s is not the binary of the signed release %s: sha256 %s, the release's is %s (built from source, by Nix, or altered)\n", exe, v.Tag, v.SHA256, v.ReleaseSHA256)
		return err
	}); err != nil {
		return err
	}
	if !v.Identical {
		return &codedError{code: exitError, msg: "this binary is not the signed release"}
	}
	return nil
}

// daemonVersion names the daemon's build. Daemons older than v0.2 do not
// report one.
func daemonVersion(st client.Status) string {
	if st.Version == "" {
		return "unknown (older than v0.2; run `hive daemon restart`)"
	}
	return st.Version
}
