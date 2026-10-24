package branch

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/admirable-oss/hive/internal/git"
)

// Default naming patterns.
const (
	// DefaultAgentPattern names the branch of an agent started with a
	// worktree of its own and no branch named.
	DefaultAgentPattern = "hive/{kind}/{id}"
	// DefaultTaskPattern names the branch of an isolated task.
	DefaultTaskPattern = "hive/{plan}/{task}"
)

// PatternVars are what a pattern may use. Unused ones are ignored.
type PatternVars struct {
	Kind string // {kind}: the agent's kind
	Name string // {name}: the agent's or task's name
	Plan string // {plan}: the plan's ID
	Task string // {task}: the task's ID
	Env  string // {env}: the environment it was made from
	// {id} is 6 random hex digits; {date} is today, as 20060102.
}

var placeholder = regexp.MustCompile(`\{([a-z]+)\}`)

// patternNames are the placeholders a pattern may use.
var patternNames = []string{"kind", "name", "plan", "task", "env", "id", "date"}

// CheckPattern rejects a pattern with an unknown placeholder, or one that
// cannot name a valid branch.
func CheckPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("%w: an empty branch pattern", ErrInvalid)
	}
	for _, m := range placeholder.FindAllStringSubmatch(pattern, -1) {
		if !slices.Contains(patternNames, m[1]) {
			return fmt.Errorf("%w: unknown {%s} in branch pattern %q (use %s)", ErrInvalid, m[1], pattern, "{"+strings.Join(patternNames, "}, {")+"}")
		}
	}
	sample := placeholder.ReplaceAllString(pattern, "x")
	if err := git.ValidateBranch(sample); err != nil {
		return fmt.Errorf("%w: branch pattern %q: %w", ErrInvalid, pattern, err)
	}
	return nil
}

// Expand fills a pattern in. Values are reduced to what a branch name
// allows; an empty value leaves its placeholder out, with the separator
// before it.
func Expand(pattern string, v PatternVars, now time.Time) (string, error) {
	if err := CheckPattern(pattern); err != nil {
		return "", err
	}
	values := map[string]string{
		"kind": v.Kind, "name": v.Name, "plan": v.Plan, "task": v.Task, "env": v.Env,
		"id": randomID(), "date": now.Format("20060102"),
	}
	out := placeholder.ReplaceAllStringFunc(pattern, func(m string) string {
		return git.Slug(values[m[1:len(m)-1]])
	})
	// Drop the empty parts left by empty values: hive//x, a--b, trailing /.
	parts := strings.Split(out, "/")
	kept := parts[:0]
	for _, p := range parts {
		if p = strings.Trim(p, "-."); p != "" {
			kept = append(kept, p)
		}
	}
	name := strings.Join(kept, "/")
	if err := git.ValidateBranch(name); err != nil {
		return "", fmt.Errorf("%w: pattern %q makes %q: %w", ErrInvalid, pattern, name, err)
	}
	return name, nil
}

func randomID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
