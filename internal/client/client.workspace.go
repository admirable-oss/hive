package client

import (
	"context"
	"time"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/layout"
	"github.com/admirable-oss/hive/internal/pane"
)

// Workspace is the typed tab, pane, layout and worktree API (tab.*, pane.*,
// layout.*, worktree.*) over any Client.
type Workspace struct {
	c Client
}

// NewWorkspace wraps c.
func NewWorkspace(c Client) Workspace { return Workspace{c: c} }

// ReadRequest selects what PaneRead returns. Source is visible (the
// screen, default), recent (screen plus the last scrolled-off lines),
// recent-unwrapped (the same, with soft-wrapped lines joined) or history.
type ReadRequest struct {
	Source string `json:"source,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	ANSI   bool   `json:"ansi,omitempty"`
}

// WaitRequest waits for a line matching Pattern (a Go regular expression).
// Without Anywhere, only output that appears after the call counts.
type WaitRequest struct {
	Pattern  string `json:"pattern"`
	Anywhere bool   `json:"anywhere,omitempty"`
}

// TabCreated is what TabCreate returns.
type TabCreated struct {
	Tab  pane.Tab  `json:"tab"`
	Pane pane.Pane `json:"pane"`
}

// WorktreeInfo is a worktree of a repository and the environments in it.
type WorktreeInfo struct {
	git.Worktree
	Environments []string `json:"environments,omitempty"`
}

// Worktree request types; see the daemon's worktree.* methods.
type (
	WorktreeCreateRequest struct {
		Repo   string            `json:"repo"`
		Branch string            `json:"branch"`
		Base   string            `json:"base,omitempty"`
		ID     string            `json:"id,omitempty"`
		Env    map[string]string `json:"env,omitempty"`
	}
	WorktreeOpenRequest struct {
		Path string            `json:"path"`
		ID   string            `json:"id,omitempty"`
		Env  map[string]string `json:"env,omitempty"`
	}
	WorktreeRemoveRequest struct {
		ID    string `json:"id,omitempty"`
		Path  string `json:"path,omitempty"`
		Force bool   `json:"force,omitempty"`
	}
)

type wsID struct {
	ID string `json:"id"`
}

type wsList struct {
	EnvironmentID string `json:"environment_id,omitempty"`
	TabID         string `json:"tab_id,omitempty"`
}

func wsCall[R any](ctx context.Context, w Workspace, method string, params any) (R, error) {
	var r R
	err := w.c.Call(ctx, method, params, &r)
	return r, err
}

// --- tabs ---

func (w Workspace) TabList(ctx context.Context, envID string) ([]pane.Tab, error) {
	return wsCall[[]pane.Tab](ctx, w, "tab.list", wsList{EnvironmentID: envID})
}

func (w Workspace) TabCreate(ctx context.Context, req pane.CreateTabRequest) (TabCreated, error) {
	return wsCall[TabCreated](ctx, w, "tab.create", req)
}

func (w Workspace) TabGet(ctx context.Context, id string) (pane.Tab, error) {
	return wsCall[pane.Tab](ctx, w, "tab.get", wsID{id})
}

func (w Workspace) TabRename(ctx context.Context, id, name string) (pane.Tab, error) {
	return wsCall[pane.Tab](ctx, w, "tab.rename", map[string]string{"id": id, "name": name})
}

func (w Workspace) TabFocus(ctx context.Context, id string) (pane.Tab, error) {
	return wsCall[pane.Tab](ctx, w, "tab.focus", wsID{id})
}

func (w Workspace) TabResize(ctx context.Context, id string, width, height int) (pane.Tab, error) {
	return wsCall[pane.Tab](ctx, w, "tab.resize", map[string]any{"id": id, "width": width, "height": height})
}

func (w Workspace) TabClose(ctx context.Context, id string) error {
	return w.c.Call(ctx, "tab.close", wsID{id}, nil)
}

// --- panes ---

func (w Workspace) PaneList(ctx context.Context, envID, tabID string) ([]pane.Pane, error) {
	return wsCall[[]pane.Pane](ctx, w, "pane.list", wsList{EnvironmentID: envID, TabID: tabID})
}

func (w Workspace) PaneGet(ctx context.Context, id string) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.get", wsID{id})
}

func (w Workspace) PaneSplit(ctx context.Context, req pane.SplitRequest) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.split", req)
}

func (w Workspace) PanePopup(ctx context.Context, req pane.PopupRequest) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.popup", req)
}

// PaneFocus focuses id, or with a direction the neighbour of id that way.
func (w Workspace) PaneFocus(ctx context.Context, id string, d layout.Direction) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.focus", map[string]any{"id": id, "direction": d})
}

func (w Workspace) PaneResize(ctx context.Context, id string, d layout.Direction, cells int) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.resize", map[string]any{"id": id, "direction": d, "cells": cells})
}

// PaneZoom sets the zoom (on nil: toggles it).
func (w Workspace) PaneZoom(ctx context.Context, id string, on *bool) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.zoom", map[string]any{"id": id, "on": on})
}

func (w Workspace) PaneSwap(ctx context.Context, id, with string) error {
	return w.c.Call(ctx, "pane.swap", map[string]string{"id": id, "with": with}, nil)
}

func (w Workspace) PaneMove(ctx context.Context, req pane.MoveRequest) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.move", req)
}

func (w Workspace) PaneRename(ctx context.Context, id, name string) (pane.Pane, error) {
	return wsCall[pane.Pane](ctx, w, "pane.rename", map[string]string{"id": id, "name": name})
}

func (w Workspace) PaneClose(ctx context.Context, id string) error {
	return w.c.Call(ctx, "pane.close", wsID{id}, nil)
}

func (w Workspace) PaneInput(ctx context.Context, id string, data []byte) error {
	return w.c.Call(ctx, "pane.input", map[string]any{"id": id, "data": data}, nil)
}

// PaneSendText types text; with paste it arrives as one bracketed paste.
func (w Workspace) PaneSendText(ctx context.Context, id, text string, paste bool) error {
	return w.c.Call(ctx, "pane.send_text", map[string]any{"id": id, "text": text, "paste": paste}, nil)
}

// PaneSendKeys presses named keys (Enter, C-c, Up, F5, …).
func (w Workspace) PaneSendKeys(ctx context.Context, id string, keys []string) error {
	return w.c.Call(ctx, "pane.send_keys", map[string]any{"id": id, "keys": keys}, nil)
}

// PaneRun types a command line and presses Enter.
func (w Workspace) PaneRun(ctx context.Context, id, command string) error {
	return w.c.Call(ctx, "pane.run", map[string]string{"id": id, "command": command}, nil)
}

func (w Workspace) PaneRead(ctx context.Context, id string, req ReadRequest) ([]string, error) {
	return wsCall[[]string](ctx, w, "pane.read", struct {
		ID string `json:"id"`
		ReadRequest
	}{id, req})
}

// PaneWaitOutput waits up to timeout (0: until ctx ends) for a matching
// line and returns it. Running out of time is a protocol error with code
// timeout.
func (w Workspace) PaneWaitOutput(ctx context.Context, id string, req WaitRequest, timeout time.Duration) (string, error) {
	r, err := wsCall[map[string]string](ctx, w, "pane.wait_output", struct {
		ID string `json:"id"`
		WaitRequest
		TimeoutMS int64 `json:"timeout_ms,omitempty"`
	}{id, req, timeout.Milliseconds()})
	return r["line"], err
}

// --- layouts ---

// LayoutExport describes the given environments (all when none).
func (w Workspace) LayoutExport(ctx context.Context, envIDs ...string) (pane.LayoutSpec, error) {
	return wsCall[pane.LayoutSpec](ctx, w, "layout.export", map[string][]string{"environments": envIDs})
}

func (w Workspace) LayoutApply(ctx context.Context, spec pane.LayoutSpec) (pane.ApplyResult, error) {
	return wsCall[pane.ApplyResult](ctx, w, "layout.apply", spec)
}

// --- worktrees ---

func (w Workspace) WorktreeList(ctx context.Context, repo string) ([]WorktreeInfo, error) {
	return wsCall[[]WorktreeInfo](ctx, w, "worktree.list", map[string]string{"repo": repo})
}

func (w Workspace) WorktreeCreate(ctx context.Context, req WorktreeCreateRequest) (environment.Environment, error) {
	return wsCall[environment.Environment](ctx, w, "worktree.create", req)
}

func (w Workspace) WorktreeOpen(ctx context.Context, req WorktreeOpenRequest) (environment.Environment, error) {
	return wsCall[environment.Environment](ctx, w, "worktree.open", req)
}

func (w Workspace) WorktreeRemove(ctx context.Context, req WorktreeRemoveRequest) error {
	return w.c.Call(ctx, "worktree.remove", req, nil)
}
