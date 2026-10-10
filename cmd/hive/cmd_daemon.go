package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/daemonctl"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/runtime"
	"github.com/admirable-oss/hive/internal/terminal"
)

func newDaemonCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"server"},
		Short:   "Run the Hive runtime in the foreground, or manage it",
		Long: `Without a subcommand, run the runtime in the foreground until Ctrl+C,
SIGTERM or ` + "`hive stop`" + `. This is what service managers run.

Other commands start the daemon automatically when it is not running (see
daemon.autostart in ` + "`hive config`" + `). To start it at login and restart
it after a crash, use ` + "`hive daemon install`" + `.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runDaemon(cmd.Context(), a) },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start the daemon in the background",
			Args:  noArgs,
			RunE: withTimeout(15*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				if st, ok := a.daemonRunning(ctx); ok {
					fmt.Fprintf(a.out, "hive daemon is already running (pid %d)\n", st.PID)
					return nil
				}
				if err := a.startDaemon(ctx); err != nil {
					return err
				}
				st, _ := a.daemonRunning(ctx)
				fmt.Fprintf(a.out, "hive daemon started (pid %d, logs: %s)\n", st.PID, a.daemonLog())
				return nil
			}),
		},
		newDaemonStopCmd(a),
		newDaemonRestartCmd(a),
		&cobra.Command{
			Use:   "status",
			Short: "Show whether the daemon is running",
			Args:  noArgs,
			RunE:  withTimeout(5*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error { return showStatus(ctx, a) }),
		},
		newDaemonLogsCmd(a),
		newReloadManifestsCmd(a),
		newDaemonInstallCmd(a),
		&cobra.Command{
			Use:   "uninstall",
			Short: "Remove the login service installed by `hive daemon install`",
			Args:  noArgs,
			RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
				path, err := daemonctl.NewManager(a.home).Uninstall(ctx)
				if err != nil {
					return err
				}
				fmt.Fprintf(a.out, "removed %s\n", path)
				return nil
			}),
		},
	)
	return cmd
}

func newDaemonStopCmd(a *app) *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon and every agent it runs (--keep-agents to leave them running)",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return stopDaemon(cmd.Context(), a, !keep) },
	}
	cmd.Flags().BoolVar(&keep, "keep-agents", false, "leave agents running; the next daemon re-attaches to them")
	return cmd
}

func newDaemonRestartCmd(a *app) *cobra.Command {
	var stopAgents bool
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon; agents keep running and are re-attached",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := stopDaemon(cmd.Context(), a, stopAgents); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			if err := a.startDaemon(ctx); err != nil {
				return err
			}
			st, _ := a.daemonRunning(ctx)
			fmt.Fprintf(a.out, "hive daemon started (pid %d)\n", st.PID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&stopAgents, "stop-agents", false, "stop every agent too")
	return cmd
}

// runDaemon is `hive daemon`: the runtime in the foreground.
func runDaemon(ctx context.Context, a *app) error {
	if a.cfgErr != nil {
		// Running with defaults the user did not choose would be surprising;
		// clients only warn, but the daemon refuses.
		return a.cfgErr
	}
	interactive := term.IsTerminal(os.Stderr.Fd())
	log, closeLog, err := logging.New(a.cfg.LoggingConfig(a.daemonLog(), interactive))
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = closeLog() }()
	for _, w := range a.warnings {
		log.Warn("config warning", "path", a.configPath, "warning", w)
	}

	// SIGHUP arrives when the terminal that started `hive daemon &` closes.
	// Keeping agents alive through that is the point of Hive, so it is
	// logged and ignored. (Notify, unlike signal.Ignore, is not inherited by
	// the agents, which keep the default SIGHUP behaviour.)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the hive binary: %w", err)
	}
	t := a.cfg.Terminal
	scrollback := t.ScrollbackMB << 20
	if scrollback == 0 {
		scrollback = -1 // none
	}
	mod := runtime.NewModule(runtime.Config{
		SocketPath: a.socket,
		BaseDir:    a.root,
		Logger:     log,
		StopGrace:  a.cfg.Process.StopGrace,
		Terminal: terminal.Config{
			DefaultSize:     terminal.Size{Width: uint16(t.DefaultWidth), Height: uint16(t.DefaultHeight)},
			ScrollbackBytes: scrollback,
			StopGrace:       a.cfg.Process.StopGrace,
		},
		// Every agent runs under its own shim (this binary), so agents
		// keep running when the daemon stops, crashes or is upgraded.
		Shim:        &runtime.ShimConfig{Exe: exe, Args: []string{shimCommand}},
		Session:     a.session,
		Home:        a.base,
		Bin:         exe,
		Shell:       a.cfg.ShellArgv(),
		WorktreeDir: a.cfg.WorktreeDir(a.home, a.base),
		GitInterval: a.cfg.Git.RefreshInterval,

		AgentManifestDir: a.agentManifestDir(),
	})
	if err := mod.Service.Start(ctx); err != nil {
		if errors.Is(err, runtime.ErrAlreadyRunning) {
			return fmt.Errorf("a hive daemon is already running for session %q (%s)", a.session, a.root)
		}
		log.Error("daemon failed to start", "err", err)
		return err
	}
	if interactive {
		fmt.Fprintf(a.errOut, "hive daemon running on %s (logs: %s).\nPress Ctrl+C to stop the daemon; agents keep running (`hive stop` stops them too).\n", a.socket, a.daemonLog())
	}

	for running := true; running; {
		select {
		case <-ctx.Done():
			log.Info("received stop signal")
			running = false
		case <-mod.Service.Done(): // stopped remotely
			running = false
		case <-hup:
			log.Info("ignoring SIGHUP; agents keep running")
		}
	}

	// ctx is already cancelled when a signal arrived; shutdown gets its own
	// budget. Agents keep running; `hive stop` (runtime.shutdown) is what
	// stops them.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.Daemon.ShutdownTimeout)
	defer cancel()
	err = mod.Service.Stop(stopCtx)
	if interactive {
		fmt.Fprintln(a.errOut, "hive daemon stopped; agents keep running")
	}
	return err
}

func stopDaemon(ctx context.Context, a *app, stopAgents bool) error {
	if _, ok := a.daemonRunning(ctx); !ok {
		fmt.Fprintln(a.out, "hive daemon is not running")
		return nil
	}
	if err := a.shutdownDaemon(ctx, stopAgents); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "hive daemon stopped")
	return nil
}

// shutdownDaemon stops the running daemon and waits until it is gone.
func (a *app) shutdownDaemon(ctx context.Context, stopAgents bool) error {
	// Agents get the configured grace to exit, plus slack for the reply.
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Daemon.ShutdownTimeout+5*time.Second)
	defer cancel()
	if err := a.client.Shutdown(ctx, stopAgents); err != nil {
		return err
	}
	_ = a.client.Close()
	// The reply comes before the process exits; wait until the socket is gone
	// so a following start cannot race the old daemon.
	for {
		if _, ok := a.daemonRunning(ctx); !ok {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

func newDaemonLogsCmd(a *app) *cobra.Command {
	var (
		lines  int
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the daemon's log",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tailLocalFile(cmd.Context(), a.daemonLog(), lines, follow, a.out)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of lines to show (0 for all)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new log lines")
	return cmd
}

// tailLocalFile prints the last n lines of path and optionally follows it.
func tailLocalFile(ctx context.Context, path string, n int, follow bool, out io.Writer) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no daemon log yet at %s", path)
	}
	if err != nil {
		return err
	}
	text := string(data)
	if n > 0 {
		lines := strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		text = strings.Join(lines[max(0, len(lines)-n):], "")
	}
	fmt.Fprint(out, text)
	if !follow {
		return nil
	}
	pos := int64(len(data))
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
		f, err := os.Open(path)
		if err != nil {
			continue // rotated; the new file appears shortly
		}
		info, err := f.Stat()
		if err == nil && info.Size() < pos {
			pos = 0 // rotated or truncated
		}
		if _, err := f.Seek(pos, io.SeekStart); err == nil {
			n, _ := io.Copy(out, f)
			pos += n
		}
		_ = f.Close()
	}
}

func newDaemonInstallCmd(a *app) *cobra.Command {
	var (
		printOnly  bool
		executable string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Start the daemon at login and restart it if it crashes",
		Long: `Install the daemon as a per-user service: a launchd agent on macOS or a
