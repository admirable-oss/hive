package client

import (
	"context"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

type Client interface {
	Ping(context.Context) error
	Status(context.Context) (Status, error)
	Shutdown(context.Context) error

	EnvironmentList(context.Context) ([]environment.Environment, error)
	EnvironmentCreate(context.Context, string) (environment.Environment, error)
	EnvironmentGet(context.Context, string) (environment.Environment, error)
	EnvironmentRemove(context.Context, string) error

	ProcessStart(ctx context.Context, envID string, command string, args []string) (process.Process, error)
	ProcessGet(ctx context.Context, id string) (process.Process, error)
	ProcessList(ctx context.Context, envID string) ([]process.Process, error)
	ProcessStop(ctx context.Context, id string) error
}
