package process

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/terminal"
)

type StartRequest struct {
	EnvironmentID string   `json:"environment_id"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	// Terminal, when true, starts the process inside a PTY.
	Terminal bool   `json:"terminal"`
	Width    uint16 `json:"width"`
	Height   uint16 `json:"height"`
}

type Service interface {
	Start(context.Context, StartRequest) (Process, error)
	Get(context.Context, string) (Process, error)
	List(context.Context, string) ([]Process, error)
	Stop(context.Context, string) error
}

type serviceImpl struct {
	store       Store
	envService  environment.Service
	runner      Runner
	termService terminal.Service
	baseDir     string

	mu       sync.Mutex
	handles  map[string]Handle
	stopping map[string]bool
}

type sessionHandle struct {
	sess terminal.Session
}

func (h *sessionHandle) PID() int    { return h.sess.Pid() }
func (h *sessionHandle) Wait() error { return h.sess.Wait() }
func (h *sessionHandle) Kill() error { return h.sess.Close() }

func NewService(store Store, envService environment.Service, runner Runner, baseDir string, termService ...terminal.Service) Service {
	var ts terminal.Service
	if len(termService) > 0 {
		ts = termService[0]
	}
	return &serviceImpl{
		store:       store,
		envService:  envService,
		runner:      runner,
		termService: ts,
		baseDir:     baseDir,
		handles:     make(map[string]Handle),
		stopping:    make(map[string]bool),
	}
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *serviceImpl) Start(ctx context.Context, req StartRequest) (Process, error) {
	if req.Command == "" {
		return Process{}, errors.New("command is required")
	}
	if req.EnvironmentID == "" {
		return Process{}, errors.New("environment id is required")
	}

	env, err := s.envService.Get(ctx, req.EnvironmentID)
	if err != nil {
		return Process{}, err
	}

	if req.Args == nil {
		req.Args = []string{}
	}

	id := generateID()
	processDir := filepath.Join(s.baseDir, env.ID, "processes", id)

	p := Process{
		ID:            id,
		EnvironmentID: env.ID,
		Command:       req.Command,
		Args:          req.Args,
		WorkingDir:    env.Path,
		Terminal:      req.Terminal,
		Status:        StatusStarting,
		StartedAt:     time.Now(),
	}

	if req.Terminal {
		if s.termService == nil {
			return Process{}, errors.New("terminal service not available")
		}

		if err := s.store.Create(ctx, p); err != nil {
			return Process{}, err
		}

		termCmd := terminal.Command{
			Path:       req.Command,
			Args:       req.Args,
			WorkingDir: env.Path,
			StdoutPath: filepath.Join(processDir, "stdout.log"),
			StderrPath: filepath.Join(processDir, "stderr.log"),
			Size:       terminal.Size{Width: req.Width, Height: req.Height},
		}

		sess, err := s.termService.Open(context.Background(), id, termCmd)
		if err != nil {
			p.Status = StatusFailed
			now := time.Now()
			p.EndedAt = &now
			_ = s.store.Update(context.Background(), p)
			return p, err
		}

		p.PID = sess.Pid()
		p.Status = StatusRunning
		if err := s.store.Update(context.Background(), p); err != nil {
			_ = sess.Close()
			return Process{}, err
		}

		handle := &sessionHandle{sess: sess}
		s.mu.Lock()
		s.handles[id] = handle
		s.mu.Unlock()

		go s.monitor(handle, p)

		return p, nil
	}

	if err := s.store.Create(ctx, p); err != nil {
		return Process{}, err
	}

	cmd := Command{
		Path:       req.Command,
		Args:       req.Args,
		WorkingDir: env.Path,
		StdoutPath: filepath.Join(processDir, "stdout.log"),
		StderrPath: filepath.Join(processDir, "stderr.log"),
	}

	handle, err := s.runner.Start(context.Background(), cmd)
	if err != nil {
		p.Status = StatusFailed
		now := time.Now()
		p.EndedAt = &now
		_ = s.store.Update(context.Background(), p)
		return p, err
	}

	p.PID = handle.PID()
	p.Status = StatusRunning
	if err := s.store.Update(context.Background(), p); err != nil {
		_ = handle.Kill()
		return Process{}, err
	}

	s.mu.Lock()
	s.handles[id] = handle
	s.mu.Unlock()

	go s.monitor(handle, p)

	return p, nil
}

func (s *serviceImpl) monitor(handle Handle, p Process) {
	err := handle.Wait()

	s.mu.Lock()
	delete(s.handles, p.ID)
	stopped := s.stopping[p.ID]
	delete(s.stopping, p.ID)
	s.mu.Unlock()

	now := time.Now()
	p.EndedAt = &now

	type exitCoder interface {
		ExitCode() int
	}

	if stopped {
		p.Status = StatusKilled
		code := -1
		if ec, ok := err.(exitCoder); ok {
			code = ec.ExitCode()
		}
		p.ExitCode = &code
	} else if err != nil {
		if ec, ok := err.(exitCoder); ok {
			code := ec.ExitCode()
			p.ExitCode = &code
			if code == -1 {
				p.Status = StatusKilled
			} else {
				p.Status = StatusExited
			}
		} else {
			code := -1
			p.ExitCode = &code
			p.Status = StatusFailed
		}
	} else {
		code := 0
		p.ExitCode = &code
		p.Status = StatusExited
	}

	_ = s.store.Update(context.Background(), p)
}

func (s *serviceImpl) Get(ctx context.Context, id string) (Process, error) {
	return s.store.Get(ctx, id)
}

func (s *serviceImpl) List(ctx context.Context, envID string) ([]Process, error) {
	return s.store.List(ctx, envID)
}

func (s *serviceImpl) Stop(ctx context.Context, id string) error {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.Status != StatusRunning && p.Status != StatusStarting {
		return nil
	}

	s.mu.Lock()
	s.stopping[id] = true
	handle, ok := s.handles[id]
	s.mu.Unlock()

	if !ok {
		p.Status = StatusKilled
		now := time.Now()
		p.EndedAt = &now
		return s.store.Update(ctx, p)
	}

	return handle.Kill()
}
