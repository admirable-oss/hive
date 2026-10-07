package environment

import "context"

// Store persists environments. It owns the on-disk layout, so Create returns
// the environment with its workspace Path filled in.
type Store interface {
	Create(context.Context, Environment) (Environment, error)
	Get(context.Context, string) (Environment, error)
	List(context.Context) ([]Environment, error)
	Delete(context.Context, string) error
}
