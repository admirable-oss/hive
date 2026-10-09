package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/vt"
)

// detachKey (Ctrl+]) ends an attach session, as in telnet. Every other key,
// Ctrl+C included, goes to the agent.
const detachKey = 0x1d

func newTerminalCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terminal",
		Short: "Attach to, type into and resize agent terminals",
	}
	cmd.AddCommand(
		needsDaemon(&cobra.Command{
			Use:               "attach <process-id>",
			Short:             "Attach to an agent's terminal (Ctrl+] detaches)",
			Args:              exactArgs(1),
			ValidArgsFunction: completeProcesses(a, true),
			RunE:              func(cmd *cobra.Command, args []string) error { return termAttach(cmd.Context(), a, args[0]) },
		}),
		newTerminalSnapshotCmd(a),
		needsDaemon(&cobra.Command{
			Use:               "input <process-id> <text>",
			Short:             "Type text into an agent's terminal",
			Args:              exactArgs(2),
			ValidArgsFunction: completeProcesses(a, true),
			RunE: withTimeout(5*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				return a.client.TerminalInput(ctx, args[0], []byte(args[1]))
			}),
		}),
		needsDaemon(&cobra.Command{
			Use:               "resize <process-id> <width> <height>",
			Short:             "Resize an agent's terminal",
			Args:              resizeArgs,
			ValidArgsFunction: completeProcesses(a, true),
			RunE: withTimeout(5*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
				w, _ := parseDimension(args[1])
				h, _ := parseDimension(args[2])
				return a.client.TerminalResize(ctx, args[0], w, h)
			}),
		}),
	)
	return cmd
}

// resizeArgs validates `resize <id> <width> <height>` before anything runs.
func resizeArgs(cmd *cobra.Command, args []string) error {
	if err := exactArgs(3)(cmd, args); err != nil {
		return err
	}
	for i, name := range []string{"width", "height"} {
		if _, err := parseDimension(args[i+1]); err != nil {
			return &usageError{fmt.Errorf("invalid %s %q: want 1..65535", name, args[i+1])}
		}
	}
	return nil
}

func parseDimension(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err == nil && n == 0 {
		err = strconv.ErrRange
	}
	return uint16(n), err
}

func termAttach(ctx context.Context, a *app, id string) error {
	in, out := os.Stdin.Fd(), os.Stdout.Fd()
	interactive := term.IsTerminal(in) && term.IsTerminal(out)
	req := client.ViewRequest{ProcessID: id}
	if interactive {
		if w, h, err := term.GetSize(out); err == nil && w > 0 && h > 0 {
			req.Width, req.Height = uint16(w), uint16(h)
		}
	}
	att, err := a.client.TerminalAttach(ctx, req)
	if err != nil {
		return err
	}
	defer att.Close()

	if interactive {
		state, err := term.MakeRaw(in)
		if err != nil {
			return fmt.Errorf("switch terminal to raw mode: %w", err)
		}
		// The daemon paints the agent's screen; draw it on the alternate
		// screen so detaching restores the shell exactly as it was.
		fmt.Fprint(a.out, "\x1b[?1049h")
		defer func() {
			fmt.Fprint(a.out, vt.ResetSequence()+"\x1b[?1049l")
			_ = term.Restore(in, state)
		}()
		wctx, stop := context.WithCancel(ctx)
		defer stop()
		go followWindowSize(wctx, att, out)
	}

	detached := make(chan struct{})
	go func() {
		// Keystrokes go to the agent until the detach key.
		_, _ = io.Copy(att, detachReader{os.Stdin})
		close(detached)
		_ = att.Close()
	}()
	_, err = io.Copy(a.out, att)
	select {
	case <-detached:
		defer fmt.Fprintf(a.errOut, "detached from %s; it keeps running\n", id)
		return nil
	default:
		if err != nil && ctx.Err() == nil {
			return err
		}
		defer fmt.Fprintf(a.errOut, "%s has exited\n", id)
		return nil
	}
}

// followWindowSize keeps the view's size in step with this terminal.
func followWindowSize(ctx context.Context, att *client.Attachment, fd uintptr) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
			if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
				_ = att.Resize(ctx, uint16(w), uint16(h))
			}
		}
	}
}

// detachReader passes input through until the detach key, then reports EOF.
type detachReader struct{ r io.Reader }

func (d detachReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if i := bytes.IndexByte(p[:n], detachKey); i >= 0 {
		return i, io.EOF
	}
	return n, err
}

func newTerminalSnapshotCmd(a *app) *cobra.Command {
	var (
		ansi       bool
		scrollback int
	)
	cmd := needsDaemon(&cobra.Command{
		Use:               "snapshot <process-id>",
		Short:             "Print an agent's current screen (and, with --scrollback, its history)",
		Args:              exactArgs(1),
		ValidArgsFunction: completeProcesses(a, true),
		RunE: withTimeout(10*time.Second, func(ctx context.Context, _ *cobra.Command, args []string) error {
			snap, err := a.client.TerminalSnapshot(ctx, client.SnapshotRequest{ProcessID: args[0], ANSI: ansi, Scrollback: scrollback})
			if err != nil {
				return err
			}
			return a.emit(snap, func() error {
				for _, l := range snap.Scrollback {
					fmt.Fprintln(a.out, l)
				}
				last := len(snap.Lines)
				for last > 0 && snap.Lines[last-1] == "" {
					last--
				}
				for _, l := range snap.Lines[:last] {
					fmt.Fprintln(a.out, l)
				}
				return nil
			})
		}),
	})
	cmd.Flags().BoolVar(&ansi, "ansi", false, "keep colours and styles (ANSI escape sequences)")
	cmd.Flags().IntVar(&scrollback, "scrollback", 0, "also print up to this many lines that scrolled off the screen")
	return cmd
}
