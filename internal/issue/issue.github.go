package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Exec runs a command-line tool with stdin and returns what it printed
// (branch.ExecCommand).
type Exec func(ctx context.Context, dir string, argv []string, stdin []byte) ([]byte, error)

// GitHubTracker reads GitHub issues with the gh tool, so it uses the
// person's own login. A key is owner/repo#number.
type GitHubTracker struct{ Exec Exec }

var githubKey = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#([0-9]+)$`)

func splitGitHubKey(key string) (repo, number string, err error) {
	m := githubKey.FindStringSubmatch(key)
	if m == nil {
		return "", "", fmt.Errorf("%w: github issue %q: write owner/repo#number", ErrInvalid, key)
	}
	return m[1], m[2], nil
}

// ghIssue is what gh prints with --json.
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (g ghIssue) issue(repo string) Issue {
	if repo == "" {
		repo = g.Repository.NameWithOwner
	}
	i := Issue{
		Key: repo + "#" + strconv.Itoa(g.Number), Title: g.Title, Body: g.Body,
		State: map[string]string{"OPEN": "open", "CLOSED": "closed"}[g.State], URL: g.URL, UpdatedAt: g.UpdatedAt,
	}
	if i.State == "" {
		i.State = g.State
	}
	for _, l := range g.Labels {
		i.Labels = append(i.Labels, l.Name)
	}
	for _, a := range g.Assignees {
		i.Assignees = append(i.Assignees, a.Login)
	}
	return i
}

func (t GitHubTracker) Get(ctx context.Context, key string) (Issue, error) {
	repo, n, err := splitGitHubKey(key)
	if err != nil {
		return Issue{}, err
	}
	out, err := t.Exec(ctx, "", []string{"gh", "issue", "view", n, "--repo", repo, "--json", "number,title,body,state,url,labels,assignees,updatedAt"}, nil)
	if err != nil {
		return Issue{}, err
	}
	var g ghIssue
	if err := json.Unmarshal(out, &g); err != nil {
		return Issue{}, fmt.Errorf("gh issue view printed %w", err)
	}
	return g.issue(repo), nil
}

func (t GitHubTracker) Search(ctx context.Context, query string, limit int) ([]Issue, error) {
	out, err := t.Exec(ctx, "", []string{"gh", "search", "issues", query, "--limit", strconv.Itoa(limit), "--json", "number,title,state,url,labels,assignees,repository,updatedAt"}, nil)
	if err != nil {
		return nil, err
	}
	var found []ghIssue
	if err := json.Unmarshal(out, &found); err != nil {
		return nil, fmt.Errorf("gh search issues printed %w", err)
	}
	issues := make([]Issue, 0, len(found))
	for _, g := range found {
		issues = append(issues, g.issue(""))
	}
	return issues, nil
}

func (t GitHubTracker) Comment(ctx context.Context, key, body string) error {
	repo, n, err := splitGitHubKey(key)
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, "", []string{"gh", "issue", "comment", n, "--repo", repo, "--body-file", "-"}, []byte(body))
	return err
}

func (t GitHubTracker) Close(ctx context.Context, key, comment string) (Issue, error) {
	repo, n, err := splitGitHubKey(key)
	if err != nil {
		return Issue{}, err
	}
	if comment != "" {
		if err := t.Comment(ctx, key, comment); err != nil {
			return Issue{}, err
		}
	}
	if _, err := t.Exec(ctx, "", []string{"gh", "issue", "close", n, "--repo", repo}, nil); err != nil {
		return Issue{}, err
	}
	return t.Get(ctx, key)
}
