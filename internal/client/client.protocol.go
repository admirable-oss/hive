package client

import (
	"context"
	"io"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
)

// Client is every call the daemon answers, one method per wire method.
// Implementations are safe for concurrent use.
type Client interface {
	Ping(context.Context) error
	Status(context.Context) (Status, error)
	// Shutdown stops the daemon. With stopAgents false its agents keep
	// running (when they run under shims) and the next daemon adopts them.
	Shutdown(ctx context.Context, stopAgents bool) error
	// Close releases the client's connection.
	Close() error

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

	// TerminalAttach opens a full-screen view: reading it yields the screen
	// painted as ANSI for a real terminal, writing it types into the agent.
	// It takes over the agent's terminal size.
	TerminalAttach(ctx context.Context, req ViewRequest) (*Attachment, error)
	// TerminalFrames opens a view that yields the screen as frames, for
	// clients that draw it themselves.
	TerminalFrames(ctx context.Context, req ViewRequest) (*FrameStream, error)
	TerminalSnapshot(ctx context.Context, req SnapshotRequest) (Snapshot, error)
	TerminalResize(ctx context.Context, processID string, width, height uint16) error
	TerminalInput(ctx context.Context, processID string, data []byte) error

	// Events subscribes to daemon events whose type starts with one of
	// types (all events when none are given).
	Events(ctx context.Context, types ...string) (*EventStream, error)
}
