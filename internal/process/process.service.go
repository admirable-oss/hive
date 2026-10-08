package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/logging"
	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
	"github.com/admirable-oss/hive/internal/terminal"
)

// Environments is the part of the environment service processes depend on.
type Environments interface {
	Get(ctx context.Context, id string) (environment.Environment, error)
}

// Terminals opens PTY sessions for processes started with Terminal: true.
type Terminals interface {
	Open(ctx context.Context, processID string, cmd terminal.Command) (terminal.Session, error)
}

type Service interface {
	Start(ctx context.Context, req StartRequest) (Process, error)
	Get(ctx context.Context, id string) (Process, error)
	// List returns one environment's processes, or all when envID is empty.
	List(ctx context.Context, envID string) ([]Process, error)
	Stop(ctx context.Context, id string) error
	// Logs returns the tail of one of a process's logs.
	Logs(ctx context.Context, req LogsRequest) (string, error)
	// OpenLogs validates req and prepares a stream of the log, which may
	// follow new output (see LogStream).
	OpenLogs(ctx context.Context, req LogsRequest) (*LogStream, error)

	// StopEnvironment stops every live process in envID and waits for them.
	StopEnvironment(ctx context.Context, envID string) error
	// StopAll stops every live process and waits for them (daemon shutdown).
	StopAll(ctx context.Context) error
	// Recover closes out records a crashed daemon left "running" and stops
	// any of their processes that are provably still alive.
	Recover(ctx context.Context) error
}

// Option customises a service built by NewService.
type Option func(*service)

// WithLogger sets the service's logger. The default discards everything.
func WithLogger(l *slog.Logger) Option {
	return func(s *service) { s.log = logging.OrDiscard(l) }
}

// WithOrphanControl replaces how processes left by an earlier daemon are
// inspected and stopped. Tests use it to avoid touching real processes.
func WithOrphanControl(lookup func(pid int) (platform.ProcessInfo, error), terminate func(pid int) error) Option {
	return func(s *service) { s.lookup, s.terminate = lookup, terminate }
}

type service struct {
	store  Store
	envs   Environments
	runner Runner
	terms  Terminals // nil disables Terminal: true
	log    *slog.Logger

	// Orphan control: see reapOrphan.
	lookup    func(pid int) (platform.ProcessInfo, error)
	terminate func(pid int) error

	mu   sync.Mutex
	live map[string]*liveProcess
}

// liveProcess is a process this daemon launched and is still supervising.
type liveProcess struct {
	envID    string
	handle   Handle
	stopping bool          // set by Stop so the exit is recorded as killed
	done     chan struct{} // closed once the final state is persisted
}

