package branch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// ForgeRequest is a pull request to open.
type ForgeRequest struct {
	// Dir is the branch's worktree, where a forge's tool runs.
	Dir string `json:"dir"`
	// Remote is the git remote the branch was pushed to, and RemoteURL
	// where it points; Host and Project are read from that URL
	// (github.com, owner/repo).
	Remote    string `json:"remote"`
	RemoteURL string `json:"remote_url"`
	Host      string `json:"host"`
	Project   string `json:"project"`
	// Head is the branch proposed; Base the branch it would merge into.
	Head  string `json:"head"`
	Base  string `json:"base"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Draft bool   `json:"draft,omitempty"`
}

// Forge opens pull requests on one kind of code hosting. Hive has GitHub
// and GitLab (through their gh and glab tools, so they use the person's
// own login) and commands configured per host; plugins add more by
// registering a Forge (see Forges.Register).
type Forge interface {
	// Name is how configuration names it: github, gitlab, ….
	Name() string
	Open(ctx context.Context, req ForgeRequest) (PullRequest, error)
}

// Exec runs a forge's command-line tool in dir, with stdin, and returns
// what it printed.
type Exec func(ctx context.Context, dir string, argv []string, stdin []byte) ([]byte, error)

// ExecCommand runs argv on this machine. A tool that fails is reported
// with what it printed on its standard error.
func ExecCommand(ctx context.Context, dir string, argv []string, stdin []byte) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	// Never wait on a prompt nobody sees.
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s is not installed (it opens pull requests here): %w", argv[0], err)
		}
		if msg != "" {
			return nil, fmt.Errorf("%s: %s", argv[0], lastLines(msg, 5))
		}
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	return stdout.Bytes(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Forges picks the forge for a remote: a host configured for one, else a
// plugin's forge for the host, else the built-in hosts (github.com,
// gitlab.com, and hosts named after GitLab).
type Forges struct {
	mu     sync.Mutex
	byName map[string]Forge
	hosts  map[string]string // host → forge name
	lookup func(host string) (Forge, bool)
}

// DefaultForges returns GitHub and GitLab, running their tools with exec
// (nil runs them on this machine).
func DefaultForges(run Exec) *Forges {
	if run == nil {
		run = ExecCommand
	}
	f := &Forges{byName: map[string]Forge{}, hosts: map[string]string{}}
	f.Register(GitHub{Exec: run})
	f.Register(GitLab{Exec: run})
	return f
}

// Register adds a forge, or replaces the one of the same name. Plugins'
// forges are not registered: Lookup finds them.
func (f *Forges) Register(forge Forge) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byName[forge.Name()] = forge
}

// Lookup sets where forges for hosts no configuration routes are found
// before the built-in ones: plugins' forges, which come and go.
func (f *Forges) Lookup(fn func(host string) (Forge, bool)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookup = fn
}

// Route sends a host's pull requests to the named forge.
func (f *Forges) Route(host, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts[strings.ToLower(host)] = name
}

// Names lists the registered forges.
func (f *Forges) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.byName {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// For returns the forge for a host.
func (f *Forges) For(host string) (Forge, error) {
	host = strings.ToLower(host)
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.hosts[host]
	if !ok && f.lookup != nil {
		if forge, found := f.lookup(host); found {
			return forge, nil
		}
	}
	if !ok {
		switch {
		case host == "github.com" || strings.HasPrefix(host, "github."):
			name = "github"
		case host == "gitlab.com" || strings.HasPrefix(host, "gitlab."):
			name = "gitlab"
		default:
			return nil, fmt.Errorf("%w %s; say which in [[branches.forge]]", ErrNoForge, host)
		}
	}
	forge := f.byName[name]
	if forge == nil {
		return nil, fmt.Errorf("%w %s: forge %q is not available", ErrNoForge, host, name)
	}
	return forge, nil
}

// scpLike matches git's user@host:path remotes.
var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]+):(.+)$`)

