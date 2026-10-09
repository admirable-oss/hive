// Command hive is the CLI. `hive daemon` runs the runtime; every other command
// is a thin client of it, and starts it on demand. With no arguments, hive
// opens the multiplexer.
//
// Call chain: main → execute → newApp (composition root) → cobra command →
// client → socket → daemon (see internal/runtime for the server side).
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

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/shim"
)

// shimCommand is the hidden command the daemon uses to start shims.
const shimCommand = "__shim"

// Exit codes. Scripts may rely on them.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2 // bad command line
	exitNotRunning  = 3 // the daemon is not running (status, ping)
	exitTimeout     = 4 // a wait ran out of time (pane wait-output)
	exitInterrupted = 130
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// execute runs the CLI and returns the process exit code.
func execute(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	// `hive __shim <dir>` is how the daemon runs an agent's shim. It needs
	// no configuration, client or command tree.
	if len(args) == 2 && args[0] == shimCommand {
		return shim.Main(args[1])
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	a, err := newApp(getenv, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hive:", err)
		return exitError
	}
	// The session decides which daemon and storage the whole command tree
	// uses, so it is read before cobra parses anything.
	if name, ok := sessionFlag(args); ok {
		if err := a.useSession(name); err != nil {
			fmt.Fprintln(stderr, "hive: --session:", err)
			return exitUsage
		}
	}
	root := newRootCmd(a)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err = root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return exitInterrupted
	}
	var ce *codedError
	if errors.As(err, &ce) {
		if ce.msg != "" {
			fmt.Fprintln(stderr, "hive:", ce.msg)
		}
		return ce.code
	}
	fmt.Fprintln(stderr, "hive:", describe(err))
	if errors.As(err, new(*usageError)) {
		return exitUsage
	}
	return exitError
}

// sessionFlag finds --session NAME or --session=NAME before any "--".
func sessionFlag(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--":
			return "", false
		case arg == "--session" && i+1 < len(args):
			return args[i+1], true
		case strings.HasPrefix(arg, "--session="):
			return strings.TrimPrefix(arg, "--session="), true
		}
	}
	return "", false
}

// codedError ends the program with a specific exit code. An empty message
// means the command already printed what the user needs to see.
type codedError struct {
	code int
	msg  string
}

func (e *codedError) Error() string { return e.msg }

// usageError marks a malformed command line (exit code 2).
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// describe turns wire errors into friendlier text without losing detail.
func describe(err error) string {
	if pe, ok := errors.AsType[*protocol.Error](err); ok && pe.Code == protocol.ErrorCodeNotFound {
		return pe.Message
	}
	return err.Error()
}
