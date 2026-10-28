package issue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeTracker struct {
	mu       sync.Mutex
	issues   map[string]Issue
	comments []string
}

func (f *fakeTracker) Get(_ context.Context, key string) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.issues[key]
	if !ok {
		return Issue{}, ErrNotFound
	}
	return i, nil
}

func (f *fakeTracker) Search(_ context.Context, query string, limit int) ([]Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Issue
	for k, i := range f.issues {
		if strings.Contains(i.Title, query) {
			i.Key = k
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *fakeTracker) Comment(_ context.Context, key, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, key+": "+body)
	return nil
}

func (f *fakeTracker) Close(ctx context.Context, key, comment string) (Issue, error) {
	f.mu.Lock()
	i := f.issues[key]
	i.State = "closed"
	f.issues[key] = i
	f.mu.Unlock()
	return i, nil
}

type fakePlugins map[string]Tracker

func (f fakePlugins) Tracker(name string) (Tracker, bool) { t, ok := f[name]; return t, ok }
func (f fakePlugins) Names() []string {
	var out []string
	for n := range f {
		out = append(out, n)
	}
	return out
}

type events []string

func (e *events) Publish(typ string, data any) { *e = append(*e, typ) }

func TestService(t *testing.T) {
	linear := &fakeTracker{issues: map[string]Issue{"HIV-42": {Title: "flaky test in api", State: "In Progress"}}}
	gh := &fakeTracker{issues: map[string]Issue{"o/r#7": {Title: "flaky test on windows", State: "open"}}}
	var ev events
	s := NewService(gh, fakePlugins{"linear": linear}, &ev)
	ctx := context.Background()

	i, err := s.Get(ctx, "linear:HIV-42")
	if err != nil || i.Ref != "linear:HIV-42" || i.Tracker != "linear" || i.Key != "HIV-42" || i.Closed() {
		t.Fatalf("get: %+v %v", i, err)
	}
	for _, ref := range []string{"HIV-42", "Linear:HIV-42", "linear:", "linear:a b"} {
		if _, err := s.Get(ctx, ref); !errors.Is(err, ErrInvalid) {
			t.Errorf("ref %q: %v", ref, err)
		}
	}
	if _, err := s.Get(ctx, "jira:X-1"); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "github, linear") {
		t.Fatalf("an unknown tracker: %v", err)
	}
	found, err := s.Search(ctx, "", "flaky", 0)
	if err != nil || len(found) != 2 {
		t.Fatalf("search every tracker: %+v %v", found, err)
	}
	if found, _ := s.Search(ctx, "github", "flaky", 0); len(found) != 1 || found[0].Ref != "github:o/r#7" {
		t.Fatalf("search one: %+v", found)
	}
	if _, err := s.Search(ctx, "", " ", 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("an empty search")
	}
	if err := s.Comment(ctx, "linear:HIV-42", "on it"); err != nil || len(linear.comments) != 1 {
		t.Fatalf("comment: %v %v", linear.comments, err)
	}
	if err := s.Comment(ctx, "linear:HIV-42", "  "); !errors.Is(err, ErrInvalid) {
		t.Fatal("an empty comment")
	}
	if i, err := s.Close(ctx, "linear:HIV-42", ""); err != nil || !i.Closed() {
		t.Fatalf("close: %+v %v", i, err)
	}
	if len(ev) != 2 || ev[0] != EventChanged {
		t.Fatalf("events: %v", ev)
	}
}

func TestGitHubTracker(t *testing.T) {
	var calls [][]string
	var stdin []string
	run := func(_ context.Context, _ string, argv []string, in []byte) ([]byte, error) {
		calls = append(calls, argv)
		stdin = append(stdin, string(in))
		switch strings.Join(argv[:3], " ") {
		case "gh issue view":
			return []byte(`{"number":42,"title":"Fix it","body":"Steps","state":"OPEN","url":"https://github.com/o/r/issues/42","labels":[{"name":"bug"}],"assignees":[{"login":"me"}]}`), nil
		case "gh search issues":
			return []byte(`[{"number":3,"title":"Other","state":"closed","url":"https://github.com/x/y/issues/3","repository":{"nameWithOwner":"x/y"}}]`), nil
		}
		return nil, nil
	}
	g := GitHubTracker{Exec: run}
	ctx := context.Background()
	i, err := g.Get(ctx, "o/r#42")
	if err != nil || i.Key != "o/r#42" || i.State != "open" || i.Labels[0] != "bug" || i.Assignees[0] != "me" {
		t.Fatalf("get: %+v %v", i, err)
	}
	if strings.Join(calls[0], " ") != "gh issue view 42 --repo o/r --json number,title,body,state,url,labels,assignees,updatedAt" {
		t.Fatalf("argv: %v", calls[0])
	}
	if _, err := g.Get(ctx, "o/r"); !errors.Is(err, ErrInvalid) {
		t.Fatal("a key without a number")
	}
	found, err := g.Search(ctx, "flaky", 5)
	if err != nil || len(found) != 1 || found[0].Key != "x/y#3" || !found[0].Closed() {
		t.Fatalf("search: %+v %v", found, err)
	}
	calls, stdin = nil, nil
	if _, err := g.Close(ctx, "o/r#42", "done"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0][2] != "comment" || stdin[0] != "done" || calls[1][2] != "close" {
		t.Fatalf("close: %v %q", calls, stdin)
	}
}
