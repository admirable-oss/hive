package issue

import (
	"context"
	"errors"

	"github.com/admirable-oss/hive/internal/protocol"
)

// RefParams names an issue.
type RefParams struct {
	Ref string `json:"ref" desc:"tracker:key (linear:HIV-42, github:owner/repo#42)"`
}

// SearchParams is issue.search's request.
type SearchParams struct {
	Tracker string `json:"tracker,omitempty" desc:"only this tracker (default: every one)"`
	Query   string `json:"query" desc:"what to search for, in the tracker's own syntax"`
	Limit   int    `json:"limit,omitempty" desc:"at most this many (default 20, at most 100)"`
}

// CommentParams is issue.comment's request.
type CommentParams struct {
	Ref  string `json:"ref" desc:"tracker:key"`
	Body string `json:"body" desc:"the comment, as the tracker formats it (Markdown on GitHub)"`
}

// CloseParams is issue.close's request.
type CloseParams struct {
	Ref     string `json:"ref" desc:"tracker:key"`
	Comment string `json:"comment,omitempty" desc:"a comment to add as it closes"`
}

// Register exposes the service on the wire as issue.*.
func Register(r *protocol.Router, s *Service) {
	r.MustRegister("issue.get", protocol.Method(func(ctx context.Context, p RefParams) (Issue, error) {
		i, err := s.Get(ctx, p.Ref)
		return i, wireError(err)
	}))
	r.MustRegister("issue.search", protocol.Method(func(ctx context.Context, p SearchParams) ([]Issue, error) {
		found, err := s.Search(ctx, p.Tracker, p.Query, p.Limit)
		return found, wireError(err)
	}))
	r.MustRegister("issue.comment", protocol.Method(func(ctx context.Context, p CommentParams) (struct{}, error) {
		return struct{}{}, wireError(s.Comment(ctx, p.Ref, p.Body))
	}))
	r.MustRegister("issue.close", protocol.Method(func(ctx context.Context, p CloseParams) (Issue, error) {
		i, err := s.Close(ctx, p.Ref, p.Comment)
		return i, wireError(err)
	}))
}

func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrInvalid):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	case errors.Is(err, context.DeadlineExceeded):
		return protocol.NewError(protocol.ErrorCodeTimeout, err)
	}
	return err
}
