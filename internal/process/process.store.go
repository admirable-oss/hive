package process

import "context"

type Store interface {
	Create(ctx context.Context, p Process) error
	Update(ctx context.Context, p Process) error
	Get(ctx context.Context, id string) (Process, error)
	List(ctx context.Context, environmentID string) ([]Process, error)
}
