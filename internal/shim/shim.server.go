package shim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// LingerAfterExit is how long a shim waits, after its agent exits, for the
// daemon to collect the result before exiting on its own.
var LingerAfterExit = 30 * time.Second

// Main runs a shim for the spec in dir and returns the process exit code.
// It is what `hive __shim <dir>` runs.
func Main(dir string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// No controlling terminal, but be explicit: a hangup is not a reason to
	// abandon the agent.
	signal.Ignore(syscall.SIGHUP)
	if err := Run(ctx, dir); err != nil {
		fmt.Fprintln(os.Stderr, "hive shim:", err)
		return 1
	}
	return 0
}

// Run starts the agent described in dir and serves it until it has exited
// and the result was collected (or LingerAfterExit passed). Cancelling ctx
// (SIGTERM to the shim) stops the agent first.
func Run(ctx context.Context, dir string) error {
	var spec Spec
	if err := jsonfile.Read(specPath(dir), &spec); err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	logFile, err := logging.OpenRotating(logPath(dir), 1<<20, 1)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log := slog.New(slog.NewTextHandler(logFile, nil)).With("agent", spec.ID)

	s := &server{dir: dir, spec: spec, log: log, exited: make(chan struct{}), release: make(chan struct{})}
	s.state = State{Schema: stateSchema, Status: StatusStarting, ShimPID: os.Getpid(), StartedAt: time.Now()}

	// Listen before starting the agent, so the daemon can connect the
	// moment state.json says running.
	sock := socketPath(dir)
	_ = os.Remove(sock)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", sock)
	if err != nil {
		return s.fail(fmt.Errorf("listen: %w", err))
	}
	defer os.Remove(sock)
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return s.fail(err)
	}

	if err := s.start(ctx); err != nil {
		_ = ln.Close()
		return s.fail(err)
	}
	log.Info("agent started", "pid", s.state.PID, "terminal", spec.Terminal)

	serveCtx, cancelServe := context.WithCancel(context.WithoutCancel(ctx))
	conns := &connSet{conns: map[net.Conn]struct{}{}}
	acceptDone := make(chan struct{})
	go func() { defer close(acceptDone); s.accept(serveCtx, ln, conns) }()
	go s.wait()

	select {
	case <-s.exited:
	case <-ctx.Done():
		log.Info("shim asked to stop; stopping the agent")
		_ = s.stopAgent()
		<-s.exited
	}
	select {
	case <-s.release:
	case <-time.After(LingerAfterExit):
		log.Info("no daemon collected the result; exiting")
	}
	_ = ln.Close()
	<-acceptDone
	cancelServe()
	conns.closeAll()
	return nil
}

// connSet tracks open connections so the shim can close them on exit.
type connSet struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

func (c *connSet) add(conn net.Conn) {
	c.mu.Lock()
	c.conns[conn] = struct{}{}
	c.mu.Unlock()
	c.wg.Add(1)
}

func (c *connSet) done(conn net.Conn) {
	c.mu.Lock()
	delete(c.conns, conn)
	c.mu.Unlock()
	c.wg.Done()
}

