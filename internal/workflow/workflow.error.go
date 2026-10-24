package workflow

import "errors"

var (
	// ErrNotFound means no workflow or run has that name or ID.
	ErrNotFound = errors.New("workflow: not found")
	// ErrInvalid means a workflow or request is malformed: a cycle, an
	// unknown step, an input it does not declare.
	ErrInvalid = errors.New("workflow: invalid")
	// ErrState means the request does not fit the run's state: cancelling a
	// finished run, trusting a file that does not parse.
	ErrState = errors.New("workflow: not in a state that allows it")
	// ErrAmbiguous means a run ID prefix matches more than one run.
	ErrAmbiguous = errors.New("workflow: ambiguous run ID")
	// ErrNewer means a file was written by a newer hive.
	ErrNewer = errors.New("workflow: written by a newer hive")
	// ErrCancelled is the cause given to a step's attempt when its run is
	// cancelled, as opposed to one interrupted by the daemon stopping,
	// which resumes when it starts again.
	ErrCancelled = errors.New("workflow: cancelled")
)
