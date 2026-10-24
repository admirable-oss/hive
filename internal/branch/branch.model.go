package branch

import (
	"slices"
	"time"

	"github.com/admirable-oss/hive/internal/git"
)

// State is where a branch is in its life, from its worktree's creation to
// its cleanup.
type State string

const (
	// StateCreated: the worktree exists and nothing has happened in it.
	StateCreated State = "created"
	// StateWorking: an agent has worked there, or it differs from its base.
	StateWorking State = "working"
	// StateReady: its gates passed on the files as they are now. A change
	// makes it working again.
	StateReady State = "ready"
	// StateMerged: its commits are in its base. Its worktree is still
	// there (see Branch.Cleanup).
	StateMerged State = "merged"
	// StateCleaned: merged, and its worktree removed.
	StateCleaned State = "cleaned"
	// StateRemoved: its worktree was removed before it merged. The branch
	// and its commits are kept.
	StateRemoved State = "removed"
)

// Active reports whether the branch still has a worktree to work in.
func (s State) Active() bool {
	return s == StateCreated || s == StateWorking || s == StateReady || s == StateMerged
}

// Open reports whether work on the branch is unfinished: active and not
// merged. Only open branches are compared for overlaps.
func (s State) Open() bool { return s == StateCreated || s == StateWorking || s == StateReady }

// schema is the version of branches.json.
const schema = 1

// Branch is a branch Hive made a worktree for, and what is known about
// the work on it.
type Branch struct {
	// ID names the branch in Hive: the ID its environment had.
	ID string `json:"id" desc:"the branch's ID in Hive: its environment's ID when it was made"`
	// Repo is the repository's main working tree.
	Repo string `json:"repo" desc:"the repository (its main working tree)"`
	Name string `json:"name" desc:"the branch's name"`
	// Base is what the branch merges into: a branch name (main,
	// origin/main) or, when it started detached, a commit.
	Base string `json:"base" desc:"what the branch merges into: a branch, or a commit"`
	// BaseCommit is the commit the branch started from.
	BaseCommit string `json:"base_commit,omitempty" desc:"the commit it started from"`
	// Path is the worktree, and EnvironmentID its environment; both are
	// empty once the worktree is removed.
	Path          string `json:"path,omitempty" desc:"its worktree, while there is one"`
	EnvironmentID string `json:"environment_id,omitempty" desc:"its worktree's environment, while there is one"`
	Owner         *Owner `json:"owner,omitempty" desc:"the agent or task it was made for"`
	State         State  `json:"state" desc:"created, working, ready, merged, cleaned or removed"`

	// Head is the commit checked out; Dirty says files differ from it.
	Head  string `json:"head,omitempty" desc:"the commit checked out"`
	Dirty bool   `json:"dirty,omitempty" desc:"uncommitted changes in the worktree"`
	// Ahead counts its commits not in its base; Behind its base's commits
	// not on it.
	Ahead  int `json:"ahead,omitempty" desc:"commits on the branch that its base lacks"`
	Behind int `json:"behind,omitempty" desc:"commits on its base that the branch lacks"`
	// Files counts the files it changes against its base (uncommitted
	// changes included), with their lines.
	Files   int `json:"files,omitempty" desc:"files changed against its base, uncommitted changes included"`
	Added   int `json:"added,omitempty" desc:"lines added"`
	Deleted int `json:"deleted,omitempty" desc:"lines deleted"`
	// Changed lists those files (at most MaxChanged).
	Changed []string `json:"changed,omitempty" desc:"the changed files (at most 200)"`
	// Overlaps are other open branches of the repository that change the
	// same files, and the base when it has moved under the branch.
	Overlaps []Overlap `json:"overlaps,omitempty" desc:"other branches (or its base) changing the same files"`
	// InProgress says a merge or rebase stopped in the worktree, waiting
	// for a person to resolve it.
	InProgress bool `json:"in_progress,omitempty" desc:"a merge or rebase is stopped there, for a person to resolve"`

	// Checking says its gates are running.
	Checking bool `json:"checking,omitempty" desc:"its gates are running"`
	// Gates are the results of the last run of its gates.
	Gates []GateResult `json:"gates,omitempty" desc:"the last run of its gates"`
	// ReadyTree is the tree its gates passed on: the branch is ready while
	// its working tree is still that tree.
	ReadyTree string     `json:"ready_tree,omitempty" desc:"the files its gates passed on (a git tree)"`
	ReadyAt   *time.Time `json:"ready_at,omitempty" desc:"when its gates passed"`

	Comments    []Comment    `json:"comments,omitempty" desc:"review comments, sent or waiting to be sent to its agent"`
	Pushed      *Push        `json:"pushed,omitempty" desc:"its last push, made on request"`
	PullRequest *PullRequest `json:"pull_request,omitempty" desc:"the pull request opened for it, on request"`

	// Cleanup says why a merged branch's worktree is still there.
	Cleanup string `json:"cleanup,omitempty" desc:"why a merged branch's worktree was not removed"`
	// Saved is the ref holding uncommitted work that was discarded with
	// its worktree.
	Saved string `json:"saved,omitempty" desc:"the ref keeping uncommitted work discarded with its worktree"`
	// Error is the last refresh's failure.
	Error string `json:"error,omitempty" desc:"why it could not be read last time"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty" desc:"when its worktree was removed"`
	// Rev grows with every change.
	Rev uint64 `json:"rev"`

	// Started is set once an agent worked there: it is working from then.
	Started bool `json:"started,omitempty" desc:"an agent has worked there"`
	// Committed is set once it was seen with commits of its own: only such
	// a branch can merge.
	Committed bool `json:"committed,omitempty" desc:"it has been seen with commits of its own"`
}

