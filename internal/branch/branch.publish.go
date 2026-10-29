package branch

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// PushRequest pushes a branch, on a person's command.
type PushRequest struct {
	Ref string `json:"ref" desc:"the branch"`
	// Remote is where to push (default: the branch's upstream remote,
	// else origin, else the only remote).
	Remote string `json:"remote,omitempty" desc:"the remote to push to (default: its upstream's, else origin)"`
}

// Push pushes a branch's commits to the branch of the same name on a
// remote, and sets it as the upstream. Hive pushes only when asked, and
// never forces. Uncommitted changes are not pushed.
func (s *Service) Push(ctx context.Context, req PushRequest) (Branch, error) {
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return Branch{}, err
	}
	dir := b.Path
	if !b.State.Active() {
		dir = b.Repo
	}
	remote, err := s.remoteOf(ctx, b, req.Remote)
	if err != nil {
		return Branch{}, err
	}
	if err := s.git.Push(ctx, dir, remote, b.Name); err != nil {
		return Branch{}, err
	}
	head, err := s.git.Resolve(ctx, dir, "refs/heads/"+b.Name)
	if err != nil {
		return Branch{}, err
	}
	s.mu.Lock()
	if live := s.branches[b.ID]; live != nil {
		live.Pushed = &Push{Remote: remote, Commit: head, At: s.cfg.Now()}
		s.commitLocked(live)
	}
	s.mu.Unlock()
	s.persist()
	s.log.Info("branch pushed", "branch", b.Name, "remote", remote)
	return s.Get(ctx, b.ID)
}

// remoteOf picks the remote a branch is pushed to.
func (s *Service) remoteOf(ctx context.Context, b Branch, want string) (string, error) {
	remotes, err := s.git.Remotes(ctx, b.Repo)
	if err != nil {
		return "", err
	}
	if want != "" {
		if !slices.Contains(remotes, want) {
			return "", fmt.Errorf("%w: no remote %q (the repository has %s)", ErrInvalid, want, listOr(remotes, "none"))
		}
		return want, nil
	}
	if up, _ := s.git.Upstream(ctx, b.Repo, b.Name); up != "" {
		for _, r := range remotes {
			if strings.HasPrefix(up, r+"/") {
				return r, nil
			}
		}
	}
	switch {
	case slices.Contains(remotes, "origin"):
		return "origin", nil
	case len(remotes) == 1:
		return remotes[0], nil
	case len(remotes) == 0:
		return "", fmt.Errorf("%w: the repository has no remote", ErrState)
	}
	return "", fmt.Errorf("%w: the repository has several remotes (%s); name one", ErrInvalid, strings.Join(remotes, ", "))
}

func listOr(list []string, none string) string {
	if len(list) == 0 {
		return none
	}
	return strings.Join(list, ", ")
}

// PullRequestRequest opens a pull request, on a person's command.
type PullRequestRequest struct {
	Ref string `json:"ref" desc:"the branch"`
	// Title and Body default to the branch's first commit and a summary
	// of its commits and gates.
	Title string `json:"title,omitempty" desc:"the title (default: its first commit's subject)"`
	Body  string `json:"body,omitempty" desc:"the description (default: its commits and gates)"`
	Draft bool   `json:"draft,omitempty" desc:"open it as a draft"`
	// Base is the branch it would merge into (default: the branch's base,
	// without its remote).
	Base string `json:"base,omitempty" desc:"the branch it would merge into (default: the branch's base)"`
	// Push pushes the branch first when the remote lacks its commits;
	// without it, such a request is refused.
	Push   bool   `json:"push,omitempty" desc:"push the branch first if the remote lacks its commits"`
	Remote string `json:"remote,omitempty" desc:"the remote (default: its upstream's, else origin)"`
}

