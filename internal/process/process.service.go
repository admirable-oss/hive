package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
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
	Logs(ctx context.Context, id string, tail int) (string, error)

	// StopEnvironment stops every live process in envID and waits for them.
	StopEnvironment(ctx context.Context, envID string) error
	// StopAll stops every live process and waits for them (daemon shutdown).
	StopAll(ctx context.Context) error
	// Recover closes out records a crashed daemon left "running".
	Recover(ctx context.Context) error
}

type service struct {
	store  Store
	envs   Environments
	runner Runner
	terms  Terminals // nil disables Terminal: true

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

func NewService(store Store, envs Environments, runner Runner, terms Terminals) Service {
	return &service{
		store:  store,
		envs:   envs,
		runner: runner,
		terms:  terms,
		live:   make(map[string]*liveProcess),
	}
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

	handle, err := s.launch(p, req)
	if err != nil {
		now := time.Now()
		p.Status, p.EndedAt = StatusFailed, &now
		_ = s.store.Update(ctx, p)
		return p, err
	}

	p.PID, p.Status = handle.PID(), StatusRunning
	if err := s.store.Update(ctx, p); err != nil {
		_ = handle.Kill()
		return Process{}, err
	}

	lp := &liveProcess{envID: p.EnvironmentID, handle: handle, done: make(chan struct{})}
	s.mu.Lock()
	s.live[p.ID] = lp
	s.mu.Unlock()
	go s.monitor(p, lp)

	return p, nil
}

// launch starts p either in a PTY or as a plain process; both become a Handle
// so the rest of the lifecycle doesn't care which.
func (s *service) launch(p Process, req StartRequest) (Handle, error) {
	stdout, stderr := s.store.LogPaths(p)
	if !p.Terminal {
		return s.runner.Start(context.Background(), Command{
			Path: p.Command, Args: p.Args, WorkingDir: p.WorkingDir,
			StdoutPath: stdout, StderrPath: stderr,
		})
	}
	sess, err := s.terms.Open(context.Background(), p.ID, terminal.Command{
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
	_ = s.store.Update(context.Background(), p)
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
		// The record says running but no live handle exists (it predates
		// this daemon), so there is nothing to signal; just close it out.
		return s.closeOut(ctx, p, StatusKilled)
	}
	return lp.handle.Kill()
}

func (s *service) Logs(ctx context.Context, id string, tail int) (string, error) {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return s.store.Tail(ctx, p, tail)
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
			return ctx.Err()
		}
	}
	return errors.Join(errs...)
}

// Recover runs at daemon start-up. Records still marked active belong to a
// daemon that died without stopping its agents; this one has no handle on
// them and cannot know how they ended, so they are marked failed.
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
		if p.Active() && !live {
			errs = append(errs, s.closeOut(ctx, p, StatusFailed))
		}
	}
	return errors.Join(errs...)
}

func (s *service) closeOut(ctx context.Context, p Process, status Status) error {
	now, code := time.Now(), -1
	p.Status, p.ExitCode, p.EndedAt = status, &code, &now
	return s.store.Update(ctx, p)
}
