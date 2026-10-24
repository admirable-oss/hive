package branch

import "errors"

var (
	// ErrNotFound means no branch matches the reference.
	ErrNotFound = errors.New("branch: not found")
	// ErrAmbiguous means a reference matches more than one branch.
	ErrAmbiguous = errors.New("branch: ambiguous reference")
	// ErrInvalid means a request is malformed.
	ErrInvalid = errors.New("branch: invalid")
	// ErrState means the branch is not in a state that allows the request:
	// opening a pull request with uncommitted changes, marking a merged
	// branch ready, removing a worktree with uncommitted work unasked.
	ErrState = errors.New("branch: not in a state that allows it")
	// ErrNoAgent means no agent works on the branch to send comments to.
	ErrNoAgent = errors.New("branch: no agent works on it")
	// ErrNoForge means no forge is known for the repository's remote.
	ErrNoForge = errors.New("branch: no forge for this remote")
	// ErrNewer means branches.json was written by a newer hive.
	ErrNewer = errors.New("branch: state written by a newer hive")
)
