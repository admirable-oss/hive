package runtime

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/branch"
	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/issue"
	"github.com/admirable-oss/hive/internal/task"
)

type onePlan task.Plan

func (p onePlan) Get(_ context.Context, id string) (task.Plan, error) {
	if id != p.ID {
		return task.Plan{}, task.ErrNotFound
	}
	return task.Plan(p), nil
}

type closingTracker struct {
	mu     sync.Mutex
	state  string
	closed []string
}

func (c *closingTracker) Get(context.Context, string) (issue.Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return issue.Issue{Title: "t", State: c.state}, nil
}
func (c *closingTracker) Search(context.Context, string, int) ([]issue.Issue, error) { return nil, nil }
func (c *closingTracker) Comment(context.Context, string, string) error              { return nil }
func (c *closingTracker) Close(_ context.Context, key, comment string) (issue.Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state, c.closed = "closed", append(c.closed, key+": "+comment)
	return issue.Issue{Title: "t", State: c.state}, nil
}

func (c *closingTracker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.closed)
}

type oneTracker struct{ t issue.Tracker }

func (o oneTracker) Tracker(name string) (issue.Tracker, bool) { return o.t, name == "linear" }
func (o oneTracker) Names() []string                           { return []string{"linear"} }

// TestCloseMergedIssues: when a task's branch merges and the task says
// close_issue, its issue is closed once, naming the pull request; other
// branches close nothing.
func TestCloseMergedIssues(t *testing.T) {
	tr := &closingTracker{state: "open"}
	bus := event.NewBus(slog.New(slog.NewTextHandler(io.Discard, nil)))
	links := &issueLinks{issues: issue.NewService(nil, oneTracker{tr}, bus), tasks: onePlan{ID: "p1", Tasks: []task.Task{
		{Spec: task.Spec{ID: "fix", Issue: "linear:HIV-42", CloseIssue: true}},
		{Spec: task.Spec{ID: "keep", Issue: "linear:HIV-43"}},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		closeMergedIssues(ctx, bus, links, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(50 * time.Millisecond) // subscribed

	merged := func(taskID string) branch.Branch {
		return branch.Branch{Name: "hive/p1/" + taskID, State: branch.StateMerged, Owner: &branch.Owner{Plan: "p1", Task: taskID},
			PullRequest: &branch.PullRequest{URL: "https://github.com/acme/app/pull/7"}}
	}
	bus.Publish(branch.EventUpdated, merged("keep"))
	bus.Publish(branch.EventUpdated, branch.Branch{Name: "hive/p1/fix", State: branch.StateReady, Owner: &branch.Owner{Plan: "p1", Task: "fix"}})
	bus.Publish(branch.EventUpdated, merged("fix"))
	bus.Publish(branch.EventUpdated, merged("fix"))
	deadline := time.Now().Add(5 * time.Second)
	for tr.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if tr.count() != 1 || tr.closed[0] != "HIV-42: Merged: hive/p1/fix (https://github.com/acme/app/pull/7), by a task in Hive." {
		t.Fatalf("closed: %q", tr.closed)
	}
}