// ParseRemote reads a remote URL's host and project path (owner/repo,
// group/sub/repo), without .git.
func ParseRemote(raw string) (host, project string, err error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.Scheme == "file" {
			return "", "", fmt.Errorf("%w: %q is not a hosted remote", ErrNoForge, raw)
		}
		host, project = u.Hostname(), u.Path
	} else if m := scpLike.FindStringSubmatch(raw); m != nil && !strings.HasPrefix(raw, "/") {
		host, project = m[1], m[2]
	} else {
		return "", "", fmt.Errorf("%w: %q is not a hosted remote", ErrNoForge, raw)
	}
	project = strings.TrimSuffix(strings.Trim(project, "/"), ".git")
	if project == "" {
		return "", "", fmt.Errorf("%w: %q names no project", ErrNoForge, raw)
	}
	return strings.ToLower(host), project, nil
}

// GitHub opens pull requests with the gh tool.
type GitHub struct{ Exec Exec }

func (GitHub) Name() string { return "github" }

func (g GitHub) Open(ctx context.Context, req ForgeRequest) (PullRequest, error) {
	repo := req.Project
	if req.Host != "github.com" {
		repo = req.Host + "/" + req.Project
	}
	argv := []string{"gh", "pr", "create", "--repo", repo, "--head", req.Head, "--base", req.Base, "--title", req.Title, "--body", req.Body}
	if req.Draft {
		argv = append(argv, "--draft")
	}
	out, err := g.Exec(ctx, req.Dir, argv, nil)
	if err != nil {
		return PullRequest{}, err
	}
	return pullRequestFrom("github", out, `/pull/(\d+)`, req.Draft)
}

// GitLab opens merge requests with the glab tool.
type GitLab struct{ Exec Exec }

func (GitLab) Name() string { return "gitlab" }

func (g GitLab) Open(ctx context.Context, req ForgeRequest) (PullRequest, error) {
	argv := []string{
		"glab", "mr", "create", "--repo", "https://" + req.Host + "/" + req.Project,
		"--source-branch", req.Head, "--target-branch", req.Base,
		"--title", req.Title, "--description", req.Body, "--yes",
	}
	if req.Draft {
		argv = append(argv, "--draft")
	}
	out, err := g.Exec(ctx, req.Dir, argv, nil)
	if err != nil {
		return PullRequest{}, err
	}
	return pullRequestFrom("gitlab", out, `/merge_requests/(\d+)`, req.Draft)
}

// pullRequestFrom finds the URL a tool printed (its last http(s) word)
// and the number in it.
func pullRequestFrom(forge string, out []byte, number string, draft bool) (PullRequest, error) {
	var link string
	for _, w := range strings.Fields(string(out)) {
		if strings.HasPrefix(w, "https://") || strings.HasPrefix(w, "http://") {
			link = w
		}
	}
	if link == "" {
		return PullRequest{}, fmt.Errorf("%s printed no pull request URL: %s", forge, lastLines(strings.TrimSpace(string(out)), 3))
	}
	pr := PullRequest{Forge: forge, URL: link, Draft: draft}
	if m := regexp.MustCompile(number).FindStringSubmatch(link); m != nil {
		pr.Number, _ = strconv.Atoi(m[1])
	}
	return pr, nil
}

// CommandForge opens pull requests with a command of the person's own: it
// gets the ForgeRequest as JSON on its standard input and prints the pull
// request as JSON ({"url": …, "number": …}), or its URL.
type CommandForge struct {
	ForgeName string
	Command   []string
	Exec      Exec
}

func (c CommandForge) Name() string { return c.ForgeName }

func (c CommandForge) Open(ctx context.Context, req ForgeRequest) (PullRequest, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return PullRequest{}, err
	}
	run := c.Exec
	if run == nil {
		run = ExecCommand
	}
	out, err := run(ctx, req.Dir, c.Command, in)
	if err != nil {
		return PullRequest{}, err
	}
	var pr PullRequest
	if json.Unmarshal(bytes.TrimSpace(out), &pr) == nil && pr.URL != "" {
		pr.Forge, pr.Draft = c.ForgeName, req.Draft
		return pr, nil
	}
	return pullRequestFrom(c.ForgeName, out, `(\d+)/?$`, req.Draft)
}
