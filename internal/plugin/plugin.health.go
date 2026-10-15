package plugin

import (
	"fmt"
	"time"
)

// Plugin health states.
const (
	// StateStarting: loaded, startup commands not yet ready.
	StateStarting = "starting"
	// StateReady: running normally.
	StateReady = "ready"
	// StateDegraded: running, but something failed in the last
	// degradedFor.
	StateDegraded = "degraded"
	// StateFailed: it cannot run (manifest, permissions, settings), or
	// its startup commands kept crashing and are no longer restarted.
	StateFailed = "failed"
	// StateDisabled: turned off by the user.
	StateDisabled = "disabled"
)

// degradedFor is how long a failure keeps a plugin degraded.
const degradedFor = 5 * time.Minute

// readyAfter is how long startup commands must stay up to count as ready,
// for plugins that do not say so themselves (plugin.ready).
const readyAfter = 2 * time.Second

// Health is how a plugin is doing.
type Health struct {
	State string    `json:"state"`
	Since time.Time `json:"since"`
	// LoadedAt is when it was (re)loaded; ReadyAt when it became ready.
	LoadedAt time.Time `json:"loaded_at,omitzero"`
	ReadyAt  time.Time `json:"ready_at,omitzero"`
	// Restarts counts startup command restarts since loading; Crashes
	// those within the restart window.
	Restarts int `json:"restarts"`
	Crashes  int `json:"crashes"`
	// Events and Actions count what it handled.
	Events  EventStats  `json:"events"`
	Actions ActionStats `json:"actions"`
	// LastError is its latest failure.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
	// Warnings are problems that do not stop it (obsolete settings,
	// handlers for events that never happen, no declared permissions).
	Warnings []string `json:"warnings,omitempty"`
	// Requests are capabilities it asked for that the user has not
	// granted.
	Requests []PermissionRequest `json:"requests,omitempty"`
}

// EventStats counts a plugin's events.
type EventStats struct {
	Delivered uint64 `json:"delivered"`
	Failed    uint64 `json:"failed"`
	// Dropped were not delivered because its queue was full (it is too
	// slow for the events it handles).
	Dropped uint64 `json:"dropped"`
	Queued  int    `json:"queued"`
}

// ActionStats counts a plugin's action runs.
type ActionStats struct {
	Run      uint64 `json:"run"`
	Failed   uint64 `json:"failed"`
	TimedOut uint64 `json:"timed_out"`
	Canceled uint64 `json:"canceled"`
}

// PermissionRequest is a plugin asking, while it runs, for an optional
// capability.
type PermissionRequest struct {
	Plugin     string    `json:"plugin"`
	Capability string    `json:"capability"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`
}

// refreshLocked recomputes l's state; it reports a change.
func (h *Host) refreshLocked(l *loaded) bool {
	state := StateReady
	now := time.Now()
	switch {
	case l.problem != "":
		state = StateFailed
	case l.health.ReadyAt.IsZero():
		state = StateStarting
	case !l.health.LastErrorAt.IsZero() && now.Sub(l.health.LastErrorAt) < degradedFor:
		state = StateDegraded
	}
	if l.queue != nil {
		l.health.Events.Queued = len(l.queue)
	}
	if state == l.health.State {
		return false
	}
	l.health.State, l.health.Since = state, now
	return true
}

// markReadyLocked records that l is ready.
func (h *Host) markReadyLocked(l *loaded) {
	if l.health.ReadyAt.IsZero() {
		l.health.ReadyAt = time.Now()
	}
	h.refreshLocked(l)
}

// Ready records that a plugin says it is ready (plugin.ready).
func (h *Host) Ready(id string) error {
	h.mu.Lock()
	l := h.plugins[id]
	if l == nil {
		h.mu.Unlock()
		return fmt.Errorf("%w %q (or it is disabled)", ErrNotFound, id)
	}
	changed := l.health.ReadyAt.IsZero()
	h.markReadyLocked(l)
	st := h.statusOfLocked(l)
	h.mu.Unlock()
	if changed {
		h.cfg.Publish(EventChanged, st)
	}
	return nil
}

// failLocked records a failure of l; it reports a state change.
func (h *Host) failLocked(l *loaded, msg string) bool {
	l.lastErr = msg
	l.health.LastError, l.health.LastErrorAt = msg, time.Now()
	return h.refreshLocked(l)
}

// crashLocked records a startup command crash; it reports whether the
// plugin crossed its restart limit (and is now failed).
func (h *Host) crashLocked(l *loaded, msg string) bool {
	now := time.Now()
	keep := l.crashes[:0]
	for _, t := range l.crashes {
		if now.Sub(t) < h.cfg.RestartWindow {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	l.crashes = keep
	l.health.Crashes = len(l.crashes)
	h.failLocked(l, msg)
	if len(l.crashes) <= h.cfg.RestartLimit {
		return false
	}
	l.problem = fmt.Sprintf("its startup command failed %d times in %s, so it is no longer restarted (last: %s); fix it, then `hive plugin reload %s`", len(l.crashes), h.cfg.RestartWindow, msg, l.ID)
	l.lastErr = l.problem
	l.health.LastError = l.problem
	h.refreshLocked(l)
	return true
}
