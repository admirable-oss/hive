package protocol

import "sync"

type hijackStore struct {
	mu    sync.Mutex
	store map[string]HijackFunc
}

func newHijackStore() *hijackStore {
	return &hijackStore{store: make(map[string]HijackFunc)}
}

func (h *hijackStore) set(id string, fn HijackFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store[id] = fn
}

func (h *hijackStore) drain(id string) HijackFunc {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn := h.store[id]
	delete(h.store, id)
	return fn
}
