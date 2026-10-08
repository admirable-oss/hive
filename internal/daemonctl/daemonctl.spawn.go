package daemonctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SpawnOptions describes how to launch a detached daemon.
type SpawnOptions struct {
	// Executable and Args run the daemon in the foreground, e.g.
	// ("/usr/local/bin/hive", ["daemon"]).
	Executable string
	Args       []string
	// Env is the daemon's environment; nil inherits this process's.
	Env []string
	// StderrPath receives the daemon's standard error (truncated on each
	// start). Errors that happen before logging is set up land here.
	StderrPath string
	// LogPath is the daemon's structured log, named in error messages.
	LogPath string
	// Ready reports whether the daemon answers. It is polled until it
	// succeeds or Timeout passes.
	Ready   func(ctx context.Context) error
	Timeout time.Duration
}

// ErrStartTimeout means the daemon was launched but never became ready.
var ErrStartTimeout = errors.New("daemon did not become ready in time")

const pollInterval = 25 * time.Millisecond

// Spawn starts the daemon in its own session, detached from this terminal,
// and waits until it is ready. If another client started a daemon at the same
// moment, ours exits (only one may own the storage root) but Ready succeeds
// against the winner, so Spawn still succeeds.
//
// Spawn leaves nothing running in this process: the child's stdio are plain
// files (no copy goroutines) and its exit is polled, not waited for.
func Spawn(ctx context.Context, opts SpawnOptions) error {
	if opts.Ready == nil {
		return errors.New("daemonctl: Ready probe is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	stderr := devnull
	if opts.StderrPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.StderrPath), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(opts.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		stderr = f
	}

	cmd := exec.Command(opts.Executable, opts.Args...) //nolint:noctx // the daemon outlives this process
	cmd.Env = opts.Env
	// The daemon must not hold this terminal, or closing it would hang the
	// daemon up. A new session also keeps Ctrl+C in this shell away from it.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	pid := cmd.Process.Pid

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		probeCtx, probeCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := opts.Ready(probeCtx)
		probeCancel()
		if err == nil {
			// The daemon now lives on its own; this process never waits
			// for it (it is reparented when we exit).
			_ = cmd.Process.Release()
			return nil
		}
		if exited, status := reaped(pid); exited {
			// Lost a race with another client's daemon? Then it is up.
			if opts.Ready(ctx) == nil {
				return nil
			}
			return startupError(status, opts.StderrPath, opts.LogPath)
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Release()
			return fmt.Errorf("%w (%s); see %s", ErrStartTimeout, opts.Timeout, opts.LogPath)
		case <-ticker.C:
		}
	}
}

// reaped reports whether pid has exited, collecting it if so.
func reaped(pid int) (bool, syscall.WaitStatus) {
	var status syscall.WaitStatus
	for {
		got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return err == nil && got == pid, status
	}
}

func startupError(status syscall.WaitStatus, stderrPath, logPath string) error {
	msg := tailFile(stderrPath, 5)
	if msg == "" {
		msg = tailFile(logPath, 5)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", status.ExitStatus())
	}
	where := logPath
	if where == "" {
		where = stderrPath
	}
	return fmt.Errorf("daemon exited during start-up: %s (see %s)", msg, where)
}

// tailFile returns the last n lines of path, or "" if it cannot be read.
func tailFile(path string, n int) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(strings.Join(lines[max(0, len(lines)-n):], "\n"))
}
