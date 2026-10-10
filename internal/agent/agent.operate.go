package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/admirable-oss/hive/internal/pane"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// Operating agents: prompting them, waiting for them, reading them and
// typing into them. These work on the agent's terminal, so they work for
// agents with or without a pane.

var (
	// ErrBlocked means the agent waits for a decision; prompting it would
	// answer that decision by accident.
	ErrBlocked = errors.New("agent: blocked on a decision")
	// ErrNotRunning means the agent's process has exited.
	ErrNotRunning = errors.New("agent: not running")
)

// Until is what a wait waits for.
type Until string

const (
	// UntilIdle waits until the agent is ready for a prompt (idle or done).
	UntilIdle Until = "idle"
	// UntilDone waits until the agent finishes a turn: its completion count
	// passes After (or, without After, the one it had, unless it is done
	// already).
	UntilDone    Until = "done"
	UntilWorking Until = "working"
	UntilBlocked Until = "blocked"
	UntilExited  Until = "exited"
	// UntilChange waits for any state change.
	UntilChange Until = "change"
)

var untils = []Until{UntilIdle, UntilDone, UntilWorking, UntilBlocked, UntilExited, UntilChange}

// Outcome is how a wait or prompt ended.
type Outcome string

const (
	OutcomeReached Outcome = "reached" // what was waited for happened
	OutcomeStarted Outcome = "started" // a prompt was taken; nobody waited for the result
	OutcomeBlocked Outcome = "blocked" // the agent needs a decision first
	OutcomeExited  Outcome = "exited"
	OutcomeTimeout Outcome = "timeout"
	// OutcomeStalled means the agent did not react to a prompt, or stopped
	// changing its screen while working, for longer than allowed.
	OutcomeStalled Outcome = "stalled"
)

// Defaults for prompts and waits.
const (
	// DefaultStartTimeout is how long an agent gets to react to a prompt,
	// or to become ready after starting.
	DefaultStartTimeout = 20 * time.Second
	// DefaultStallTimeout is how long a working agent may leave its screen
	// unchanged before it counts as stalled.
	DefaultStallTimeout = 10 * time.Minute
	// DefaultSubmitDelay separates a pasted prompt from the key that sends
	// it: terminal UIs need a moment to take the paste in.
	DefaultSubmitDelay = 150 * time.Millisecond
	// DefaultSettle is how long an agent's screen must be still before a
	// prompt goes in: an agent that just started, or just closed a dialog,
	// draws for a while before it reads its input, and a prompt typed
	// meanwhile is lost.
	DefaultSettle = time.Second
	// DefaultEchoWait bounds the wait for a pasted prompt to show on the
	// agent's screen before the submit key is pressed.
	DefaultEchoWait = 3 * time.Second
	// waitPoll re-checks waits for time-based conditions (stalls).
	waitPoll = 250 * time.Millisecond
	// inputGrace is how long a state that began before the last input
	// typed into an agent is not trusted: its screen takes a moment to
	// catch up (a dialog just answered still shows).
	inputGrace = 2 * time.Second
)

// settled reports whether an agent's screen has been still for the settle
// time.
func (s *Service) settled(a Agent) bool {
	if s.cfg.Settle < 0 {
		return true
	}
	last := a.LastOutput
	if a.Since.After(last) {
		last = a.Since
	}
	return s.now().Sub(last) >= s.cfg.Settle
}

// noteInput records that input was typed into an agent.
func (s *Service) noteInput(procID string) {
	s.mu.Lock()
	s.typed[procID] = s.now()
	s.mu.Unlock()
}

// stale reports whether a's state predates input typed into it moments
// ago, so its screen may not show the effect yet.
func (s *Service) stale(a Agent) bool {
	s.mu.Lock()
	t := s.typed[a.ID]
	s.mu.Unlock()
	return !t.IsZero() && t.After(a.Since) && s.now().Sub(t) < inputGrace
}

// WaitRequest says what to wait for.
type WaitRequest struct {
	Until Until `json:"until"`
	// After is a completion count seen earlier: UntilDone waits for a
	// completion after it.
	After *uint64 `json:"after,omitempty"`
	// Timeout bounds the wait; 0 waits until the caller gives up.
	Timeout time.Duration `json:"-"`
	// Stall ends the wait when the agent works without changing its screen
	// for this long; 0 never.
	Stall time.Duration `json:"-"`
}

// WaitResult is how a wait ended.
type WaitResult struct {
	Outcome Outcome `json:"outcome"`
	Agent   Agent   `json:"agent"`
}

