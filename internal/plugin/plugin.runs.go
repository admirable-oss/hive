package plugin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrNoAction is returned for an action a plugin does not have.
var ErrNoAction = errors.New("no such plugin action")

// ErrNoRun is returned for a run that is not in progress.
var ErrNoRun = errors.New("no such run in progress")

// RunInfo is an action run in progress.
type RunInfo struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	Async   bool      `json:"async,omitempty"`
	Started time.Time `json:"started"`
}

type actionRun struct {
	info   RunInfo
	cancel context.CancelFunc
}

// Action runs a plugin's action. A synchronous action is waited for (until
// its timeout, or until ctx ends: the caller went away); an async one
// returns at once with its run ID, and its outcome goes to the plugin's
// health and log. Either can be canceled by run ID (Cancel).
func (h *Host) Action(ctx context.Context, id, action string, c Context) (Result, error) {
	h.mu.Lock()
	l := h.plugins[id]
	if l == nil {
		h.mu.Unlock()
		return Result{}, fmt.Errorf("%w %q (or it is disabled; see `hive plugin list`)", ErrNotFound, id)
	}
	if l.problem != "" {
		h.mu.Unlock()
		return Result{}, fmt.Errorf("plugin %s is not ready: %s", id, l.problem)
	}
	a, ok := l.Manifest.Action(action)
	if !ok {
		h.mu.Unlock()
		var ids []string
		for _, x := range l.Manifest.Actions {
			ids = append(ids, x.ID)
		}
		return Result{}, fmt.Errorf("%w %q in %s (have %s)", ErrNoAction, action, id, strings.Join(ids, ", "))
	}
	h.runSeq++
	runID := id + "-" + strconv.FormatUint(h.runSeq, 10)
	// An async run outlives the request that started it; it ends with the
	// plugin.
	parent := ctx //nolint:contextcheck // see above
	if a.Async {
		parent = l.ctx
	}
	rctx, cancel := context.WithTimeout(parent, a.Timeout())
	l.runs[runID] = &actionRun{info: RunInfo{ID: runID, Action: action, Async: a.Async, Started: time.Now()}, cancel: cancel}
	l.health.Actions.Run++
	h.mu.Unlock()

	c.Trigger, c.Action, c.Run = "action", action, runID
	do := func() (Result, error) {
		defer cancel()
		var out tailBuffer
		code, err := h.run(rctx, l, a.Command, c, &out)
		res := Result{ExitCode: code, Output: out.String(), Run: runID, Async: a.Async}
		h.mu.Lock()
		delete(l.runs, runID)
		var msg string
		switch {
		case errors.Is(rctx.Err(), context.DeadlineExceeded):
			l.health.Actions.TimedOut++
			msg = fmt.Sprintf("action %s timed out after %s", action, a.Timeout())
		case errors.Is(rctx.Err(), context.Canceled):
			l.health.Actions.Canceled++
		case err != nil:
			l.health.Actions.Failed++
			msg = fmt.Sprintf("action %s: %v", action, err)
		}
		changed := false
		if msg != "" {
			changed = h.failLocked(l, msg)
		}
		st := h.statusOfLocked(l)
		h.mu.Unlock()
		if changed {
			h.cfg.Publish(EventChanged, st)
		}
		if err != nil && code == 0 && rctx.Err() == nil {
			return res, err // it did not run at all
		}
		if rctx.Err() != nil && ctx.Err() == nil {
			return res, fmt.Errorf("%s: %w", runID, cmpErr(msg, "canceled"))
		}
		return res, nil
	}
	if a.Async {
		h.running.Go(func() { _, _ = do() })
		return Result{Run: runID, Async: true}, nil
	}
	return do()
}

func cmpErr(msg, fallback string) error {
	return errors.New(cmpStr(msg, fallback))
}

// Cancel stops a run in progress.
func (h *Host) Cancel(id, run string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.plugins[id]
	if l == nil {
		return fmt.Errorf("%w %q", ErrNotFound, id)
	}
	r := l.runs[run]
	if r == nil {
		return fmt.Errorf("%w: %s", ErrNoRun, run)
	}
	r.cancel()
	return nil
}