// MaxChanged bounds Branch.Changed and an overlap's file lists.
const MaxChanged = 200

// Owner is what a branch was made for.
type Owner struct {
	// Agent is the agent's ID (its process), Kind its manifest.
	Agent string `json:"agent,omitempty" desc:"the agent's ID"`
	Kind  string `json:"kind,omitempty" desc:"the agent's kind"`
	// Plan and Task name the task (M22) it was made for.
	Plan string `json:"plan,omitempty" desc:"the plan of the task it was made for"`
	Task string `json:"task,omitempty" desc:"the task it was made for"`
}

// Overlap is another branch, or the base, changing files this one changes.
type Overlap struct {
	// With is the other branch's ID; empty for the base.
	With string `json:"with,omitempty" desc:"the other branch's ID; empty when it is the base"`
	// Branch is the other branch's name, or the base.
	Branch string `json:"branch" desc:"the other branch's name, or the base's"`
	// Files are changed on both (at most MaxChanged).
	Files []string `json:"files" desc:"files both change"`
	// Conflicts are the files a merge of the two would stop on.
	Conflicts []string `json:"conflicts,omitempty" desc:"files a merge of the two would conflict on"`
	// Unknown says conflicts could not be computed (git older than 2.38).
	Unknown bool `json:"unknown,omitempty" desc:"conflicts are unknown: git is too old to compute them"`
}

// Gate is a check a branch must pass before it is ready: tests, lint,
// build. It runs in the branch's worktree.
type Gate struct {
	Name    string   `json:"name" desc:"what it checks"`
	Command []string `json:"command" desc:"the command, as an argument vector"`
	// TimeoutMS bounds it (default DefaultGateTimeout).
	TimeoutMS int64 `json:"timeout_ms,omitempty" desc:"how long it may take, in milliseconds (default 30 minutes)"`
	// Repo limits it to repositories under this directory (empty: all).
	Repo string `json:"repo,omitempty" desc:"only for repositories in this directory (empty: every one)"`
}

// DefaultGateTimeout bounds a gate that names no timeout.
const DefaultGateTimeout = 30 * time.Minute

// GateResult is how a gate's run ended.
type GateResult struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	// Passed is set when it exited 0. Skipped gates did not run because
	// an earlier one failed.
	Passed   bool `json:"passed"`
	Skipped  bool `json:"skipped,omitempty"`
	ExitCode int  `json:"exit_code"`
	// Output is its last lines.
	Output     []string  `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	At         time.Time `json:"at"`
}

// Comment is a review comment, which goes back to the branch's agent as
// a prompt.
type Comment struct {
	ID int `json:"id"`
	// Path and Line place it in the diff; both are optional.
	Path string    `json:"path,omitempty"`
	Line int       `json:"line,omitempty"`
	Body string    `json:"body"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
	// SentAt is when it was sent to the agent, and SentTo which agent.
	SentAt *time.Time `json:"sent_at,omitempty"`
	SentTo string     `json:"sent_to,omitempty"`
}