// PromptRequest sends an agent a prompt.
type PromptRequest struct {
	Text string `json:"text"`
	// Wait waits for the agent to finish the turn the prompt started.
	Wait bool `json:"wait,omitempty"`
	// Until is what Wait waits for (default done).
	Until Until `json:"until,omitempty"`
	// Timeout bounds the whole prompt, waiting included; 0 none.
	Timeout time.Duration `json:"-"`
	// StartTimeout bounds how long the agent gets to react (default
	// DefaultStartTimeout); StallTimeout how long it may work silently
	// (default DefaultStallTimeout; negative never).
	StartTimeout time.Duration `json:"-"`
	StallTimeout time.Duration `json:"-"`
	// Read, when positive, returns that many of the agent's last lines
	// once the prompt ends.
	Read int `json:"read,omitempty"`
}

// PromptResult is how a prompt ended.
type PromptResult struct {
	Outcome Outcome  `json:"outcome"`
	Agent   Agent    `json:"agent"`
	Output  []string `json:"output,omitempty"`
}

// snapshotOf returns the agent's state as tracked, and a channel closed at
// the next state change. known is false for a process that is not tracked.
func (s *Service) snapshotOf(procID string) (a Agent, known bool, changed <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.trackers[procID]
	if t == nil || !t.resolved {
		return Agent{ID: procID, Status: Status{State: StateUnknown}}, false, s.changed
	}
	return t.agent(), true, s.changed
}

// await calls check on every state change (and every waitPoll, for
// conditions on time) until it reports an outcome or ctx ends.
func (s *Service) await(ctx context.Context, procID string, check func(a Agent, known bool) (Outcome, bool)) (Outcome, error) {
	tick := time.NewTicker(waitPoll)
	defer tick.Stop()
	for {
		a, known, changed := s.snapshotOf(procID)
		if !known {
			if ended, err := s.ended(ctx, procID); err != nil {
				return "", err
			} else if ended {
				return OutcomeExited, nil
			}
		}
		if o, done := check(a, known); done {
			return o, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		case <-tick.C:
		}
	}
}

// ended reports whether an untracked process has exited.
func (s *Service) ended(ctx context.Context, procID string) (bool, error) {
	p, err := s.procs.Get(ctx, procID)
	if err != nil {
		return false, fmt.Errorf("%w: %q", ErrNotFound, procID)
	}
	return !p.Active(), nil
}

// withTimeout bounds ctx by d (none for d ≤ 0) and reports, after the
// fact, whether that bound is what ended it.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc, func(error) bool) {
	if d <= 0 {
		ctx, cancel := context.WithCancel(ctx)
		return ctx, cancel, func(error) bool { return false }
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, d)
	return ctx, cancel, func(err error) bool {
		return errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil
	}
}

// Wait waits for an agent (by process or pane ID) to reach a state. It
// also ends when the agent blocks on a decision (unless that is what it
// waits for), exits, stalls, or the timeout passes; the outcome says which.
func (s *Service) Wait(ctx context.Context, id string, req WaitRequest) (WaitResult, error) {
	if req.Until == "" {
		req.Until = UntilIdle
	}
	if !slices.Contains(untils, req.Until) {
		return WaitResult{}, fmt.Errorf("%w: until %q (want one of %v)", ErrInvalid, req.Until, untils)
	}
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return WaitResult{}, err
	}
	start, _, _ := s.snapshotOf(procID)
	after := start.CompletionSeq
	if req.After != nil {
		after = *req.After
	} else if start.State == StateDone {
		after = start.CompletionSeq - 1 // done already: that completion counts
	}
	wctx, cancel, timedOut := withTimeout(ctx, req.Timeout)
	defer cancel()
	o, err := s.await(wctx, procID, func(a Agent, _ bool) (Outcome, bool) {
		if s.stale(a) && a.State != StateExited {
			return "", false // let the screen catch up with what was typed
		}
		reached := false
		switch req.Until {
		case UntilIdle:
			reached = a.State == StateIdle || a.State == StateDone
		case UntilDone:
			reached = a.CompletionSeq > after
		case UntilChange:
			reached = a.StateSeq > start.StateSeq
		default:
			reached = string(a.State) == string(req.Until)
		}
		switch {
		case reached:
			return OutcomeReached, true
		case a.State == StateExited:
			return OutcomeExited, true
		case a.State == StateBlocked:
			return OutcomeBlocked, true
		case stalled(a, req.Stall, s.now()):
			return OutcomeStalled, true
		}
		return "", false
	})
	if timedOut(err) {
		o, err = OutcomeTimeout, nil
	}
	if err != nil {
		return WaitResult{}, err
	}
	a, err := s.Get(ctx, procID)
	return WaitResult{Outcome: o, Agent: a}, err
}

