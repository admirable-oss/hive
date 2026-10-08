package terminal

import "context"

// Session is a running process with a terminal attached. The PTY is an
// implementation detail behind this contract.
type Session interface {
	// Write sends input to the process, as if typed on a keyboard.
	Write(b []byte) (int, error)
	// Resize changes the terminal dimensions, e.g. after a window resize.
	Resize(size Size) error
	// Wait blocks until the process exits and returns its error.
	Wait() error
	// Pid returns the OS PID of the running process.
	Pid() int
	// Close stops the process and releases the terminal.
	Close() error
	// Subscribe attaches a listener to the live output stream.
	Subscribe() *Subscription
}

// Subscription is one listener on a session's output.
type Subscription struct {
	// C delivers output chunks in order. It is closed when the session ends,
	// when Close is called, or when the subscriber falls too far behind (see
	// Lagged). A subscriber never sees a gap: it is cut off instead.
	C <-chan []byte
	// History is the recent output that preceded the first chunk on C.
	History []byte

	cancel func()
	lagged func() bool
}

// NewSubscription builds a Subscription. cancel detaches the listener and
// lagged reports whether it was cut off for falling behind; either may be nil.
func NewSubscription(c <-chan []byte, history []byte, cancel func(), lagged func() bool) *Subscription {
	return &Subscription{C: c, History: history, cancel: cancel, lagged: lagged}
}

// Close detaches the listener. It is safe to call more than once.
func (s *Subscription) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Lagged reports whether C was closed because the listener could not keep up.
func (s *Subscription) Lagged() bool { return s.lagged != nil && s.lagged() }

// Factory opens a new terminal session for the given command.
type Factory interface {
	Open(ctx context.Context, cmd Command) (Session, error)
}
