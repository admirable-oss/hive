package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/pkg/hiveapi"
)

// Branch is the branch of a worktree Hive made for an agent or a task,
// as the app shows it (hive branch).
type Branch struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Base          string `json:"base"`
	State         string `json:"state"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Path          string `json:"path,omitempty"`
	// Agent and Kind are what it was made for; Task is plan/task.
	Agent      string          `json:"agent,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Task       string          `json:"task,omitempty"`
	Files      int             `json:"files"`
	Added      int             `json:"added"`
	Deleted    int             `json:"deleted"`
	Ahead      int             `json:"ahead"`
	Behind     int             `json:"behind"`
	Dirty      bool            `json:"dirty,omitempty"`
	InProgress bool            `json:"in_progress,omitempty"`
	Checking   bool            `json:"checking,omitempty"`
	Overlaps   []BranchOverlap `json:"overlaps"`
	Gates      []BranchGate    `json:"gates"`
	Comments   int             `json:"comments"`
	Pending    int             `json:"pending"`
	// PullRequest is its pull request's URL, once opened.
	PullRequest string `json:"pull_request,omitempty"`
	Pushed      bool   `json:"pushed,omitempty"`
	// Cleanup says why a merged branch's worktree is still there.
	Cleanup string `json:"cleanup,omitempty"`
	Error   string `json:"error,omitempty"`
	// Machine is the saved machine it is on ("" for this computer).
	Machine string `json:"machine,omitempty"`
}

// BranchOverlap is another branch (or the base) changing the same files.
type BranchOverlap struct {
	Branch    string   `json:"branch"`
	Files     []string `json:"files"`
	Conflicts []string `json:"conflicts"`
}

// BranchGate is how a gate's last run ended.
type BranchGate struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Skipped  bool     `json:"skipped,omitempty"`
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
	Output   []string `json:"output"`
	Duration int64    `json:"duration_ms"`
}

// BranchDiff is a branch's changes against its base, uncommitted ones
// included.
type BranchDiff struct {
	Branch    string     `json:"branch"`
	Base      string     `json:"base"`
	Dirty     bool       `json:"dirty,omitempty"`
	Files     []DiffFile `json:"files"`
	Patch     string     `json:"patch"`
	Truncated bool       `json:"truncated,omitempty"`
}

