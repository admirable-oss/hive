package branch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/branch"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParseRemote(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/acme/app.git":        "github.com acme/app",
		"https://GitHub.com/acme/app":            "github.com acme/app",
		"git@github.com:acme/app.git":            "github.com acme/app",
		"ssh://git@gitlab.corp:2222/g/sub/r.git": "gitlab.corp g/sub/r",
		"gitlab.com:group/repo":                  "gitlab.com group/repo",
	} {
		host, project, err := branch.ParseRemote(raw)
		if err != nil || host+" "+project != want {
			t.Errorf("%s: %q %q %v", raw, host, project, err)
		}
	}
	for _, bad := range []string{"/srv/git/app.git", "file:///srv/app.git", "https://github.com/", "../app"} {
		if _, _, err := branch.ParseRemote(bad); !errors.Is(err, branch.ErrNoForge) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestForgesByHost(t *testing.T) {
	f := branch.DefaultForges(nil)
	for host, want := range map[string]string{"github.com": "github", "gitlab.com": "gitlab", "gitlab.corp.example": "gitlab", "github.acme.com": "github"} {
		if forge, err := f.For(host); err != nil || forge.Name() != want {
			t.Errorf("%s: %v %v", host, forge, err)
		}
	}
	if _, err := f.For("git.example.com"); !errors.Is(err, branch.ErrNoForge) {
		t.Fatalf("unknown host: %v", err)
	}
	f.Route("git.example.com", "gitlab")
	if forge, err := f.For("git.example.com"); err != nil || forge.Name() != "gitlab" {
		t.Fatalf("routed: %v %v", forge, err)
	}
	f.Route("x.example.com", "missing")
	if _, err := f.For("x.example.com"); !errors.Is(err, branch.ErrNoForge) {
		t.Fatalf("routed to nothing: %v", err)
	}
}

func TestGitHubAndGitLabTools(t *testing.T) {
	var argv []string
	run := func(_ context.Context, _ string, a []string, _ []byte) ([]byte, error) {
		argv = a
		if a[0] == "gh" {
			return []byte("Creating pull request for x into main\n\nhttps://github.com/acme/app/pull/42\n"), nil
		}
		return []byte("!12 Fix it (x)\n https://gitlab.com/g/app/-/merge_requests/12\n"), nil
	}
	req := branch.ForgeRequest{Host: "github.com", Project: "acme/app", Head: "x", Base: "main", Title: "T", Body: "B", Draft: true}
	pr, err := branch.GitHub{Exec: run}.Open(context.Background(), req)
	if err != nil || pr.Number != 42 || pr.URL != "https://github.com/acme/app/pull/42" || !pr.Draft {
		t.Fatalf("github: %+v %v", pr, err)
	}
	if got := strings.Join(argv, " "); got != "gh pr create --repo acme/app --head x --base main --title T --body B --draft" {
		t.Fatalf("gh: %s", got)
	}
	req.Host = "github.acme.com"
	_, _ = branch.GitHub{Exec: run}.Open(context.Background(), req)
	if argv[4] != "github.acme.com/acme/app" {
		t.Fatalf("enterprise host: %v", argv)
	}
	req.Host, req.Project, req.Draft = "gitlab.com", "g/app", false
	pr, err = branch.GitLab{Exec: run}.Open(context.Background(), req)
	if err != nil || pr.Number != 12 || pr.Forge != "gitlab" {
		t.Fatalf("gitlab: %+v %v", pr, err)
	}
	if got := strings.Join(argv, " "); got != "glab mr create --repo https://gitlab.com/g/app --source-branch x --target-branch main --title T --description B --yes" {
		t.Fatalf("glab: %s", got)
	}
	silent := func(context.Context, string, []string, []byte) ([]byte, error) { return []byte("done\n"), nil }
	if _, err := (branch.GitHub{Exec: silent}).Open(context.Background(), req); err == nil {
		t.Fatal("no URL printed")
	}
}

func TestCommandForge(t *testing.T) {
	var got branch.ForgeRequest
	run := func(_ context.Context, _ string, argv []string, stdin []byte) ([]byte, error) {
		if argv[0] != "corp-pr" {
			t.Errorf("argv %v", argv)
		}
		_ = json.Unmarshal(stdin, &got)
		return []byte(`{"url": "https://git.corp/r/5", "number": 5}`), nil
	}
	pr, err := branch.CommandForge{ForgeName: "corp", Command: []string{"corp-pr"}, Exec: run}.Open(context.Background(), branch.ForgeRequest{Head: "h", Base: "b", Title: "t"})
	if err != nil || pr.URL != "https://git.corp/r/5" || pr.Number != 5 || pr.Forge != "corp" || got.Head != "h" {
		t.Fatalf("%+v %v %+v", pr, err, got)
	}
	plain := func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte("opened https://git.corp/r/6\n"), nil
	}
	if pr, err := (branch.CommandForge{ForgeName: "corp", Command: []string{"x"}, Exec: plain}).Open(context.Background(), branch.ForgeRequest{}); err != nil || pr.Number != 6 {
		t.Fatalf("a URL is enough: %+v %v", pr, err)
	}
}

func TestExecCommandReportsAMissingTool(t *testing.T) {
	_, err := branch.ExecCommand(context.Background(), t.TempDir(), []string{"hive-no-such-forge-tool"}, nil)
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
}

func TestBranchPatterns(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		pattern string
		vars    branch.PatternVars
		want    string
	}{
		{"hive/{plan}/{task}", branch.PatternVars{Plan: "3f2a", Task: "schema"}, "hive/3f2a/schema"},
		{"agents/{kind}-{date}", branch.PatternVars{Kind: "claude"}, "agents/claude-20261023"},
		{"hive/{kind}/{name}", branch.PatternVars{Kind: "codex"}, "hive/codex"},
		{"{env}/fix login", branch.PatternVars{Env: "api"}, ""},
		{"hive/{name}", branch.PatternVars{Name: "Fix: the login!"}, "hive/Fix--the-login"},
	} {
		got, err := branch.Expand(c.pattern, c.vars, now)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: want an error, got %q", c.pattern, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v (want %q)", c.pattern, got, err, c.want)
		}
	}
	got, err := branch.Expand(branch.DefaultAgentPattern, branch.PatternVars{Kind: "claude"}, now)
	if err != nil || !strings.HasPrefix(got, "hive/claude/") || len(got) != len("hive/claude/")+6 {
		t.Fatalf("{id}: %q %v", got, err)
	}
	if err := branch.CheckPattern("hive/{who}"); !errors.Is(err, branch.ErrInvalid) {
		t.Fatalf("unknown placeholder: %v", err)
	}
}
