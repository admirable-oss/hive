package platform

import (
	"errors"
	"syscall"
	"time"
)

// AcceptRetry keeps an accept loop alive through temporary failures. Running
// out of file descriptors or memory, or a client giving up before it was
// accepted, fails one Accept; returning from the loop on it would leave the
// socket unserved for good. The zero value is ready to use.
type AcceptRetry struct {
	delay  time.Duration
	streak int
}

const (
	acceptRetryFirst = 5 * time.Millisecond
	acceptRetryMax   = time.Second
)

// Retry reports whether err (from Accept) is temporary. If it is, Retry
// first waits, longer each time in a row (5ms doubling up to 1s), so the
// loop does not spin while the condition lasts.
func (r *AcceptRetry) Retry(err error) bool {
	if !temporaryAcceptError(err) {
		return false
	}
	if r.delay == 0 {
		r.delay = acceptRetryFirst
	} else {
		r.delay = min(r.delay*2, acceptRetryMax)
	}
	r.streak++
	time.Sleep(r.delay)
	return true
}

// First reports whether the last Retry began a run of failures (to log once).
func (r *AcceptRetry) First() bool { return r.streak == 1 }

// Reset is called after a successful Accept.
func (r *AcceptRetry) Reset() { r.delay, r.streak = 0, 0 }

func temporaryAcceptError(err error) bool {
	for _, e := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.ECONNABORTED, syscall.EINTR} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
