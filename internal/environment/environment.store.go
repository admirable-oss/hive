package environment

import "context"

// Store persists environments. It owns the on-disk layout, so Create returns
// the environment with its Path filled in when Hive manages it.
type Store interface {
	Create(context.Context, Environment) (Environment, error)
	Update(context.Context, Environment) error
	Get(context.Context, string) (Environment, error)
	List(context.Context) ([]Environment, error)
	Delete(context.Context, string) error
}
