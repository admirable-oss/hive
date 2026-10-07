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

	"github.com/charmbracelet/x/term"

	"github.com/admirable-oss/hive/internal/client"
)

// detachKey (Ctrl+]) ends an attach session, as in telnet. Every other key,
// Ctrl+C included, goes to the agent.
const detachKey = 0x1d

func cmdTerminal(ctx context.Context, a *app, args []string) error {
	return subcommands{
		"attach": termAttach,
		"resize": termResize,
		"input":  termInput,
	}.dispatch(ctx, a, "terminal", args)
}

func termAttach(ctx context.Context, a *app, args []string) error {
	if err := need(args, 1, "terminal attach <process-id>"); err != nil {
		return err
	}
	id := args[0]
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	fd := os.Stdin.Fd()
	if term.IsTerminal(fd) {
		if state, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, state)
		}
		go followWindowSize(ctx, a.client, id, fd)
	}

	fmt.Fprintf(os.Stderr, "attached to %s — press Ctrl+] to detach\r\n", id)
	err := a.client.TerminalAttach(ctx, id, detachReader{os.Stdin}, os.Stdout)
	fmt.Fprint(os.Stderr, "\r\ndetached\r\n")
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

func termResize(ctx context.Context, a *app, args []string) error {
	if err := need(args, 3, "terminal resize <process-id> <width> <height>"); err != nil {
		return err
	}
	w, err := strconv.ParseUint(args[1], 10, 16)
	if err != nil {
		return fmt.Errorf("invalid width: %w", err)
	}
	h, err := strconv.ParseUint(args[2], 10, 16)
	if err != nil {
		return fmt.Errorf("invalid height: %w", err)
	}
	return a.client.TerminalResize(ctx, args[0], uint16(w), uint16(h))
}

func termInput(ctx context.Context, a *app, args []string) error {
	if err := need(args, 2, "terminal input <process-id> <data>"); err != nil {
		return err
	}
	return a.client.TerminalInput(ctx, args[0], []byte(args[1]))
}
