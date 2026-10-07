package client

import (
	"context"
	"io"

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
	ProcessStartRequest(ctx context.Context, req process.StartRequest) (process.Process, error)
	ProcessGet(ctx context.Context, id string) (process.Process, error)
	ProcessList(ctx context.Context, envID string) ([]process.Process, error)
	ProcessStop(ctx context.Context, id string) error

	TerminalAttach(ctx context.Context, processID string, in io.Reader, out io.Writer) error
	TerminalResize(ctx context.Context, processID string, width, height uint16) error
	TerminalInput(ctx context.Context, processID string, data []byte) error
}
