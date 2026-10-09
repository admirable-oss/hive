package process

import "context"

// Store persists process records and owns where their logs live on disk.
type Store interface {
	Create(ctx context.Context, p Process) error
	Update(ctx context.Context, p Process) error
	Get(ctx context.Context, id string) (Process, error)
	// List returns the processes of one environment, or of every environment
	// when environmentID is empty, oldest first.
	List(ctx context.Context, environmentID string) ([]Process, error)
	// LogPaths returns where p's stdout and stderr are written.
	LogPaths(p Process) (stdout, stderr string)
	// Move transfers p's record and logs to environment envID and returns
	// the updated record. Open log files keep being written (they are
	// renamed, not copied).
	Move(ctx context.Context, p Process, envID string) (Process, error)
	// Tail returns the last n lines of one of p's logs (all of it when n <= 0).
	Tail(ctx context.Context, p Process, stream Stream, n int) (string, error)
}
