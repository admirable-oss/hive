package tui

import (
	"context"
	"sync"
	"time"
)

// inputTimeout bounds one delivery of keystrokes to the daemon.
const inputTimeout = 3 * time.Second

// inputQueue delivers keystrokes to agent terminals in the order they were
// typed. Bubble Tea runs every tea.Cmd on its own goroutine, so sending each
// key from its own command let fast typing reach the agent out of order.
// Here each process has at most one sender goroutine draining a FIFO buffer;
// keys typed while a send is in flight go out together in the next one.
type inputQueue struct {
	send func(ctx context.Context, processID string, data []byte) error

	mu      sync.Mutex
	pending map[string][]byte
	sending map[string]bool
	lastErr error
	closed  bool
	wg      sync.WaitGroup
}

func newInputQueue(send func(context.Context, string, []byte) error) *inputQueue {
	return &inputQueue{send: send, pending: map[string][]byte{}, sending: map[string]bool{}}
}

// Enqueue appends data to processID's queue. It never blocks.
func (q *inputQueue) Enqueue(processID string, data []byte) {
	if len(data) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pending[processID] = append(q.pending[processID], data...)
	if !q.sending[processID] {
		q.sending[processID] = true
		q.wg.Add(1)
		go q.drain(processID)
	}
}

// drain sends processID's buffered input until the buffer is empty. A failed
// send drops what was buffered: replaying stale keystrokes later would be
// worse than losing them, and the failure is reported through TakeErr.
func (q *inputQueue) drain(processID string) {
	defer q.wg.Done()
	for {
		q.mu.Lock()
		data := q.pending[processID]
		delete(q.pending, processID)
		if len(data) == 0 {
			q.sending[processID] = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), inputTimeout)
		err := q.send(ctx, processID, data)
		cancel()
		if err != nil {
			q.mu.Lock()
			q.lastErr = err
			delete(q.pending, processID)
			q.mu.Unlock()
		}
	}
}

// TakeErr returns and clears the most recent delivery error.
func (q *inputQueue) TakeErr() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.lastErr
	q.lastErr = nil
	return err
}

// Close stops accepting input and waits for in-flight deliveries.
func (q *inputQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wg.Wait()
}