// OpenPullRequest proposes a branch's commits on its forge. It refuses a
// branch with uncommitted changes (a pull request carries commits, and
// Hive does not make them) or with nothing to propose, and one the remote
// does not have unless Push says to push it first.
func (s *Service) OpenPullRequest(ctx context.Context, req PullRequestRequest) (Branch, error) {
	b, err := s.Get(ctx, req.Ref)
	if err != nil {
		return Branch{}, err
	}
	switch {
	case !b.State.Open():
		return Branch{}, fmt.Errorf("%w: branch %s is %s", ErrState, b.Name, b.State)
	case b.PullRequest != nil:
		return Branch{}, fmt.Errorf("%w: branch %s already has a pull request: %s", ErrState, b.Name, b.PullRequest.URL)
	}
	if err := s.Refresh(ctx, b.Repo); err != nil {
		return Branch{}, err
	}
	if b, err = s.Get(ctx, b.ID); err != nil {
		return Branch{}, err
	}
	switch {
	case b.Dirty:
		return Branch{}, fmt.Errorf("%w: %s has uncommitted changes; commit them (a pull request carries commits)", ErrState, b.Path)
	case b.Ahead == 0:
		return Branch{}, fmt.Errorf("%w: branch %s has no commits that %s lacks", ErrState, b.Name, b.Base)
	}
	remote, err := s.remoteOf(ctx, b, req.Remote)
	if err != nil {
		return Branch{}, err
	}
	if have, _ := s.git.Resolve(ctx, b.Path, "refs/remotes/"+remote+"/"+b.Name); have != b.Head {
		if !req.Push {
			return Branch{}, fmt.Errorf("%w: %s does not have branch %s as it is; push it first (hive branch push, or --push)", ErrState, remote, b.Name)
		}
		if b, err = s.Push(ctx, PushRequest{Ref: b.ID, Remote: remote}); err != nil {
			return Branch{}, err
		}
	}
	rawURL, err := s.git.RemoteURL(ctx, b.Path, remote)
	if err != nil {
		return Branch{}, err
	}
	host, project, err := ParseRemote(rawURL)
	if err != nil {
		return Branch{}, err
	}
	forge, err := s.ports.Forges.For(host)
	if err != nil {
		return Branch{}, err
	}
	base := req.Base
	if base == "" {
		base = strings.TrimPrefix(b.Base, remote+"/")
	}
	if base == "" || base == b.Name {
		return Branch{}, fmt.Errorf("%w: name the branch it would merge into", ErrInvalid)
	}
	title, body := req.Title, req.Body
	if title == "" || body == "" {
		t, bd := s.describe(ctx, b)
		title, body = cmpStr(title, t), cmpStr(body, bd)
	}
	pr, err := forge.Open(ctx, ForgeRequest{
		Dir: b.Path, Remote: remote, RemoteURL: rawURL, Host: host, Project: project,
		Head: b.Name, Base: base, Title: title, Body: body, Draft: req.Draft,
	})
	if err != nil {
		return Branch{}, err
	}
	pr.At = s.cfg.Now()
	s.mu.Lock()
	if live := s.branches[b.ID]; live != nil {
		live.PullRequest = &pr
		s.commitLocked(live)
	}
	s.mu.Unlock()
	s.persist()
	s.log.Info("pull request opened", "branch", b.Name, "url", pr.URL)
	return s.Get(ctx, b.ID)
}

// describe writes a pull request's default title and body.
func (s *Service) describe(ctx context.Context, b Branch) (title, body string) {
	subjects, _ := s.git.Subjects(ctx, b.Path, b.Base, b.Head, 50)
	title = b.Name
	if len(subjects) > 0 {
		title = subjects[0]
	}
	var sb strings.Builder
	if len(subjects) > 1 {
		sb.WriteString("Commits:\n\n")
		for _, s := range subjects {
			fmt.Fprintf(&sb, "- %s\n", s)
		}
		sb.WriteString("\n")
	}
	if len(b.Gates) > 0 && b.State == StateReady {
		sb.WriteString("Checks passed in the branch's worktree:\n\n")
		for _, g := range b.Gates {
			fmt.Fprintf(&sb, "- %s: `%s`\n", g.Name, strings.Join(g.Command, " "))
		}
		sb.WriteString("\n")
	}
	if b.Owner != nil && b.Owner.Task != "" && s.ports.Issues != nil {
		if ref := s.ports.Issues.For(ctx, b.Owner.Plan, b.Owner.Task); ref != "" {
			fmt.Fprintf(&sb, "Issue: %s\n\n", ref)
		}
	}
	if b.Owner != nil && b.Owner.Kind != "" {
		fmt.Fprintf(&sb, "Written by a %s agent in Hive.\n", b.Owner.Kind)
	}
	return title, strings.TrimSpace(sb.String())
}
