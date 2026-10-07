package terminal

import (
	"context"
	"sync"
)

// Service tracks the live terminal sessions of running processes, keyed by
// process ID. It owns sessions but not processes.
type Service interface {
	// Open starts a terminal session for processID using the given command.
	Open(ctx context.Context, processID string, cmd Command) (Session, error)
	// Get returns the live session for processID.
	Get(processID string) (Session, error)
	// Close terminates the session for processID.
	Close(processID string) error
}

type service struct {
	factory Factory

	mu       sync.RWMutex
	sessions map[string]Session
}

func NewService(factory Factory) Service {
	return &service{factory: factory, sessions: make(map[string]Session)}
}

func (s *service) Open(ctx context.Context, processID string, cmd Command) (Session, error) {
	if _, err := s.Get(processID); err == nil {
		return nil, ErrSessionExists
	}

	// Spawn outside the lock so one slow start doesn't block every other call.
	session, err := s.factory.Open(ctx, cmd)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if _, exists := s.sessions[processID]; exists {
		s.mu.Unlock()
		_ = session.Close() // lost a race with a concurrent Open
		return nil, ErrSessionExists
	}
	s.sessions[processID] = session
	s.mu.Unlock()

	// Forget the session once its process exits on its own.
	go func() {
		_ = session.Wait()
		s.mu.Lock()
		if s.sessions[processID] == session {
			delete(s.sessions, processID)
		}
		s.mu.Unlock()
	}()

	return session, nil
}

func (s *service) Get(processID string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[processID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

func (s *service) Close(processID string) error {
	s.mu.Lock()
	sess, ok := s.sessions[processID]
	delete(s.sessions, processID)
	s.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	return sess.Close()
}
