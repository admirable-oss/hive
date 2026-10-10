package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/vt"
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

	watcher       Watcher
	watchInterval time.Duration

	mu       sync.RWMutex
	sessions map[string]*entry
}

// entry is a live session and the views on it.
type entry struct {
	sess   Session
	views  map[string]Size
	active string // the view whose size the terminal has
}

// Option configures a Service.
type Option func(*service)

// DefaultWatchInterval is how often a busy session's screen is passed to
// the watcher.
const DefaultWatchInterval = 200 * time.Millisecond

// Watcher follows what every session's screen shows: the daemon's agent
// detection. Its methods are called from one goroutine per session and
// must not block.
type Watcher interface {
	// Frame passes a session's screen: a keyframe first, then changes.
	Frame(processID string, f *vt.Frame)
	// Ended says the session's output ended (its process exited, or the
	// daemon let go of its shim).
	Ended(processID string)
}

// WithWatcher passes every session's frames to w, at most one per
// interval (0: DefaultWatchInterval) per session.
//
// It watches each session as a viewer whose frames are taken slowly: a
// slow viewer gets one frame for everything that changed meanwhile, so a
// busy agent costs one frame per interval, and an idle one nothing.
func WithWatcher(w Watcher, interval time.Duration) Option {
	return func(s *service) {
		if interval <= 0 {
			interval = DefaultWatchInterval
		}
		s.watcher, s.watchInterval = w, interval
	}
}

func NewService(factory Factory, opts ...Option) Service {
	s := &service{factory: factory, sessions: make(map[string]*entry)}
	for _, o := range opts {
		o(s)
	}
	return s
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
	if err := s.register(processID, session); err != nil { //nolint:contextcheck // the session outlives the request that opened it
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
	if err := s.register(processID, session); err != nil { //nolint:contextcheck // the session outlives the request that opened it
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

	// Forget the session once its process exits on its own (or, for a
	// shim, once it is closed or detached).
	ended, end := context.WithCancel(context.Background())
	go func() {
		defer end()
		_ = session.Wait()
		s.mu.Lock()
		if e, ok := s.sessions[processID]; ok && e.sess == session {
			delete(s.sessions, processID)
		}
		s.mu.Unlock()
	}()
	if s.watcher != nil {
		go s.watch(ended, processID, session)
	}
	return nil
}

// watch passes processID's frames to the watcher until the session ends.
func (s *service) watch(ctx context.Context, processID string, session Session) {
	defer s.watcher.Ended(processID)
	_ = session.Frames(ctx, func(f *vt.Frame) error {
		s.watcher.Frame(processID, f)
		t := time.NewTimer(s.watchInterval)
		defer t.Stop()
		select {
		case <-t.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
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
