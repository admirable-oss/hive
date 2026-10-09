package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/protocol"
)

// worktrees gives agents isolated checkouts: `worktree.create` adds a git
// worktree under <dir>/<repo>/<branch> and an environment rooted in it, so
// agents working on different branches never share a working tree. It
// spans git and environments, which is why it lives in the runtime.
type worktrees struct {
	git  *git.Git
	envs environment.Service // the guarded service: deletes stop agents
	dir  string
}

// WorktreeInfo is one worktree of a repository and the environments rooted
// in it.
type WorktreeInfo struct {
	git.Worktree
	Environments []string `json:"environments,omitempty"`
}

// WorktreeCreateRequest makes a worktree and its environment.
type WorktreeCreateRequest struct {
	// Repo is any directory of the repository (absolute).
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	// Base is where a new branch starts; empty means the repository's HEAD.
	Base string `json:"base,omitempty"`
	// ID names the environment; empty derives it from the branch.
	ID  string            `json:"id,omitempty"`
	Env map[string]string `json:"env,omitempty"`
}

// WorktreeOpenRequest makes an environment for an existing worktree.
type WorktreeOpenRequest struct {
	Path string            `json:"path"`
	ID   string            `json:"id,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// WorktreeRemoveRequest deletes a worktree (given by its environment or its
// path) together with every environment rooted in it. Without Force it
// refuses when the worktree has uncommitted changes. The branch is kept.
type WorktreeRemoveRequest struct {
	ID    string `json:"id,omitempty"`
	Path  string `json:"path,omitempty"`
	Force bool   `json:"force,omitempty"`
}

var (
	errWorktreeExists = errors.New("worktree path already exists")
	errMainWorktree   = errors.New("the main worktree of a repository cannot be removed")
	errNotWorktree    = errors.New("not a worktree of a repository")
	errInvalidRequest = errors.New("invalid worktree request")
)

// mainWorktree returns the repository's main worktree and all its
// worktrees.
func (w *worktrees) mainWorktree(ctx context.Context, dir string) (git.Worktree, []git.Worktree, error) {
	if !filepath.IsAbs(dir) {
		return git.Worktree{}, nil, fmt.Errorf("%w: repository path %q must be absolute", errInvalidRequest, dir)
	}
	list, err := w.git.Worktrees(ctx, dir)
	if err != nil {
		return git.Worktree{}, nil, err
	}
	for _, wt := range list {
		if wt.Main {
			return wt, list, nil
		}
	}
	if len(list) == 0 {
		return git.Worktree{}, nil, git.ErrNotRepository
	}
	return list[0], list, nil
}

func (w *worktrees) List(ctx context.Context, repo string) ([]WorktreeInfo, error) {
	_, list, err := w.mainWorktree(ctx, repo)
	if err != nil {
		return nil, err
	}
	envs, err := w.envs.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorktreeInfo, 0, len(list))
	for _, wt := range list {
		info := WorktreeInfo{Worktree: wt}
		for _, e := range envs {
			if samePath(e.Path, wt.Path) {
				info.Environments = append(info.Environments, e.ID)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (w *worktrees) Create(ctx context.Context, req WorktreeCreateRequest) (environment.Environment, error) {
	if err := git.ValidateBranch(req.Branch); err != nil {
		return environment.Environment{}, fmt.Errorf("%w: %w", errInvalidRequest, err)
	}
	main, _, err := w.mainWorktree(ctx, req.Repo)
	if err != nil {
		return environment.Environment{}, err
	}
	repoName := filepath.Base(main.Path)
	path := filepath.Join(w.dir, repoName, git.Slug(req.Branch))
	if _, err := os.Lstat(path); err == nil {
		return environment.Environment{}, fmt.Errorf("%w: %s", errWorktreeExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return environment.Environment{}, err
	}
	id, err := w.environmentID(ctx, req.ID, repoName, req.Branch)
	if err != nil {
		return environment.Environment{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return environment.Environment{}, err
	}
	if err := w.git.AddWorktree(ctx, main.Path, path, req.Branch, req.Base); err != nil {
		return environment.Environment{}, err
	}
	env, err := w.envs.Create(ctx, environment.CreateRequest{
		ID: id, Root: path, Env: req.Env,
		Worktree: &environment.Worktree{Repo: main.Path, Branch: req.Branch},
	})
	if err != nil {
		// Nothing uses the new worktree yet; do not leave it behind.
		_ = w.git.RemoveWorktree(context.WithoutCancel(ctx), main.Path, path, true)
		return environment.Environment{}, err
	}
	return env, nil
}

// environmentID picks the new environment's ID: the requested one, else
// the branch's slug, else <repo>-<slug> when the slug is taken.
func (w *worktrees) environmentID(ctx context.Context, want, repo, branch string) (string, error) {
	if want != "" {
		return want, nil
	}
	slug := git.Slug(branch)
	for _, id := range []string{slug, git.Slug(repo + "-" + branch)} {
		if !environment.ValidID(id) {
			continue
		}
		if _, err := w.envs.Get(ctx, id); errors.Is(err, environment.ErrNotFound) {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: no free environment id for branch %q; choose one with --id", environment.ErrAlreadyExists, branch)
}

func (w *worktrees) Open(ctx context.Context, req WorktreeOpenRequest) (environment.Environment, error) {
	path := filepath.Clean(req.Path)
	main, list, err := w.mainWorktree(ctx, path)
	if err != nil {
		return environment.Environment{}, err
	}
	i := slices.IndexFunc(list, func(wt git.Worktree) bool { return samePath(wt.Path, path) })
	if i < 0 {
		return environment.Environment{}, fmt.Errorf("%w: %s", errNotWorktree, path)
	}
	wt := list[i]
	envs, err := w.envs.List(ctx)
	if err != nil {
		return environment.Environment{}, err
	}
	for _, e := range envs {
		if samePath(e.Path, wt.Path) && (req.ID == "" || req.ID == e.ID) {
			return e, nil // already open
		}
	}
	id, err := w.environmentID(ctx, req.ID, filepath.Base(main.Path), cmp(wt.Branch, filepath.Base(wt.Path)))
	if err != nil {
		return environment.Environment{}, err
	}
	return w.envs.Create(ctx, environment.CreateRequest{
		ID: id, Root: wt.Path, Env: req.Env,
		Worktree: &environment.Worktree{Repo: main.Path, Branch: wt.Branch},
	})
}

func (w *worktrees) Remove(ctx context.Context, req WorktreeRemoveRequest) error {
	path := req.Path
	if req.ID != "" {
		env, err := w.envs.Get(ctx, req.ID)
		if err != nil {
			return err
		}
		path = env.Path
	}
	if path == "" {
		return fmt.Errorf("%w: give an environment or a worktree path", errInvalidRequest)
	}
	main, list, err := w.mainWorktree(ctx, path)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(list, func(wt git.Worktree) bool { return samePath(wt.Path, path) }) {
		return fmt.Errorf("%w: %s", errNotWorktree, path)
	}
	if samePath(main.Path, path) {
		return errMainWorktree
	}
	// Refuse before any agent is stopped, not after.
	if !req.Force {
		if st, err := w.git.Status(ctx, path); err == nil && st.Dirty() {
			return fmt.Errorf("%w: %s (use --force to discard them)", git.ErrDirty, path)
		}
	}
	envs, err := w.envs.List(ctx)
	if err != nil {
		return err
	}
	for _, e := range envs {
		if samePath(e.Path, path) {
			if err := w.envs.Delete(ctx, e.ID); err != nil {
				return fmt.Errorf("remove environment %s: %w", e.ID, err)
			}
		}
	}
	return w.git.RemoveWorktree(ctx, main.Path, path, req.Force)
}

// samePath compares directories, resolving symlinks (on macOS /tmp and
// /var are links, and git reports resolved paths).
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type worktreeListParams struct {
	Repo string `json:"repo"`
}

// registerWorktrees exposes w as worktree.*.
func registerWorktrees(r *protocol.Router, w *worktrees) {
	r.MustRegister("worktree.list", protocol.Method(func(ctx context.Context, p worktreeListParams) ([]WorktreeInfo, error) {
		list, err := w.List(ctx, p.Repo)
		return list, worktreeWireError(err)
	}))
	r.MustRegister("worktree.create", protocol.Method(func(ctx context.Context, p WorktreeCreateRequest) (environment.Environment, error) {
		env, err := w.Create(ctx, p)
		return env, worktreeWireError(err)
	}))
	r.MustRegister("worktree.open", protocol.Method(func(ctx context.Context, p WorktreeOpenRequest) (environment.Environment, error) {
		env, err := w.Open(ctx, p)
		return env, worktreeWireError(err)
	}))
	r.MustRegister("worktree.remove", protocol.Method(func(ctx context.Context, p WorktreeRemoveRequest) (protocol.Empty, error) {
		return protocol.Empty{}, worktreeWireError(w.Remove(ctx, p))
	}))
}

func worktreeWireError(err error) error {
	var ce *git.CommandError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, environment.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, git.ErrDirty), errors.Is(err, errWorktreeExists), errors.Is(err, environment.ErrAlreadyExists):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	case errors.Is(err, git.ErrNotRepository), errors.Is(err, errMainWorktree), errors.Is(err, errNotWorktree), errors.Is(err, errInvalidRequest),
		errors.Is(err, environment.ErrInvalidID), errors.Is(err, environment.ErrInvalidRoot),
		errors.Is(err, environment.ErrInvalidEnv):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.As(err, &ce):
		// git said no (an unknown base, a branch checked out elsewhere):
		// the caller's request, not the daemon, is at fault.
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
