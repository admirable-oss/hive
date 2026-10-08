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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	fd := os.Stdin.Fd()
	if term.IsTerminal(fd) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("switch terminal to raw mode: %w", err)
		}
		defer func() { _ = term.Restore(fd, state) }()
		go followWindowSize(ctx, a.client, id, fd)
	}

	fmt.Fprintf(a.errOut, "attached to %s — press Ctrl+] to detach\r\n", id)
	err := a.client.TerminalAttach(ctx, id, detachReader{os.Stdin}, a.out)
	fmt.Fprint(a.errOut, "\r\ndetached\r\n")
	return err
}

// followWindowSize keeps the agent's PTY the same size as this terminal.
func followWindowSize(ctx context.Context, c client.Client, id string, fd uintptr) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	for {
		if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
			_ = c.TerminalResize(ctx, id, uint16(w), uint16(h))
		}
		select {
		case <-ctx.Done():
			return
		case <-winch:
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
