package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/term"
)

func cmdTerminal(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing terminal command (attach, resize, input)")
	}
	switch args[0] {
	case "attach":
		return cmdTerminalAttach(ctx, args[1:])
	case "resize":
		return cmdTerminalResize(ctx, args[1:])
	case "input":
		return cmdTerminalInput(ctx, args[1:])
	default:
		return fmt.Errorf("unknown terminal command %q", args[0])
	}
}

func cmdTerminalAttach(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing process id")
	}
	procID := args[0]

	c, err := newClient()
	if err != nil {
		return err
	}

	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		oldState, err := term.MakeRaw(stdinFd)
		if err == nil {
			defer func() {
				_ = term.Restore(stdinFd, oldState)
			}()
		}

		if w, h, err := term.GetSize(stdinFd); err == nil && w > 0 && h > 0 {
			_ = c.TerminalResize(ctx, procID, uint16(w), uint16(h))
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGWINCH)
		defer signal.Stop(sigCh)

		go func() {
			for range sigCh {
				if w, h, err := term.GetSize(stdinFd); err == nil && w > 0 && h > 0 {
					_ = c.TerminalResize(context.Background(), procID, uint16(w), uint16(h))
				}
			}
		}()
	}

	return c.TerminalAttach(ctx, procID, os.Stdin, os.Stdout)
}

func cmdTerminalResize(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: hive terminal resize <process-id> <width> <height>")
	}
	procID := args[0]
	w, err := strconv.ParseUint(args[1], 10, 16)
	if err != nil {
		return fmt.Errorf("invalid width: %w", err)
	}
	h, err := strconv.ParseUint(args[2], 10, 16)
	if err != nil {
		return fmt.Errorf("invalid height: %w", err)
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	return c.TerminalResize(ctx, procID, uint16(w), uint16(h))
}

func cmdTerminalInput(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: hive terminal input <process-id> <data>")
	}
	procID := args[0]
	data := args[1]

	c, err := newClient()
	if err != nil {
		return err
	}
	return c.TerminalInput(ctx, procID, []byte(data))
}
