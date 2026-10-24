package branch

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/git"
	"github.com/admirable-oss/hive/internal/protocol"
)

// Ref names a branch.
type Ref struct {
	Ref string `json:"ref" desc:"the branch: its ID, environment, agent, plan/task or name"`
}

// CheckParams is branch.check's request.
type CheckParams struct {
	Ref  string `json:"ref,omitempty" desc:"read this branch's repository"`
	Repo string `json:"repo,omitempty" desc:"read this repository (its main working tree); neither: every repository"`
}

// Register exposes the service on the wire as branch.*.
func Register(r *protocol.Router, s *Service) {
	m := func(name string, h protocol.Handler) { r.MustRegister(name, h) }

	m("branch.list", protocol.Method(func(_ context.Context, p ListRequest) ([]Branch, error) {
		return s.List(p), nil
	}))
	m("branch.get", protocol.Method(func(ctx context.Context, p Ref) (Branch, error) {
		b, err := s.Get(ctx, p.Ref)
		return b, wireError(err)
	}))
	m("branch.check", protocol.Method(func(ctx context.Context, p CheckParams) ([]Branch, error) {
		repos := []string{p.Repo}
		switch {
		case p.Ref != "":
			b, err := s.Get(ctx, p.Ref)
			if err != nil {
				return nil, wireError(err)
			}
			repos = []string{b.Repo}
		case p.Repo == "":
			repos = s.repos()
		}
		for _, repo := range repos {
			if err := s.Refresh(ctx, repo); err != nil {
				return nil, wireError(err)
			}
		}
		if len(repos) == 1 {
			return s.List(ListRequest{Repo: repos[0]}), nil
		}
		return s.List(ListRequest{}), nil
	}))
	m("branch.diff", protocol.Method(func(ctx context.Context, p DiffRequest) (Diff, error) {
		d, err := s.Diff(ctx, p)
		return d, wireError(err)
	}))
	m("branch.conflicts", protocol.Method(func(ctx context.Context, p ConflictsRequest) (Conflicts, error) {
		c, err := s.Conflicts(ctx, p)
		return c, wireError(err)
	}))
	m("branch.update", protocol.Method(func(ctx context.Context, p UpdateRequest) (UpdateResult, error) {
		res, err := s.Update(ctx, p)
		return res, wireError(err)
	}))
	m("branch.ready", protocol.Method(func(ctx context.Context, p ReadyRequest) (Branch, error) {
		b, err := s.Ready(ctx, p)
		if b.ID != "" && errors.Is(err, ErrState) && len(b.Gates) > 0 {
			// The gates ran and one failed: the branch, with its results,
			// is the answer.
			return b, nil
		}
		return b, wireError(err)
	}))
	m("branch.comment", protocol.Method(func(ctx context.Context, p CommentRequest) (Branch, error) {
		b, err := s.Comment(ctx, p)
		return b, wireError(err)
	}))
	m("branch.send", protocol.Method(func(ctx context.Context, p Ref) (Branch, error) {
		b, err := s.Send(ctx, p.Ref)
		return b, wireError(err)
	}))
	m("branch.push", protocol.Method(func(ctx context.Context, p PushRequest) (Branch, error) {
		b, err := s.Push(ctx, p)
		return b, wireError(err)
	}))
	m("branch.pr", protocol.Method(func(ctx context.Context, p PullRequestRequest) (Branch, error) {
		b, err := s.OpenPullRequest(ctx, p)
		return b, wireError(err)
	}))
	m("branch.clean", protocol.Method(func(ctx context.Context, p CleanRequest) ([]Branch, error) {
		list, err := s.Clean(ctx, p)
		return list, wireError(err)
	}))
}

// repos lists the repositories with active branches.
func (s *Service) repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range s.List(ListRequest{}) {
		if !seen[b.Repo] {
			seen[b.Repo] = true
			out = append(out, b.Repo)
		}
	}
	return out
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	var ce *git.CommandError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrAmbiguous), errors.Is(err, ErrNoForge),
		errors.Is(err, git.ErrNotRepository), errors.Is(err, git.ErrNoCommit):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, ErrState), errors.Is(err, ErrNoAgent), errors.Is(err, git.ErrDirty):
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	case errors.Is(err, git.ErrUnsupported):
		return protocol.NewError(protocol.ErrorCodeUnavailable, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	case errors.As(err, &ce):
		// git said no (a rejected push, an unknown base): the request,
		// not the daemon, is at fault.
		return protocol.NewError(protocol.ErrorCodeConflict, err)
	}
	return err
}
