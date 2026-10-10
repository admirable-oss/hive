package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/terminal"
)

// bracketed turns on bracketed paste, as Claude Code does.
const bracketed = "\x1b[?2004h"

// promptAsync runs a prompt in the background.
func (h *harness) promptAsync(id string, req PromptRequest) <-chan PromptResult {
	h.t.Helper()
	out := make(chan PromptResult, 1)
	go func() {
		res, err := h.svc.Prompt(context.Background(), id, req)
		if err != nil {
			h.t.Errorf("prompt: %v", err)
		}
		out <- res
	}()
	return out
}

func (h *harness) waitState(id string, want State) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, _, _ := h.svc.snapshotOf(id)
		if a.State == want {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s never became %s (is %s)", id, want, a.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPromptPastesSubmitsAndWaitsForTheTurn(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, bracketed+claudeIdle)
	h.onInput = func(id, data string) {
		if data == "\r" {
			h.show(id, claudeWorking) // it takes the prompt
		}
	}
	done := h.promptAsync("pane-1", PromptRequest{Text: "fix the tests\nall of them", Wait: true, Read: 5})

	h.waitState("p1", StateWorking)
	if got, want := h.input("p1"), "\x1b[200~fix the tests\nall of them\x1b[201~\r"; got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
	h.show("p1", claudeIdle)
	res := <-done
	if res.Outcome != OutcomeReached || res.Agent.State != StateDone || res.Agent.CompletionSeq != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(strings.Join(res.Output, "\n"), "Claude Code") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestPromptWithoutWaitReturnsOnceStarted(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	h.onInput = func(id, data string) {
		if data == "\r" {
			h.show(id, claudeWorking)
		}
	}
	res := <-h.promptAsync("p1", PromptRequest{Text: "go"})
	if res.Outcome != OutcomeStarted || res.Agent.State != StateWorking {
		t.Fatalf("result = %+v", res)
	}
	if got := h.input("p1"); got != "go\r" { // no bracketed paste mode: plain text
		t.Fatalf("typed %q", got)
	}
}

func TestPromptRefusesABlockedAgent(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeBlocked)
	_, err := h.svc.Prompt(context.Background(), "p1", PromptRequest{Text: "yes"})
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if h.input("p1") != "" {
		t.Fatal("typed into a blocked agent")
	}
}

func TestPromptEndsWhenTheAgentAsksOrStalls(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	h.onInput = func(id, data string) {
		if data == "\r" {
			h.show(id, claudeBlocked) // a permission dialog
		}
	}
	if res := <-h.promptAsync("p1", PromptRequest{Text: "mkdir build", Wait: true}); res.Outcome != OutcomeBlocked {
		t.Fatalf("result = %+v, want blocked", res)
	}

	// An agent that ignores the prompt stalls once the start timeout passes.
	h.start("p2", []string{"claude"}, nil, claudeIdle)
	h.onInput = nil
	done := h.promptAsync("p2", PromptRequest{Text: "hello", StartTimeout: time.Minute})
	for len(h.input("p2")) < len("hello\r") {
		time.Sleep(5 * time.Millisecond)
	}
	for {
		h.advance(2 * time.Minute) // until the prompt notices: it may still be typing
		select {
		case res := <-done:
			if res.Outcome != OutcomeStalled {
				t.Fatalf("result = %+v, want stalled", res)
			}
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestPromptTimesOut(t *testing.T) {
	h := newHarness(t)
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	h.onInput = func(id, data string) {
		if data == "\r" {
			h.show(id, claudeWorking)
		}
	}
	res := <-h.promptAsync("p1", PromptRequest{Text: "go", Wait: true, Timeout: 300 * time.Millisecond})
	if res.Outcome != OutcomeTimeout || res.Agent.State != StateWorking {
		t.Fatalf("result = %+v, want timeout while working", res)
	}
}

func TestWait(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.start("p1", []string{"claude"}, nil, claudeIdle)

	// Ready already.
	if res, err := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilIdle}); err != nil || res.Outcome != OutcomeReached {
		t.Fatalf("idle: %+v %v", res, err)
	}
	// A timeout is an outcome, not an error.
	if res, err := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilWorking, Timeout: 50 * time.Millisecond}); err != nil || res.Outcome != OutcomeTimeout {
		t.Fatalf("working: %+v %v", res, err)
	}

	// Done waits for the next completion.
	got := make(chan WaitResult, 1)
	go func() {
		res, err := h.svc.Wait(ctx, "pane-1", WaitRequest{Until: UntilDone})
		if err != nil {
			t.Error(err)
		}
		got <- res
	}()
	h.show("p1", claudeWorking)
	h.show("p1", claudeIdle)
	if res := <-got; res.Outcome != OutcomeReached || res.Agent.State != StateDone {
		t.Fatalf("done: %+v", res)
	}
	// Done already and not seen: that completion counts …
	if res, _ := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilDone}); res.Outcome != OutcomeReached {
		t.Fatalf("done again: %+v", res)
	}
	// … unless the caller saw it.
	seen := uint64(1)
	if res, _ := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilDone, After: &seen, Timeout: 50 * time.Millisecond}); res.Outcome != OutcomeTimeout {
		t.Fatalf("after 1: %+v", res)
	}

	// Waiting for done ends early on a decision.
	h.show("p1", claudeBlocked)
	if res, _ := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilDone, After: &seen}); res.Outcome != OutcomeBlocked {
		t.Fatalf("blocked: %+v", res)
	}
	if _, err := h.svc.Wait(ctx, "p1", WaitRequest{Until: "soon"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad until: %v", err)
	}
}

