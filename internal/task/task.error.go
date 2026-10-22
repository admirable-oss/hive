package task

import "errors"

var (
	// ErrNotFound means no plan or task has that ID.
	ErrNotFound = errors.New("task: not found")
	// ErrInvalid means a plan or request is malformed: a cycle, an unknown
	// dependency, a missing prompt.
	ErrInvalid = errors.New("task: invalid")
	// ErrState means the request does not fit the task's state: approving
	// a task that is not waiting for approval, retrying one that runs.
	ErrState = errors.New("task: not in a state that allows it")
	// ErrAmbiguous means an ID prefix matches more than one plan.
	ErrAmbiguous = errors.New("task: ambiguous plan ID")
	// ErrNewer means a plan was written by a newer hive.
	ErrNewer = errors.New("task: plan written by a newer hive")
	// ErrCancelled is the cause given to an attempt that is cancelled (by a
	// person, or by its plan's budget), as opposed to one interrupted by
	// the daemon stopping, which resumes when it starts again.
	ErrCancelled = errors.New("task: cancelled")
	// ErrUnreachable means a task's machine could not be reached.
	ErrUnreachable = errors.New("task: machine unreachable")
)
