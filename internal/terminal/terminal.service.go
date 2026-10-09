package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Service tracks the live terminal sessions of running processes, keyed by
// process ID, and the clients viewing each one. It owns sessions but not
// processes.
type Service interface {
	// Open starts a terminal session for processID using the given command.
	Open(ctx context.Context, processID string, cmd Command) (Session, error)
	// Adopt reconnects to the session of processID after a daemon restart,
	// when the factory's sessions outlive the daemon (ErrNotAdoptable
	// otherwise).
	Adopt(ctx context.Context, processID string) (Session, error)
	// Get returns the live session for processID.
	Get(processID string) (Session, error)
	// Close terminates the session for processID.
	Close(processID string) error

	// Join registers a client view of the terminal. An interactive view (a
	// full-screen attach) takes over the terminal's size at once; others
	// only do so when they send input (Interact).
	Join(processID string, size Size, interactive bool) (viewID string, err error)
	// Leave unregisters a view.
	Leave(processID, viewID string)
	// Interact records input from a view. The last view to interact sets
	// the terminal size, as in tmux: the person typing sees a correct screen.
	Interact(processID, viewID string) error
	// ResizeView records a view's new size, applying it if that view is the
	// one in control.
	ResizeView(processID, viewID string, size Size) error
}

type service struct {
	factory Factory

	mu       sync.RWMutex
	sessions map[string]*entry
}

// entry is a live session and the views on it.
type entry struct {
	sess   Session
	views  map[string]Size
	active string // the view whose size the terminal has
}

func NewService(factory Factory) Service {
	return &service{factory: factory, sessions: make(map[string]*entry)}
}

func (s *service) Open(ctx context.Context, processID string, cmd Command) (Session, error) {
	if _, err := s.Get(processID); err == nil {
		return nil, ErrSessionExists
	}
	if cmd.ID == "" {
		cmd.ID = processID
	}
	// Spawn outside the lock so one slow start doesn't block every other call.
	session, err := s.factory.Open(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if err := s.register(processID, session); err != nil {
		_ = session.Close() // lost a race with a concurrent Open
		return nil, err
	}
	return session, nil
}

func (s *service) Adopt(ctx context.Context, processID string) (Session, error) {
	adopter, ok := s.factory.(Adopter)
	if !ok {
		return nil, ErrNotAdoptable
	}
	session, err := adopter.Adopt(ctx, processID)
	if err != nil {
		return nil, err
	}
	if err := s.register(processID, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *service) register(processID string, session Session) error {
	s.mu.Lock()
	if _, exists := s.sessions[processID]; exists {
		s.mu.Unlock()
		return ErrSessionExists
	}
	s.sessions[processID] = &entry{sess: session, views: map[string]Size{}}
	s.mu.Unlock()

	// Forget the session once its process exits on its own.
	go func() {
		_ = session.Wait()
		s.mu.Lock()
		if e, ok := s.sessions[processID]; ok && e.sess == session {
			delete(s.sessions, processID)
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *service) Get(processID string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.sessions[processID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return e.sess, nil
}

func (s *service) Close(processID string) error {
	s.mu.Lock()
	e, ok := s.sessions[processID]
	delete(s.sessions, processID)
	s.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	return e.sess.Close()
}

func newViewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "v" + hex.EncodeToString(b)
}

func (s *service) Join(processID string, size Size, interactive bool) (string, error) {
	s.mu.Lock()
	e, ok := s.sessions[processID]
	if !ok {
		s.mu.Unlock()
		return "", ErrSessionNotFound
	}
	id := newViewID()
	e.views[id] = size
	s.mu.Unlock()
	if interactive {
		return id, s.Interact(processID, id)
	}
	return id, nil
}

func (s *service) Leave(processID, viewID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.sessions[processID]; ok {
		delete(e.views, viewID)
		if e.active == viewID {
			e.active = "" // keep the size until someone else interacts
		}
	}
}

func (s *service) Interact(processID, viewID string) error {
	s.mu.Lock()
	e, ok := s.sessions[processID]
	if !ok {
		s.mu.Unlock()
		return ErrSessionNotFound
	}
	size, ok := e.views[viewID]
	if !ok {
		s.mu.Unlock()
		return ErrViewNotFound
	}
	if e.active == viewID {
		s.mu.Unlock()
		return nil
	}
	e.active = viewID
	s.mu.Unlock()
	return e.sess.Resize(size)
}

func (s *service) ResizeView(processID, viewID string, size Size) error {
	s.mu.Lock()
	e, ok := s.sessions[processID]
	if !ok {
		s.mu.Unlock()
		return ErrSessionNotFound
	}
	if _, ok := e.views[viewID]; !ok {
		s.mu.Unlock()
		return ErrViewNotFound
	}
	e.views[viewID] = size
	active := e.active == viewID
	s.mu.Unlock()
	if active {
		return e.sess.Resize(size)
	}
	return nil
}
