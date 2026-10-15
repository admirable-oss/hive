package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// eventQueue bounds the events waiting for one plugin. Past it events
	// are dropped (and counted), never waited for: a slow plugin cannot
	// hold up the daemon or other plugins.
	eventQueue = 256
	// defaultEventConcurrency is how many of a plugin's event commands run
	// at once unless its manifest says.
	defaultEventConcurrency = 4
)

type queuedEvent struct {
	handler Event
	ctx     Context
}

// Event queues every enabled plugin's handlers for an event of typ.
func (h *Host) Event(typ string, data json.RawMessage) {
	h.EventAt(typ, 0, time.Time{}, data)
}

// EventAt queues an event with its daemon sequence number and time.
// Delivery is at most once, in sequence order per plugin; an event that
// does not fit a plugin's queue is dropped for that plugin, and the next
// one it gets says Missed.
func (h *Host) EventAt(typ string, seq uint64, at time.Time, data json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !strings.HasPrefix(typ, "plugin.") {
		// Plugin events are for the UI and other clients; feeding them to
		// handlers could loop a plugin on its own health.
		for _, l := range h.plugins {
			h.queueLocked(l, typ, seq, at, data)
		}
	}
}

func (h *Host) queueLocked(l *loaded, typ string, seq uint64, at time.Time, data json.RawMessage) {
	if l.problem != "" || l.queue == nil {
		return
	}
	for _, e := range l.Manifest.Events {
		if !e.Matches(typ) {
			continue
		}
		c := Context{Trigger: "event", Event: &EventInfo{Type: typ, Data: data, Seq: seq, Time: at, Missed: h.missed || l.missed}}
		c.Environment, c.Tab, c.Pane = about(data)
		select {
		case l.queue <- queuedEvent{handler: e, ctx: c}:
			l.missed = false
		default:
			l.missed = true
			l.health.Events.Dropped++
			if l.health.Events.Dropped == 1 || l.health.Events.Dropped%100 == 0 {
				h.log.Warn("plugin is too slow for its events; dropping", "plugin", l.ID, "dropped", l.health.Events.Dropped)
			}
			h.failLocked(l, fmt.Sprintf("dropped %d event(s): its handlers are slower than the events they take", l.health.Events.Dropped))
		}
	}
	h.missed = false
}

// Lost records that the daemon lost events before the next ones: every
// plugin's next event says Missed.
func (h *Host) Lost() {
	h.mu.Lock()
	h.missed = true
	h.mu.Unlock()
}

// deliver runs l's queued events until it is unloaded.
func (h *Host) deliver(l *loaded) {
	for {
		select {
		case <-l.ctx.Done():
			return
		case q := <-l.queue:
			h.mu.Lock()
			skip := l.problem != ""
			h.mu.Unlock()
			if skip {
				continue
			}
			ctx, cancel := context.WithTimeout(l.ctx, q.handler.Timeout())
			_, err := h.run(ctx, l, q.handler.Command, q.ctx, nil)
			timedOut := ctx.Err() == context.DeadlineExceeded
			cancel()
			h.mu.Lock()
			l.health.Events.Delivered++
			if err != nil && l.ctx.Err() == nil {
				l.health.Events.Failed++
				msg := fmt.Sprintf("on %s: %v", q.ctx.Event.Type, err)
				if timedOut {
					msg = fmt.Sprintf("on %s: timed out after %s", q.ctx.Event.Type, q.handler.Timeout())
				}
				changed := h.failLocked(l, msg)
				st := h.statusOfLocked(l)
				h.mu.Unlock()
				h.log.Warn("plugin event command failed", "plugin", l.ID, "event", q.ctx.Event.Type, "err", err)
				if changed {
					h.cfg.Publish(EventChanged, st)
				}
				continue
			}
			h.mu.Unlock()
		}
	}
}

// about reads which environment, tab and pane an event's data names.
func about(data json.RawMessage) (env, tab, pane string) {
	var d struct {
		ID            string `json:"id"`
		EnvironmentID string `json:"environment_id"`
		TabID         string `json:"tab_id"`
		PaneID        string `json:"pane_id"`
		Environment   *struct {
			ID string `json:"id"`
		} `json:"environment"`
	}
	if json.Unmarshal(data, &d) != nil {
		return "", "", ""
	}
	env, tab, pane = d.EnvironmentID, d.TabID, d.PaneID
	if d.Environment != nil {
		env = d.Environment.ID
	}
	return env, tab, pane
}

// Restart pauses for a startup command that keeps exiting.
const (
	restartFirst = 2 * time.Second
	restartMax   = time.Minute
)

// runStartup runs one startup command for l's life, restarting it when it
// asks to be, until it crashes more than the restart limit allows.
func (h *Host) runStartup(l *loaded, s Startup) {
	ctx := l.ctx
	pause := restartFirst
	for ctx.Err() == nil {
		h.mu.Lock()
		if l.problem != "" {
			h.mu.Unlock()
			return
		}
		l.running++
		h.mu.Unlock()
		start := time.Now()
		done := make(chan struct{})
		ready := time.AfterFunc(readyAfter, func() {
			select {
			case <-done:
				return
			default:
			}
			h.mu.Lock()
			changed := l.health.ReadyAt.IsZero()
			h.markReadyLocked(l)
			st := h.statusOfLocked(l)
			h.mu.Unlock()
			if changed {
				h.cfg.Publish(EventChanged, st)
			}
		})
		_, err := h.run(ctx, l, s.Command, Context{Trigger: "startup"}, nil)
		close(done)
		ready.Stop()
		h.mu.Lock()
		l.running--
		if ctx.Err() != nil {
			h.mu.Unlock()
			return
		}
		if err == nil && !s.Restart {
			h.markReadyLocked(l) // a one-shot setup that finished
			h.mu.Unlock()
			return
		}
		msg := fmt.Sprintf("startup %s exited", strings.Join(s.Command, " "))
		if err != nil {
			msg = fmt.Sprintf("startup %s: %v", strings.Join(s.Command, " "), err)
		}
		// Only quick exits count towards the restart limit: a watcher that
		// ran a good while and stopped is restarted without blame.
		gaveUp := false
		if time.Since(start) < restartMax {
			gaveUp = h.crashLocked(l, msg)
		} else if err != nil {
			h.failLocked(l, msg)
		}
		if s.Restart && !gaveUp {
			l.health.Restarts++
		}
		st := h.statusOfLocked(l)
		h.mu.Unlock()
		h.cfg.Publish(EventChanged, st)
		if gaveUp || !s.Restart {
			if gaveUp {
				h.log.Error("plugin keeps crashing; no longer restarting it", "plugin", l.ID, "command", s.Command, "err", err)
			}
			return
		}
		if time.Since(start) > restartMax {
			pause = restartFirst // it ran a good while: start over quickly
		}
		h.log.Warn("plugin command stopped; starting it again", "plugin", l.ID, "command", s.Command, "err", err, "in", pause)
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
		pause = min(pause*2, restartMax)
	}
}