// stalled reports whether a working agent has not changed its screen for
// longer than limit.
func stalled(a Agent, limit time.Duration, now time.Time) bool {
	if limit <= 0 || a.State != StateWorking {
		return false
	}
	last := a.LastOutput
	if last.IsZero() || a.Since.After(last) {
		last = a.Since
	}
	return now.Sub(last) > limit
}

// Prompt types a prompt into an agent and sends it: pasted (bracketed, when
// the agent asks for that, so newlines do not send it early), then its
// manifest's submit key. Prompts to one agent never interleave. It refuses
// an agent blocked on a decision, waits for a starting agent to become
// ready, and reports a stall when the agent does not react. With Wait, it
// then waits for the turn to end.
func (s *Service) Prompt(ctx context.Context, id string, req PromptRequest) (PromptResult, error) {
	if req.Text == "" {
		return PromptResult{}, fmt.Errorf("%w: the prompt is empty", ErrInvalid)
	}
	if req.Until == "" {
		req.Until = UntilDone
	}
	if req.Until != UntilDone && req.Until != UntilIdle {
		return PromptResult{}, fmt.Errorf("%w: a prompt waits until done or idle, not %q", ErrInvalid, req.Until)
	}
	if req.StartTimeout <= 0 {
		req.StartTimeout = DefaultStartTimeout
	}
	switch {
	case req.StallTimeout == 0:
		req.StallTimeout = DefaultStallTimeout
	case req.StallTimeout < 0:
		req.StallTimeout = 0
	}
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return PromptResult{}, err
	}
	sess, err := s.terms.Get(procID)
	if err != nil {
		return PromptResult{}, fmt.Errorf("%w: %q has no running terminal", ErrNotRunning, id)
	}

	unlock := s.lockPrompt(procID)
	defer unlock()
	ctx, cancel, timedOut := withTimeout(ctx, req.Timeout)
	defer cancel()
	finish := func(o Outcome, err error) (PromptResult, error) {
		if timedOut(err) {
			o, err = OutcomeTimeout, nil
		}
		if err != nil {
			return PromptResult{}, err
		}
		res := PromptResult{Outcome: o}
		bg := context.WithoutCancel(ctx)
		if res.Agent, err = s.Get(bg, procID); err != nil {
			return PromptResult{}, err
		}
		if req.Read > 0 {
			lines, _ := sess.Read(bg, terminal.ReadRequest{Source: terminal.SourceRecent, Lines: terminal.MaxReadLines})
			res.Output = Tail(Compact(lines), req.Read)
		}
		return res, nil
	}

	// A starting agent gets time to become ready.
	o, err := s.await(ctx, procID, startedCheck(s, req.StartTimeout, func(a Agent) bool {
		// Blocked and exited are refused below at once; idle ones wait for
		// their screen to settle.
		busy := a.State == StateWorking || a.State == StateBlocked || a.State == StateExited
		return a.State != StateUnknown && !s.stale(a) && (busy || s.settled(a))
	}))
	if err != nil || o != OutcomeReached {
		return finish(o, err)
	}
	before, _, _ := s.snapshotOf(procID)
	switch before.State {
	case StateBlocked:
		return PromptResult{}, fmt.Errorf("%w: answer it first (hive agent read %s)", ErrBlocked, id)
	case StateExited:
		return PromptResult{}, fmt.Errorf("%w: %q", ErrNotRunning, id)
	}

	if err := s.send(ctx, procID, sess, req.Text, s.manifestOf(procID)); err != nil {
		return PromptResult{}, err
	}

	// It reacts: works, asks, or finishes at once.
	busy := before.State == StateWorking
	o, err = s.await(ctx, procID, startedCheck(s, req.StartTimeout, func(a Agent) bool {
		return busy || a.CompletionSeq > before.CompletionSeq ||
			a.StateSeq > before.StateSeq && (a.State == StateWorking || a.State == StateBlocked || a.State == StateDone)
	}))
	if err != nil || o != OutcomeReached {
		return finish(o, err)
	}
	if !req.Wait {
		return finish(OutcomeStarted, nil)
	}
	o, err = s.await(ctx, procID, func(a Agent, _ bool) (Outcome, bool) {
		switch {
		case a.CompletionSeq > before.CompletionSeq && req.Until == UntilDone,
			req.Until == UntilIdle && (a.State == StateIdle || a.State == StateDone):
			return OutcomeReached, true
		case a.State == StateExited:
			return OutcomeExited, true
		case a.State == StateBlocked:
			return OutcomeBlocked, true
		case stalled(a, req.StallTimeout, s.now()):
			return OutcomeStalled, true
		}
		return "", false
	})
	return finish(o, err)
}