func TestSendKeysAndRead(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.start("p1", []string{"claude"}, nil, claudeBlocked)
	if err := h.svc.SendKeys(ctx, "pane-1", []string{"Down", "Enter"}); err != nil {
		t.Fatal(err)
	}
	if got := h.input("p1"); got != "\x1b[B\r" {
		t.Fatalf("typed %q", got)
	}
	if err := h.svc.SendKeys(ctx, "p1", []string{"Nope"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad key: %v", err)
	}
	lines, err := h.svc.Read(ctx, "p1", terminal.ReadRequest{})
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "Do you want to proceed?") {
		t.Fatalf("read: %q %v", lines, err)
	}
}

func TestAnsweringADialogIsNotMistakenForBlocked(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.start("p1", []string{"claude"}, nil, claudeBlocked)
	h.advance(time.Second)
	// Keys answer the dialog; the screen has not caught up yet.
	if err := h.svc.SendKeys(ctx, "p1", []string{"Enter"}); err != nil {
		t.Fatal(err)
	}
	got := make(chan WaitResult, 1)
	go func() {
		res, err := h.svc.Wait(ctx, "p1", WaitRequest{Until: UntilDone})
		if err != nil {
			t.Error(err)
		}
		got <- res
	}()
	select {
	case res := <-got:
		t.Fatalf("the wait ended on the answered dialog: %+v", res)
	case <-time.After(100 * time.Millisecond):
	}
	h.show("p1", claudeIdle) // the dialog closes: done
	if res := <-got; res.Outcome != OutcomeReached {
		t.Fatalf("result = %+v", res)
	}
}

func TestPromptWaitsForTheScreenToSettle(t *testing.T) {
	h := newHarness(t)
	h.svc.cfg.Settle = time.Second
	h.start("p1", []string{"claude"}, nil, claudeIdle) // it just drew its screen
	h.onInput = func(id, data string) {
		if data == "\r" {
			h.show(id, claudeWorking)
		}
	}
	done := h.promptAsync("p1", PromptRequest{Text: "go"})
	time.Sleep(400 * time.Millisecond) // several checks: still drawing, as far as it knows
	if got := h.input("p1"); got != "" {
		t.Fatalf("typed %q into a screen that had not settled", got)
	}
	h.advance(2 * time.Second)
	if res := <-done; res.Outcome != OutcomeStarted || h.input("p1") != "go\r" {
		t.Fatalf("after settling: %+v, typed %q", res, h.input("p1"))
	}
}

func TestPromptSubmitsOnceThePasteShows(t *testing.T) {
	h := newHarness(t)
	h.svc.cfg.EchoWait = time.Minute
	h.start("p1", []string{"claude"}, nil, claudeIdle)
	h.onInput = func(id, data string) {
		switch data {
		case "\r":
			h.show(id, claudeWorking)
		default:
			h.show(id, "\x1b[18;3H"+data) // the paste shows in the prompt box
		}
	}
	start := time.Now()
	res := <-h.promptAsync("p1", PromptRequest{Text: "go"})
	if res.Outcome != OutcomeStarted || time.Since(start) > 10*time.Second {
		t.Fatalf("result %+v after %s", res, time.Since(start))
	}
}