// DiffFile is one changed file.
type DiffFile struct {
	Path    string `json:"path"`
	From    string `json:"from,omitempty"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

func branchesOf(list []hiveapi.Branch) []Branch {
	out := make([]Branch, 0, len(list))
	for _, b := range list {
		v := Branch{
			ID: b.ID, Name: b.Name, Base: b.Base, State: string(b.State), EnvironmentID: b.EnvironmentID, Path: b.Path,
			Files: b.Files, Added: b.Added, Deleted: b.Deleted, Ahead: b.Ahead, Behind: b.Behind,
			Dirty: b.Dirty, InProgress: b.InProgress, Checking: b.Checking,
			Overlaps: []BranchOverlap{}, Gates: []BranchGate{},
			Comments: len(b.Comments), Pending: b.Pending(), Pushed: b.Pushed != nil, Cleanup: b.Cleanup, Error: b.Error,
		}
		if o := b.Owner; o != nil {
			v.Agent, v.Kind = o.Agent, o.Kind
			if o.Plan != "" {
				v.Task = o.Plan + "/" + o.Task
			}
		}
		if b.PullRequest != nil {
			v.PullRequest = b.PullRequest.URL
		}
		for _, o := range b.Overlaps {
			v.Overlaps = append(v.Overlaps, BranchOverlap{Branch: o.Branch, Files: nonNil(o.Files), Conflicts: nonNil(o.Conflicts)})
		}
		for _, g := range b.Gates {
			v.Gates = append(v.Gates, BranchGate{Name: g.Name, Passed: g.Passed, Skipped: g.Skipped, ExitCode: g.ExitCode, Error: g.Error, Output: nonNil(g.Output), Duration: g.DurationMS})
		}
		out = append(out, v)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// overlapsLocked announces branches that started to overlap since the
// last refresh: once per pair, and not on the first snapshot.
func (h *Hub) overlapsLocked(ws Workspace) []Notification {
	next := map[string]bool{}
	var out []Notification
	for _, b := range ws.Branches {
		for _, o := range b.Overlaps {
			pair := []string{b.Name, o.Branch}
			slices.Sort(pair)
			key := strings.Join(pair, "\x00")
			if len(o.Conflicts) > 0 {
				key += "\x00conflict"
			}
			if next[key] {
				continue
			}
			next[key] = true
			if h.overlaps == nil || h.overlaps[key] || !h.settings.notifies("blocked") || h.settings.SystemNotifications {
				continue
			}
			body := "both change " + strings.Join(o.Files, ", ")
			if len(o.Conflicts) > 0 {
				body = "would conflict in " + strings.Join(o.Conflicts, ", ")
			}
			out = append(out, Notification{ID: "branch:" + key, Title: pair[0] + " · " + pair[1], Body: body})
		}
	}
	h.overlaps = next
	return out
}

// gateTimeout bounds a branch's gates run from the app.
const gateTimeout = 30 * time.Minute

// callFor is call with its own time limit.
func (h *Hub) callFor(d time.Duration, fn func(context.Context, *hiveapi.Client) error) error {
	c, err := h.api()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(h.ctx, d)
	defer cancel()
	return fn(ctx, c)
}

// BranchDiff returns a branch's diff.
func (h *Hub) BranchDiff(ref string) (BranchDiff, error) {
	if err := validID(ref); err != nil {
		return BranchDiff{}, err
	}
	var out BranchDiff
	err := h.call(func(ctx context.Context, c *hiveapi.Client) error {
		d, err := c.BranchDiff(ctx, hiveapi.DiffRequest{Ref: ref})
		if err != nil {
			return err
		}
		out = BranchDiff{Branch: d.Branch, Base: d.Base, Dirty: d.Dirty, Patch: d.Patch, Truncated: d.Truncated, Files: []DiffFile{}}
		for _, f := range d.Files {
			out.Files = append(out.Files, DiffFile{Path: f.Path, From: f.From, Status: f.Status, Added: f.Added, Deleted: f.Deleted})
		}
		return nil
	})
	return out, err
}

// CommentBranch adds a review comment (on a file and line when given);
// send sends the comments not sent yet to the branch's agent.
func (h *Hub) CommentBranch(ref, path string, line int, body string, send bool) error {
	if err := validID(ref); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.CommentBranch(ctx, hiveapi.CommentRequest{Ref: ref, Path: path, Line: line, Body: body, Send: send, By: "desktop"})
		return err
	})
}

// SendComments sends a branch's comments not sent yet to its agent.
func (h *Hub) SendComments(ref string) error {
	if err := validID(ref); err != nil {
		return err
	}
	return h.call(func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.SendComments(ctx, ref)
		return err
	})
}

// ReadyBranch runs a branch's gates and says whether it is ready.
func (h *Hub) ReadyBranch(ref string) (bool, error) {
	if err := validID(ref); err != nil {
		return false, err
	}
	ready := false
	err := h.callFor(gateTimeout, func(ctx context.Context, c *hiveapi.Client) error {
		b, err := c.ReadyBranch(ctx, ref, nil)
		ready = b.State == hiveapi.BranchReady
		return err
	})
	return ready, err
}

// PushBranch pushes a branch to its remote, as the person asked.
func (h *Hub) PushBranch(ref string) error {
	if err := validID(ref); err != nil {
		return err
	}
	return h.callFor(5*time.Minute, func(ctx context.Context, c *hiveapi.Client) error {
		_, err := c.PushBranch(ctx, ref, "")
		return err
	})
}

// OpenPullRequest opens a pull request for a branch, pushing it first
// when push is set, and returns its URL.
func (h *Hub) OpenPullRequest(ref string, draft, push bool) (string, error) {
	if err := validID(ref); err != nil {
		return "", err
	}
	url := ""
	err := h.callFor(5*time.Minute, func(ctx context.Context, c *hiveapi.Client) error {
		b, err := c.OpenPullRequest(ctx, hiveapi.PullRequestRequest{Ref: ref, Draft: draft, Push: push})
		if err == nil && b.PullRequest != nil {
			url = b.PullRequest.URL
		}
		return err
	})
	return url, err
}

// CleanBranch removes a branch's worktree; force also with uncommitted
// changes, which are saved under refs/hive/saved/ first.
func (h *Hub) CleanBranch(ref string, force bool) error {
	if err := validID(ref); err != nil {
		return err
	}
	return h.callFor(2*time.Minute, func(ctx context.Context, c *hiveapi.Client) error {
		list, err := c.CleanBranches(ctx, hiveapi.CleanRequest{Ref: ref, Force: force})
		if err == nil && len(list) > 0 && list[0].Saved != "" {
			h.log.Info("uncommitted work saved", "branch", list[0].Name, "ref", list[0].Saved)
		}
		if err != nil {
			return fmt.Errorf("clean up %s: %w", ref, err)
		}
		return nil
	})
}