// startedCheck is a check that is reached when ok holds, ends on a block or
// exit, and stalls when nothing happens within limit.
func startedCheck(s *Service, limit time.Duration, ok func(Agent) bool) func(Agent, bool) (Outcome, bool) {
	deadline := s.now().Add(limit)
	return func(a Agent, _ bool) (Outcome, bool) {
		switch {
		case ok(a):
			return OutcomeReached, true
		case s.stale(a) && a.State != StateExited:
			return "", false
		case a.State == StateExited:
			return OutcomeExited, true
		case a.State == StateBlocked:
			return OutcomeBlocked, true
		case s.now().After(deadline):
			return OutcomeStalled, true
		}
		return "", false
	}
}

// send pastes text into a terminal, then presses the submit key.
func (s *Service) send(ctx context.Context, procID string, sess terminal.Session, text string, m *Manifest) error {
	scr, err := sess.Snapshot(ctx)
	if err != nil {
		return err
	}
	data := text
	if scr.Modes&vt.ModeBracketedPaste != 0 {
		data = "\x1b[200~" + text + "\x1b[201~"
	}
	submit, delay := "Enter", DefaultSubmitDelay
	if m != nil {
		if m.Start.Submit != "" {
			submit = m.Start.Submit
		}
		if m.Start.SubmitDelayMS > 0 {
			delay = time.Duration(m.Start.SubmitDelayMS) * time.Millisecond
		}
	}
	key, err := pane.EncodeKeys([]string{submit}, scr.Modes)
	if err != nil {
		return fmt.Errorf("manifest submit key %q: %w", submit, err)
	}
	before := s.screenChanges(procID)
	if _, err := sess.Write([]byte(data)); err != nil {
		return err
	}
	defer s.noteInput(procID)
	// The paste shows on the screen, then a moment later Enter sends it.
	echo := time.NewTimer(s.cfg.EchoWait)
	defer echo.Stop()
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
echoed:
	for s.screenChanges(procID) == before {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-echo.C:
			s.log.Debug("prompt not echoed; submitting anyway", "agent", procID)
			break echoed
		case <-poll.C:
		}
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
	}
	_, err = sess.Write(key)
	return err
}

// lockPrompt serialises prompts to one agent.
func (s *Service) lockPrompt(procID string) func() {
	s.mu.Lock()
	l := s.prompting[procID]
	if l == nil {
		l = &sync.Mutex{}
		s.prompting[procID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// screenChanges counts the changes to an agent's screen seen so far.
func (s *Service) screenChanges(procID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.trackers[procID]; t != nil {
		return t.changes
	}
	return 0
}

func (s *Service) manifestOf(procID string) *Manifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.trackers[procID]; t != nil {
		return t.manifest
	}
	return nil
}

// Compact drops trailing spaces, and the blank rows full-screen agents
// leave between their output and their input box: runs of blank lines
// become one, and leading and trailing ones go.
func Compact(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// Tail returns the last n lines.
func Tail(lines []string, n int) []string {
	if n > 0 && len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// Read returns an agent's text (see terminal.ReadRequest).
func (s *Service) Read(ctx context.Context, id string, req terminal.ReadRequest) ([]string, error) {
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return nil, err
	}
	sess, err := s.terms.Get(procID)
	if err != nil {
		return nil, fmt.Errorf("%w: %q has no running terminal", ErrNotRunning, id)
	}
	return sess.Read(ctx, req)
}

// SendKeys types named keys into an agent (see pane.EncodeKeys).
func (s *Service) SendKeys(ctx context.Context, id string, keys []string) error {
	procID, err := s.processOf(ctx, id)
	if err != nil {
		return err
	}
	sess, err := s.terms.Get(procID)
	if err != nil {
		return fmt.Errorf("%w: %q has no running terminal", ErrNotRunning, id)
	}
	scr, err := sess.Snapshot(ctx)
	if err != nil {
		return err
	}
	data, err := pane.EncodeKeys(keys, scr.Modes)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	_, err = sess.Write(data)
	s.noteInput(procID)
	return err
}