systemd user service on Linux. Your current PATH is recorded in the service so
agents find the same tools they do in your shell; re-run install after
changing it.`,
		Args: noArgs,
		RunE: withTimeout(30*time.Second, func(ctx context.Context, _ *cobra.Command, _ []string) error {
			if executable == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("locate the hive binary: %w", err)
				}
				executable = exe
			}
			env := map[string]string{"PATH": a.getenv("PATH")}
			for _, k := range []string{"HIVE_HOME", "HIVE_CONFIG", "HOME", "LANG"} {
				if v := a.getenv(k); v != "" {
					env[k] = v
				}
			}
			spec := daemonctl.UnitSpec{Executable: executable, Env: env, LogDir: a.logDir}
			m := daemonctl.NewManager(a.home)
			if printOnly {
				data, err := m.Render(spec)
				if err != nil {
					return err
				}
				_, err = a.out.Write(data)
				return err
			}
			if st, ok := a.daemonRunning(ctx); ok {
				return fmt.Errorf("a daemon is already running (pid %d) outside the service manager; "+
					"stop it with `hive stop` first (this stops its agents), then install", st.PID)
			}
			path, err := m.Install(ctx, spec)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "installed %s\nthe daemon now starts at login; remove it with `hive daemon uninstall`\n", path)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the service definition instead of installing it")
	cmd.Flags().StringVar(&executable, "executable", "", "path of the hive binary the service runs (default: this binary)")
	return cmd
}
