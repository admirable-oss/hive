package terminal

import (
	"context"
	"sync"
)

// Service manages the live terminal sessions for running processes.
// It is intentionally thin: it owns sessions but does not own processes.
type Service interface {
	// Open starts a terminal session for processID using the given command.
	Open(ctx context.Context, processID string, cmd Command) (Session, error)
	// Get returns the live session for processID.
	Get(processID string) (Session, error)
	// Close terminates the session for processID.
	Close(processID string) error
	// List returns all current process IDs with live sessions.
	List() []string
}

type serviceImpl struct {
	factory Factory

	mu       sync.RWMutex
	sessions map[string]Session
}

func NewService(factory Factory) Service {
	return &serviceImpl{
		factory:  factory,
		sessions: make(map[string]Session),
	}
}

func (s *serviceImpl) Open(ctx context.Context, processID string, cmd Command) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[processID]; exists {
		return nil, ErrSessionExists
	}

	session, err := s.factory.Open(ctx, cmd)
	if err != nil {
		return nil, err
	}

	s.sessions[processID] = session

	// Clean up the entry when the process exits naturally.
	go func() {
		_ = session.Wait()
		s.mu.Lock()
		delete(s.sessions, processID)
		s.mu.Unlock()
	}()

	return session, nil
}

func (s *serviceImpl) Get(processID string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[processID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

func (s *serviceImpl) Close(processID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[processID]
	if !ok {
		return ErrSessionNotFound
	}
	delete(s.sessions, processID)
	return sess.Close()
}

func (s *serviceImpl) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids
}