func (c *connSet) closeAll() {
	c.mu.Lock()
	for conn := range c.conns {
		_ = conn.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

type server struct {
	dir  string
	spec Spec
	log  *slog.Logger

	sess terminal.Session // terminal agents
	cmd  *exec.Cmd        // plain agents

	mu    sync.Mutex
	state State

	exited      chan struct{} // closed once state.json records the exit
	release     chan struct{}
	releaseOnce sync.Once
}

func (s *server) fail(err error) error {
	s.mu.Lock()
	s.state.Status, s.state.Error = StatusFailed, err.Error()
	st := s.state
	s.mu.Unlock()
	_ = jsonfile.Write(statePath(s.dir), st)
	s.log.Error("agent failed to start", "err", err)
	return err
}

func (s *server) start(ctx context.Context) error {
	sp := s.spec
	if sp.Terminal {
		sess, err := terminal.PTYFactory{ScrollbackBytes: sp.ScrollbackBytes, StopGrace: sp.StopGrace, Logger: s.log}.Open(ctx, terminal.Command{
			ID: sp.ID, Path: sp.Path, Args: sp.Args, WorkingDir: sp.WorkingDir, Env: sp.Env, LogPath: sp.StdoutPath, Size: sp.Size,
		})
		if err != nil {
			return err
		}
		s.sess = sess
		size := sess.Size()
		s.mu.Lock()
		s.state.PID, s.state.Width, s.state.Height = sess.Pid(), size.Width, size.Height
		s.mu.Unlock()
	} else {
		cmd, err := startPlain(sp)
		if err != nil {
			return err
		}
		s.cmd = cmd
		s.mu.Lock()
		s.state.PID = cmd.Process.Pid
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.state.Status = StatusRunning
	st := s.state
	s.mu.Unlock()
	return jsonfile.Write(statePath(s.dir), st)
}

// startPlain runs a non-terminal agent as the leader of its own process
// group, with its output appended to the log files.
func startPlain(sp Spec) (*exec.Cmd, error) {
	c := exec.Command(sp.Path, sp.Args...) //nolint:noctx // the agent outlives everything but the shim
	c.Dir, c.Env = sp.WorkingDir, sp.Env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for _, out := range []struct {
		path string
		dst  *io.Writer
	}{{sp.StdoutPath, &c.Stdout}, {sp.StderrPath, &c.Stderr}} {
		if out.path == "" {
			continue
		}
		f, err := os.OpenFile(out.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			closeAll()
			return nil, err
		}
		files = append(files, f)
		*out.dst = f
	}
	err := c.Start()
	closeAll() // the child has its own copies
	if err != nil {
		return nil, err
	}
	return c, nil
}

// wait records the agent's exit.
func (s *server) wait() {
	var err error
	if s.sess != nil {
		err = s.sess.Wait()
	} else {
		err = s.cmd.Wait()
	}
	code := 0
	if ec, ok := errors.AsType[interface {
		error
		ExitCode() int
	}](err); ok {
		code = ec.ExitCode()
	} else if err != nil {
		code = -1
	}
	now := time.Now()
	s.mu.Lock()
	s.state.Status, s.state.ExitCode, s.state.EndedAt = StatusExited, &code, &now
	st := s.state
	s.mu.Unlock()
	if err := jsonfile.Write(statePath(s.dir), st); err != nil {
		s.log.Error("record exit", "err", err)
	}
	s.log.Info("agent exited", "exit_code", code)
	close(s.exited)
}

func (s *server) stopAgent() error {
	if s.sess != nil {
		return s.sess.Close()
	}
	grace := s.spec.StopGrace
	if grace <= 0 {
		grace = pgroup.Grace
	}
	return pgroup.TerminateAfter(s.cmd.Process.Pid, grace)
}

func (s *server) snapshotState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	if s.sess != nil {
		size := s.sess.Size()
		st.Width, st.Height = size.Width, size.Height
	}
	return st
}

func (s *server) accept(ctx context.Context, ln net.Listener, conns *connSet) {
	router := s.router()
	info := protocol.ServerInfo{Version: buildinfo.Get().Version, Capabilities: []string{vt.FrameCapability}}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conns.add(conn)
		go func() {
			defer conns.done(conn)
			_ = protocol.ServeConn(ctx, conn, router, info)
		}()
	}
}

type dataParams struct {
	Data []byte `json:"data"`
}

type stopParams struct{}

type scrollbackParams struct {
	Lines int  `json:"lines"`
	ANSI  bool `json:"ansi,omitempty"`
}

type snapshotResult struct {
	// Frame is a keyframe of the screen (vt.AppendFrame, without the
	// length prefix).
	Frame []byte `json:"frame"`
}

func (s *server) router() *protocol.Router {
	r := protocol.NewRouter()
	term := func() (terminal.Session, error) {
		if s.sess == nil {
			return nil, protocol.NewError(protocol.ErrorCodeUnsupported, ErrNotTerminal)
		}
		return s.sess, nil
	}
	r.MustRegister("shim.status", protocol.Method(func(context.Context, struct{}) (State, error) {
		return s.snapshotState(), nil
	}))
	r.MustRegister("shim.input", protocol.Method(func(_ context.Context, p dataParams) (protocol.Empty, error) {
		sess, err := term()
		if err == nil {
			_, err = sess.Write(p.Data)
		}
		return protocol.Empty{}, err
	}))
	r.MustRegister("shim.resize", protocol.Method(func(_ context.Context, p terminal.Size) (protocol.Empty, error) {
		sess, err := term()
		if err == nil {
			err = sess.Resize(p)
		}
		return protocol.Empty{}, err
	}))
	r.MustRegister("shim.snapshot", protocol.Method(func(ctx context.Context, _ struct{}) (snapshotResult, error) {
		sess, err := term()
		if err != nil {
			return snapshotResult{}, err
		}
		scr, err := sess.Snapshot(ctx)
		if err != nil {
			return snapshotResult{}, err
		}
		return snapshotResult{Frame: vt.AppendFrame(nil, scr.Keyframe())[4:]}, nil
	}))
	r.MustRegister("shim.scrollback", protocol.Method(func(ctx context.Context, p scrollbackParams) ([]string, error) {
		sess, err := term()
		if err != nil {
			return nil, err
		}
		return sess.Scrollback(ctx, p.Lines, p.ANSI)
	}))
	r.MustRegister("shim.frames", protocol.PipeMethod(func(context.Context, struct{}) (protocol.Empty, protocol.PipeFunc, error) {
		sess, err := term()
		if err != nil {
			return protocol.Empty{}, nil, err
		}
		return protocol.Empty{}, func(ctx context.Context, p protocol.Pipe) error {
			ctx, stop := protocol.UntilClosed(ctx, p)
			defer stop()
			var buf []byte
			return sess.Frames(ctx, func(f *vt.Frame) error {
				buf = vt.AppendFrame(buf[:0], f)
				_, err := p.Write(buf)
				return err
			})
		}, nil
	}))
	r.MustRegister("shim.stop", protocol.Method(func(context.Context, stopParams) (protocol.Empty, error) {
		return protocol.Empty{}, s.stopAgent()
	}))
	// shim.wait blocks until the agent has exited and returns its state.
	r.MustRegister("shim.wait", protocol.Method(func(ctx context.Context, _ struct{}) (State, error) {
		select {
		case <-s.exited:
			return s.snapshotState(), nil
		case <-ctx.Done():
			return State{}, ctx.Err()
		}
	}))
	// shim.release tells an exited shim its result is recorded; it exits.
	r.MustRegister("shim.release", protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		select {
		case <-s.exited:
		default:
			return protocol.Fail(req, protocol.NewError(protocol.ErrorCodeInvalidRequest, errors.New("the agent is still running")))
		}
		resp := protocol.Reply(req, protocol.Empty{})
		resp.AfterSend = func() { s.releaseOnce.Do(func() { close(s.release) }) }
		return resp
	}))
	return r
}
