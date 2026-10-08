package client

import (
	"context"
	"io"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

// Client is every call the daemon answers, one method per wire method.
type Client interface {
	Ping(context.Context) error
	Status(context.Context) (Status, error)
	Shutdown(context.Context) error

	EnvironmentList(context.Context) ([]environment.Environment, error)
	EnvironmentCreate(ctx context.Context, id string) (environment.Environment, error)
	EnvironmentGet(ctx context.Context, id string) (environment.Environment, error)
	EnvironmentRemove(ctx context.Context, id string) error

	ProcessStart(ctx context.Context, req process.StartRequest) (process.Process, error)
	ProcessGet(ctx context.Context, id string) (process.Process, error)
	// ProcessList lists one environment's processes, or all when envID is "".
	ProcessList(ctx context.Context, envID string) ([]process.Process, error)
	ProcessStop(ctx context.Context, id string) error
	// ProcessLogs returns the tail of a log in one reply (size-capped).
	ProcessLogs(ctx context.Context, req process.LogsRequest) (Logs, error)
	// ProcessLogsStream writes a log of any size to out and, with req.Follow,
	// keeps writing new output until the process exits or ctx is cancelled.
	ProcessLogsStream(ctx context.Context, req process.LogsRequest, out io.Writer) error

	// TerminalAttach streams the process's terminal: in is sent as keystrokes
	// and output is written to out. It returns when either stream ends or ctx
	// is cancelled.
	TerminalAttach(ctx context.Context, processID string, in io.Reader, out io.Writer) error
	TerminalResize(ctx context.Context, processID string, width, height uint16) error
	TerminalInput(ctx context.Context, processID string, data []byte) error
}