func NewService(store Store, envs Environments, runner Runner, terms Terminals, opts ...Option) Service {
	s := &service{
		store:     store,
		envs:      envs,
		runner:    runner,
		terms:     terms,
		log:       logging.Discard(),
		lookup:    platform.LookupProcess,
		terminate: pgroup.Terminate,
		live:      make(map[string]*liveProcess),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

func (s *service) Start(ctx context.Context, req StartRequest) (Process, error) {
	if req.Command == "" {
		return Process{}, ErrCommandRequired
	}
	if req.Terminal && s.terms == nil {
		return Process{}, ErrNoTerminalSupport
	}
	env, err := s.envs.Get(ctx, req.EnvironmentID)
	if err != nil {
		return Process{}, err
	}
	if req.Args == nil {
		req.Args = []string{}
	}

	p := Process{
		ID:            newID(),
		EnvironmentID: env.ID,
		Command:       req.Command,
		Args:          req.Args,
		WorkingDir:    env.Path,
		Terminal:      req.Terminal,
		Status:        StatusStarting,
		StartedAt:     time.Now(),
	}
	if err := s.store.Create(ctx, p); err != nil {
		return Process{}, err
	}
	log := s.log.With("process", p.ID, "env", p.EnvironmentID)

	// The process outlives this request, so it gets a context that is never cancelled.
	handle, err := s.launch(context.WithoutCancel(ctx), p, req)
	if err != nil {
		now := time.Now()
		p.Status, p.EndedAt = StatusFailed, &now
		log.Warn("process failed to start", "command", p.Command, "err", err)
		if uerr := s.store.Update(context.WithoutCancel(ctx), p); uerr != nil {
			log.Error("record failed start", "err", uerr)
		}
		return p, err
	}

	p.PID, p.Status = handle.PID(), StatusRunning
	if err := s.store.Update(ctx, p); err != nil {
		log.Error("record started process; stopping it", "pid", p.PID, "err", err)
		_ = handle.Kill()
		return Process{}, err
	}

	lp := &liveProcess{envID: p.EnvironmentID, handle: handle, done: make(chan struct{})}
	s.mu.Lock()
	s.live[p.ID] = lp
	s.mu.Unlock()
	log.Info("process started", "pid", p.PID, "command", p.Command, "terminal", p.Terminal)
	go s.monitor(p, lp) //nolint:gosec,contextcheck // supervision outlives the start request by design

	return p, nil
}

// launch starts p either in a PTY or as a plain process; both become a Handle
// so the rest of the lifecycle doesn't care which.
func (s *service) launch(ctx context.Context, p Process, req StartRequest) (Handle, error) {
	stdout, stderr := s.store.LogPaths(p)
	if !p.Terminal {
		return s.runner.Start(ctx, Command{
			Path: p.Command, Args: p.Args, WorkingDir: p.WorkingDir,
			StdoutPath: stdout, StderrPath: stderr,
		})
	}
	sess, err := s.terms.Open(ctx, p.ID, terminal.Command{
		Path: p.Command, Args: p.Args, WorkingDir: p.WorkingDir,
		LogPath: stdout,
		Size:    terminal.Size{Width: req.Width, Height: req.Height},
	})
	if err != nil {
		return nil, err
	}
	return sessionHandle{sess}, nil
}

// sessionHandle lets a terminal session act as a Handle. Embedding supplies
// Wait; only the two differently named methods are adapted.
type sessionHandle struct{ terminal.Session }

func (h sessionHandle) PID() int    { return h.Pid() }
func (h sessionHandle) Kill() error { return h.Close() }

// monitor waits for the process to exit and records how it ended.
func (s *service) monitor(p Process, lp *liveProcess) {
	defer close(lp.done)
	err := lp.handle.Wait()

	s.mu.Lock()
	delete(s.live, p.ID)
	stopping := lp.stopping
	s.mu.Unlock()

	code, status := 0, StatusExited
	if ec, ok := errors.AsType[interface {
		error
		ExitCode() int
	}](err); ok {
		code = ec.ExitCode() // -1 means it was killed by a signal
		if code == -1 {
			status = StatusKilled
		}
	} else if err != nil {
		code, status = -1, StatusFailed
	}
	if stopping {
		status = StatusKilled
	}

	now := time.Now()
	p.Status, p.ExitCode, p.EndedAt = status, &code, &now
	log := s.log.With("process", p.ID, "env", p.EnvironmentID, "pid", p.PID)
	log.Info("process ended", "status", status, "exit_code", code, "runtime", now.Sub(p.StartedAt).Round(time.Millisecond))
	if err := s.store.Update(context.Background(), p); err != nil {
		log.Error("record process exit", "err", err)
	}
}

func (s *service) Get(ctx context.Context, id string) (Process, error) {
	return s.store.Get(ctx, id)
}

func (s *service) List(ctx context.Context, envID string) ([]Process, error) {
	return s.store.List(ctx, envID)
}

func (s *service) Stop(ctx context.Context, id string) error {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !p.Active() {
		return nil
	}

	s.mu.Lock()
	lp, ok := s.live[id]
	if ok {
		lp.stopping = true
	}
	s.mu.Unlock()

	if !ok {
		// The record predates this daemon. Its process may still be running
		// (a plain process survives a daemon crash), so stop it if it is
		// provably ours, then close the record out.
		s.reapOrphan(p)
		return s.closeOut(ctx, p, StatusKilled)
	}
	s.log.Info("stopping process", "process", id, "pid", p.PID)
	return lp.handle.Kill()
}

func (s *service) Logs(ctx context.Context, req LogsRequest) (string, error) {
	req, p, err := s.resolveLogs(ctx, req)
	if err != nil {
		return "", err
	}
	if req.Tail <= 0 {
		req.Tail = DefaultTail
	}
	return s.store.Tail(ctx, p, req.Stream, req.Tail)
}

// resolveLogs validates req and loads the process it names.
func (s *service) resolveLogs(ctx context.Context, req LogsRequest) (LogsRequest, Process, error) {
	req, err := req.normalize()
	if err != nil {
		return req, Process{}, err
	}
	p, err := s.store.Get(ctx, req.ID)
	if err != nil {
		return req, Process{}, err
	}
	if p.Terminal && req.Stream == StreamStderr {
		return req, Process{}, ErrNoStderr
	}
	return req, p, nil
}

func (s *service) StopEnvironment(ctx context.Context, envID string) error {
	return s.stopLive(ctx, func(lp *liveProcess) bool { return lp.envID == envID })
}

func (s *service) StopAll(ctx context.Context) error {
	return s.stopLive(ctx, func(*liveProcess) bool { return true })
}

// stopLive kills every matching live process, then waits until each one's
// final state is on disk (or ctx expires).
func (s *service) stopLive(ctx context.Context, match func(*liveProcess) bool) error {
	s.mu.Lock()
	var targets []*liveProcess
	for _, lp := range s.live {
		if match(lp) {
			lp.stopping = true
			targets = append(targets, lp)
		}
	}
	s.mu.Unlock()

	var errs []error
	for _, lp := range targets {
		errs = append(errs, lp.handle.Kill())
	}
	for _, lp := range targets {
		select {
		case <-lp.done:
		case <-ctx.Done():
			s.log.Warn("gave up waiting for processes to exit", "pending", s.liveCount(match), "err", ctx.Err())
			return ctx.Err()
		}
	}
	return errors.Join(errs...)
}

func (s *service) liveCount(match func(*liveProcess) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, lp := range s.live {
		if match(lp) {
			n++
		}
	}
	return n
}

// Recover runs at daemon start-up. Records still marked active belong to a
// daemon that died without stopping its agents. This daemon cannot supervise
// them (a PTY agent lost its terminal with the old daemon), so any that are
// provably still running are stopped and the records are closed out: killed
// when a process was stopped, failed when it was already gone.
func (s *service) Recover(ctx context.Context) error {
	procs, err := s.store.List(ctx, "")
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range procs {
		s.mu.Lock()
		_, live := s.live[p.ID]
		s.mu.Unlock()
		if !p.Active() || live {
			continue
		}
		status := StatusFailed
		if s.reapOrphan(p) {
			status = StatusKilled
		}
		s.log.Warn("closed out process left by a previous daemon", "process", p.ID, "env", p.EnvironmentID, "pid", p.PID, "status", status)
		errs = append(errs, s.closeOut(ctx, p, status))
	}
	return errors.Join(errs...)
}

// orphanStartWindow bounds how far a process's kernel start time may be from
// its record's StartedAt for it to count as the same process. The record is
// written just before launch; the lower slack covers coarse kernel clocks.
const (
	orphanStartBefore = 2 * time.Second
	orphanStartAfter  = 30 * time.Second
)

// reapOrphan stops the process group of a record left by an earlier daemon
// and reports whether it did. PIDs are reused (certainly across a reboot), so
// it only signals a process that leads its own group and was started when
// the record says; anything else is someone else's process.
func (s *service) reapOrphan(p Process) bool {
	if p.PID <= 0 {
		return false
	}
	log := s.log.With("process", p.ID, "pid", p.PID)
	info, err := s.lookup(p.PID)
	switch {
	case errors.Is(err, platform.ErrNoProcess):
		return false
	case err != nil:
		log.Warn("cannot inspect possible orphan; leaving it alone", "err", err)
		return false
	case info.PGID != p.PID:
		log.Info("pid now belongs to another process; leaving it alone")
		return false
	}
	if d := info.StartTime.Sub(p.StartedAt); d < -orphanStartBefore || d > orphanStartAfter {
		log.Info("pid was reused by a later process; leaving it alone", "started", info.StartTime)
		return false
	}
	if err := s.terminate(p.PID); err != nil {
		log.Warn("stop orphaned process group", "err", err)
		return false
	}
	log.Warn("stopped orphaned process group from a previous daemon")
	return true
}

func (s *service) closeOut(ctx context.Context, p Process, status Status) error {
	now, code := time.Now(), -1
	p.Status, p.ExitCode, p.EndedAt = status, &code, &now
	return s.store.Update(ctx, p)
}
