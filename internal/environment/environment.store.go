package environment

import "context"

type Store interface {
	Create(context.Context, Environment) error
	Get(context.Context, string) (Environment, error)
	List(context.Context) ([]Environment, error)
	Delete(context.Context, string) error
}
