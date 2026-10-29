// Package issue reads and updates issues in trackers: GitHub's, built in
// through the gh tool, and the trackers plugins provide. An issue is
// named by a ref, <tracker>:<key>, such as linear:HIV-42 or
// github:owner/repo#42. A task can carry one (task.Spec.Issue): its pull
// request names it, and it is closed when the task's branch merges if the
// task says so.
package issue

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Errors.
var (
	ErrInvalid  = errors.New("invalid issue request")
	ErrNotFound = errors.New("not found")
)

// EventChanged announces an issue Hive commented on or closed.
const EventChanged = "issue.changed"

// GitHub is the built-in tracker's name.
const GitHub = "github"

// Bounds.
const (
	MaxSearch  = 100
	MaxComment = 64 << 10
)

// Issue is one issue as its tracker describes it.
type Issue struct {
	Ref       string    `json:"ref" desc:"tracker:key, as Hive names it"`
	Tracker   string    `json:"tracker" desc:"the tracker: github, or a plugin's"`
	Key       string    `json:"key" desc:"the tracker's own name for it: HIV-42, owner/repo#42"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	State     string    `json:"state,omitempty" desc:"open or closed, as the tracker says"`
	URL       string    `json:"url,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Assignees []string  `json:"assignees,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Closed reports whether the tracker says it is closed.
func (i Issue) Closed() bool {
	s := strings.ToLower(i.State)
	return s == "closed" || s == "done" || s == "completed" || s == "canceled" || s == "cancelled"
}

// Tracker is one issue tracker.
type Tracker interface {
	Get(ctx context.Context, key string) (Issue, error)
	Search(ctx context.Context, query string, limit int) ([]Issue, error)
	Comment(ctx context.Context, key, body string) error
	Close(ctx context.Context, key, comment string) (Issue, error)
}

// Trackers are the trackers plugins provide, which come and go.
type Trackers interface {
	Tracker(name string) (Tracker, bool)
	Names() []string
}

// Events publishes what the service changed.
type Events interface {
	Publish(typ string, data any)
}

var refPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,63}):(\S{1,256})$`)

// ParseRef splits tracker:key.
func ParseRef(ref string) (tracker, key string, err error) {
	m := refPattern.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", "", fmt.Errorf("%w: %q: write tracker:key (linear:HIV-42, github:owner/repo#42)", ErrInvalid, ref)
	}
	return m[1], m[2], nil
}

// Service routes issue requests to their trackers.
type Service struct {
	builtin map[string]Tracker
	plugins Trackers
	events  Events
}

// NewService serves GitHub's issues through gh (github, nil for none)
// and the trackers plugins provide.
func NewService(github Tracker, plugins Trackers, events Events) *Service {
	s := &Service{builtin: map[string]Tracker{}, plugins: plugins, events: events}
	if github != nil {
		s.builtin[GitHub] = github
	}
	return s
}

// Names lists the trackers there are now.
func (s *Service) Names() []string {
	var out []string
	for n := range s.builtin {
		out = append(out, n)
	}
	if s.plugins != nil {
		for _, n := range s.plugins.Names() {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (s *Service) tracker(name string) (Tracker, error) {
	if t, ok := s.builtin[name]; ok {
		return t, nil
	}
	if s.plugins != nil {
		if t, ok := s.plugins.Tracker(name); ok {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w: no issue tracker %q (have %s)", ErrNotFound, name, strings.Join(s.Names(), ", "))
}

func named(tracker string, i Issue, key string) Issue {
	i.Tracker = tracker
	if i.Key == "" {
		i.Key = key
	}
	i.Ref = tracker + ":" + i.Key
	return i
}

// Get reads an issue.
func (s *Service) Get(ctx context.Context, ref string) (Issue, error) {
	name, key, err := ParseRef(ref)
	if err != nil {
		return Issue{}, err
	}
	t, err := s.tracker(name)
	if err != nil {
		return Issue{}, err
	}
	i, err := t.Get(ctx, key)
	if err != nil {
		return Issue{}, err
	}
	return named(name, i, key), nil
}

// Search finds issues in one tracker, or in every one.
func (s *Service) Search(ctx context.Context, tracker, query string, limit int) ([]Issue, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: say what to search for", ErrInvalid)
	}
	if limit <= 0 || limit > MaxSearch {
		limit = min(max(limit, 20), MaxSearch)
	}
	names := []string{tracker}
	if tracker == "" {
		names = s.Names()
	}
	out := []Issue{}
	var errs []error
	for _, n := range names {
		t, err := s.tracker(n)
		if err != nil {
			return nil, err
		}
		found, err := t.Search(ctx, query, limit)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n, err))
			continue
		}
		for _, i := range found {
			out = append(out, named(n, i, i.Key))
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Comment adds a comment to an issue.
func (s *Service) Comment(ctx context.Context, ref, body string) error {
	switch {
	case strings.TrimSpace(body) == "":
		return fmt.Errorf("%w: the comment is empty", ErrInvalid)
	case len(body) > MaxComment:
		return fmt.Errorf("%w: a comment is at most %d KiB", ErrInvalid, MaxComment>>10)
	}
	name, key, err := ParseRef(ref)
	if err != nil {
		return err
	}
	t, err := s.tracker(name)
	if err != nil {
		return err
	}
	if err := t.Comment(ctx, key, body); err != nil {
		return err
	}
	s.publish(name + ":" + key)
	return nil
}

// Close closes an issue, with a comment when one is given.
func (s *Service) Close(ctx context.Context, ref, comment string) (Issue, error) {
	if len(comment) > MaxComment {
		return Issue{}, fmt.Errorf("%w: a comment is at most %d KiB", ErrInvalid, MaxComment>>10)
	}
	name, key, err := ParseRef(ref)
	if err != nil {
		return Issue{}, err
	}
	t, err := s.tracker(name)
	if err != nil {
		return Issue{}, err
	}
	i, err := t.Close(ctx, key, comment)
	if err != nil {
		return Issue{}, err
	}
	i = named(name, i, key)
	s.publish(i.Ref)
	return i, nil
}

func (s *Service) publish(ref string) {
	if s.events != nil {
		s.events.Publish(EventChanged, map[string]string{"ref": ref})
	}
}