// MaxComments bounds a branch's comments; the oldest sent ones go first.
const MaxComments = 200

// MaxCommentBytes bounds one comment.
const MaxCommentBytes = 16 << 10

// Push is a push made on request.
type Push struct {
	Remote string    `json:"remote"`
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
}

// PullRequest is one opened on request.
type PullRequest struct {
	// Forge is github, gitlab, or the name of a configured command.
	Forge  string    `json:"forge"`
	URL    string    `json:"url"`
	Number int       `json:"number,omitempty"`
	Draft  bool      `json:"draft,omitempty"`
	At     time.Time `json:"at"`
}

// Diff is a branch's changes against its base, uncommitted ones included.
type Diff struct {
	Branch string `json:"branch" desc:"the branch's name"`
	Base   string `json:"base" desc:"what it merges into"`
	// From is where it forked from its base (their merge base); To is a
	// commit of its worktree as it is (Head when it is clean).
	From  string           `json:"from"`
	To    string           `json:"to"`
	Head  string           `json:"head,omitempty"`
	Dirty bool             `json:"dirty,omitempty"`
	Files []git.FileChange `json:"files"`
	// Patch is the unified diff (absent with stat), cut at the request's
	// limit when Truncated.
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Conflicts is what merging a branch with another (or its base) would
// stop on, computed without touching either worktree.
type Conflicts struct {
	Branch string `json:"branch"`
	With   string `json:"with"`
	Clean  bool   `json:"clean"`
	// Files are the conflicted files, with their content as the merge
	// leaves it: conflict markers around each conflict.
	Files []ConflictFile `json:"files,omitempty"`
}

// ConflictFile is one conflicted file.
type ConflictFile struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// OverlapEvent announces branches that newly overlap: data of
// EventOverlap.
type OverlapEvent struct {
	Repo string `json:"repo"`
	// IDs and Names are the two branches; the second is empty and its
	// name the base's when a branch overlaps its base.
	IDs       [2]string `json:"ids"`
	Names     [2]string `json:"names"`
	Files     []string  `json:"files"`
	Conflicts []string  `json:"conflicts,omitempty"`
}

// Event types the service publishes.
const (
	// EventUpdated: a branch changed. Data: Branch.
	EventUpdated = "branch.updated"
	// EventOverlap: two branches started changing the same files, or a
	// branch and its base, which moved. Data: OverlapEvent.
	EventOverlap = "branch.overlap"
	// EventRemoved: branches were forgotten. Data: {"ids": [...]}.
	EventRemoved = "branch.removed"
)

func (b *Branch) clone() Branch {
	c := *b
	c.Changed = slices.Clone(b.Changed)
	c.Overlaps = slices.Clone(b.Overlaps)
	for i := range c.Overlaps {
		c.Overlaps[i].Files = slices.Clone(b.Overlaps[i].Files)
		c.Overlaps[i].Conflicts = slices.Clone(b.Overlaps[i].Conflicts)
	}
	c.Gates = slices.Clone(b.Gates)
	c.Comments = slices.Clone(b.Comments)
	if b.Owner != nil {
		o := *b.Owner
		c.Owner = &o
	}
	if b.Pushed != nil {
		p := *b.Pushed
		c.Pushed = &p
	}
	if b.PullRequest != nil {
		p := *b.PullRequest
		c.PullRequest = &p
	}
	return c
}

// Pending counts the comments not yet sent to the agent.
func (b *Branch) Pending() int {
	n := 0
	for _, c := range b.Comments {
		if c.SentAt == nil {
			n++
		}
	}
	return n
}

// Conflicted reports whether any overlap would conflict.
func (b *Branch) Conflicted() bool {
	return slices.ContainsFunc(b.Overlaps, func(o Overlap) bool { return len(o.Conflicts) > 0 })
}
