package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/environment"
)

type Client interface {
	Ping(context.Context) error
	Status(context.Context) (Status, error)
	Shutdown(context.Context) error

	EnvironmentList(context.Context) ([]environment.Environment, error)
	EnvironmentCreate(context.Context, string) (environment.Environment, error)
	EnvironmentGet(context.Context, string) (environment.Environment, error)
	EnvironmentRemove(context.Context, string) error
}
