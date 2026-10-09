package shim

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Launcher starts shims and reconnects to them. It implements
// terminal.Factory and terminal.Adopter for terminal agents; plain agents
// use StartPlain and Connect.
type Launcher struct {
	// Exe and Args run a shim: the shim's directory is appended, e.g.
	// ("/usr/local/bin/hive", ["__shim"]).
	Exe  string
	Args []string
	// Env is added to the daemon's environment for shim processes.
	Env []string
	// RunDir holds one directory per shim.
	RunDir string

	DefaultSize     terminal.Size
	ScrollbackBytes int
	StopGrace       time.Duration
	// StartTimeout bounds how long a new shim may take to start its agent.
	StartTimeout time.Duration
	Logger       *slog.Logger
}

var (
	_ terminal.Factory = (*Launcher)(nil)
	_ terminal.Adopter = (*Launcher)(nil)
)

// Dir returns the directory of the shim for agent id.
func (l *Launcher) Dir(id string) string { return filepath.Join(l.RunDir, id) }

// Open starts a terminal agent under a new shim.
func (l *Launcher) Open(ctx context.Context, cmd terminal.Command) (terminal.Session, error) {
	size := cmd.Size.Or(l.DefaultSize, terminal.DefaultSize)
	return l.launch(ctx, Spec{
		ID: cmd.ID, Path: cmd.Path, Args: cmd.Args, WorkingDir: cmd.WorkingDir, Env: cmd.Env,
		Terminal: true, Size: size, StdoutPath: cmd.LogPath,
		ScrollbackBytes: l.ScrollbackBytes, StopGrace: l.StopGrace,
	})
}

// StartPlain starts a non-terminal agent under a new shim.
func (l *Launcher) StartPlain(ctx context.Context, spec Spec) (*Remote, error) {
	spec.Terminal = false
	if spec.StopGrace == 0 {
		spec.StopGrace = l.StopGrace
	}
	return l.launch(ctx, spec)
}

// Adopt reconnects to the shim of agent id (terminal.Adopter) for a daemon
// that is recovering. Unlike Connect it settles the outcome: when the shim
// is gone or its agent has finished, the caller records that result, so the
// shim's directory is removed.
func (l *Launcher) Adopt(ctx context.Context, id string) (terminal.Session, error) {
	r, err := l.Connect(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNoShim) {
			_ = l.Discard(id)
		}
		return nil, err
	}
	return r, nil
}

// Connect reconnects to the shim of agent id after a daemon restart. It
// returns ErrNoShim when there is no shim directory, an *Exit (with the
// agent's exit code) when the agent finished while no daemon was watching,
// and ErrShimGone when the shim died without recording an exit.
func (l *Launcher) Connect(ctx context.Context, id string) (*Remote, error) {
	dir := l.Dir(id)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoShim
	}
	st, err := readState(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: no state (%w)", ErrShimGone, err)
	}
	switch st.Status {
	case StatusExited:
		// The shim may still be lingering; let it go.
		if r, err := l.dial(ctx, dir, id); err == nil {
			r.Release() //nolint:contextcheck // releasing is a short, self-bounded call
		}
		if err := exitError(st); err != nil {
			return nil, err
		}
		return nil, &Exit{Code: 0}
	case StatusFailed:
		return nil, fmt.Errorf("%w: agent failed to start: %s", ErrShimGone, st.Error)
	}
	r, err := l.dial(ctx, dir, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrShimGone, err)
	}
	return r, nil
}

// Discard removes the directory of a shim whose result was recorded.
func (l *Launcher) Discard(id string) error { return os.RemoveAll(l.Dir(id)) }

// List returns the IDs of every shim directory.
func (l *Launcher) List() ([]string, error) {
	entries, err := os.ReadDir(l.RunDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// dial connects to the shim in dir and reads its status.
func (l *Launcher) dial(ctx context.Context, dir, id string) (*Remote, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath(dir))
	if err != nil {
		return nil, err
	}
	mux, err := protocol.Handshake(ctx, conn, protocol.Hello{Client: "hived", ClientVersion: buildinfo.Get().Version}, 0)
	if err != nil {
		return nil, err
	}
	var st State
	if err := mux.Call(ctx, "shim.status", nil, &st); err != nil {
		_ = mux.Close()
		return nil, err
	}
	var spec Spec
	_ = jsonfile.Read(specPath(dir), &spec)
	return &Remote{
		id: id, dir: dir, mux: mux, pid: st.PID, terminal: spec.Terminal,
		size: terminal.Size{Width: st.Width, Height: st.Height},
	}, nil
}

func (l *Launcher) launch(ctx context.Context, spec Spec) (*Remote, error) {
	if spec.ID == "" {
		return nil, errors.New("shim: agent ID is required")
	}
	log := logging.OrDiscard(l.Logger).With("process", spec.ID)
	dir := l.Dir(spec.ID)
	if n := len(socketPath(dir)); n > maxSocketPath {
		return nil, fmt.Errorf("shim: socket path %s is %d bytes, over the %d-byte limit; use a shorter HIVE_HOME", socketPath(dir), n, maxSocketPath)
	}
	if err := os.MkdirAll(l.RunDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("shim: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := jsonfile.Write(specPath(dir), spec); err != nil {
		cleanup()
		return nil, err
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer devnull.Close()
	stderr, err := os.OpenFile(stderrPath(dir), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer stderr.Close()

	cmd := exec.Command(l.Exe, append(append([]string(nil), l.Args...), dir)...) //nolint:noctx // the shim outlives the daemon
	cmd.Env = append(os.Environ(), l.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, stderr
	// A new session: the shim survives the daemon's terminal, process group
	// and service manager stopping the daemon.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start shim: %w", err)
	}
	exited := make(chan struct{})
	// Reap the shim if it exits while this daemon runs; after a daemon
	// restart the shim is reparented and reaped by the system.
	go func() { _ = cmd.Wait(); close(exited) }()

	timeout := l.StartTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.After(timeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := readState(dir)
		if err == nil {
			switch st.Status {
			case StatusRunning, StatusExited:
				r, err := l.dial(ctx, dir, spec.ID)
				if err != nil {
					_ = cmd.Process.Kill()
					cleanup()
					return nil, fmt.Errorf("connect to shim: %w", err)
				}
				log.Debug("shim started", "shim_pid", cmd.Process.Pid, "pid", r.pid)
				return r, nil
			case StatusFailed:
				<-exited
				cleanup()
				return nil, errors.New(st.Error)
			}
		}
		select {
		case <-exited:
			msg := tail(stderrPath(dir))
			cleanup()
			return nil, fmt.Errorf("shim exited during start-up: %s", msg)
		case <-deadline:
			_ = cmd.Process.Kill()
			cleanup()
			return nil, fmt.Errorf("shim did not start its agent within %s", timeout)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			cleanup()
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

func tail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "no output"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.Join(lines[max(0, len(lines)-5):], "\n")
}
