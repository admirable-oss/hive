package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner runs git. It is a port so tests can fake git's answers.
type Runner interface {
	// Run runs `git -C dir args...` and returns its standard output.
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// CommandError is a git command that failed, with what git said.
type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *CommandError) Unwrap() error { return e.Err }

// ExecRunner runs the git binary on PATH.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Reading status must not take the index lock the user's own git
	// commands need, and output must not be translated.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &CommandError{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// Git answers questions about repositories.
type Git struct {
	Runner Runner
}

// New returns a Git using the git binary.
func New() *Git { return &Git{Runner: ExecRunner{}} }

func notRepo(err error) bool {
	var ce *CommandError
	return errors.As(err, &ce) && strings.Contains(ce.Stderr, "not a git repository")
}

// Root returns the top of the working tree containing dir.
func (g *Git) Root(ctx context.Context, dir string) (string, error) {
	out, err := g.Runner.Run(ctx, dir, "rev-parse", "--show-toplevel")
	if notRepo(err) {
		return "", ErrNotRepository
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Dirs returns the working tree's git directory and the repository's common
// directory (they differ for linked worktrees). Changes there (HEAD, index,
// refs) are what the watcher listens for.
func (g *Git) Dirs(ctx context.Context, dir string) (gitDir, commonDir string, err error) {
	out, err := g.Runner.Run(ctx, dir, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	if notRepo(err) {
		return "", "", ErrNotRepository
	}
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("git rev-parse: unexpected output %q", out)
	}
	gitDir, commonDir = lines[0], lines[1]
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(dir, commonDir)
	}
	return gitDir, filepath.Clean(commonDir), nil
}

// Status returns the state of the working tree containing dir.
func (g *Git) Status(ctx context.Context, dir string) (*Status, error) {
	root, err := g.Root(ctx, dir)
	if err != nil {
		return nil, err
	}
	out, err := g.Runner.Run(ctx, root, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	st := parseStatus(out)
	st.Root = root
	return st, nil
}

// Worktrees lists the repository's worktrees, the main one first.
func (g *Git) Worktrees(ctx context.Context, repo string) ([]Worktree, error) {
	out, err := g.Runner.Run(ctx, repo, "worktree", "list", "--porcelain")
	if notRepo(err) {
		return nil, ErrNotRepository
	}
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// BranchExists reports whether a local branch exists.
func (g *Git) BranchExists(ctx context.Context, repo, branch string) bool {
	_, err := g.Runner.Run(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// AddWorktree checks out branch at path. A branch that does not exist yet
// is created from base (the current HEAD when base is empty).
func (g *Git) AddWorktree(ctx context.Context, repo, path, branch, base string) error {
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if g.BranchExists(ctx, repo, branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path)
		if base != "" {
			args = append(args, base)
		}
	}
	_, err := g.Runner.Run(ctx, repo, args...)
	return err
}

// RemoveWorktree removes the worktree at path. Without force, git refuses
// when it has changes; that is reported as ErrDirty.
func (g *Git) RemoveWorktree(ctx context.Context, repo, path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = []string{"worktree", "remove", "--force", path}
	}
	_, err := g.Runner.Run(ctx, repo, args...)
	var ce *CommandError
	if errors.As(err, &ce) && (strings.Contains(ce.Stderr, "contains modified or untracked files") || strings.Contains(ce.Stderr, "use --force")) {
		return fmt.Errorf("%w: %s (use --force to discard them)", ErrDirty, path)
	}
	return err
}

// ValidateBranch rejects branch names git would refuse or that could be
// mistaken for options.
func ValidateBranch(branch string) error {
	switch {
	case branch == "", strings.HasPrefix(branch, "-"), strings.Contains(branch, ".."),
		strings.ContainsAny(branch, " ~^:?*[\\\x00\x7f"), strings.HasSuffix(branch, ".lock"),
		strings.HasSuffix(branch, "/"), strings.HasPrefix(branch, "/"), strings.Contains(branch, "@{"):
		return fmt.Errorf("invalid branch name %q", branch)
	}
	return nil
}

// Slug turns a branch name into a single directory name: feature/login
// becomes feature-login.
func Slug(branch string) string {
	var b strings.Builder
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}
