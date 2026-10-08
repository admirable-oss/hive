package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"syscall"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

type service struct {
	config Config
	seq    atomic.Uint64 // request IDs, unique per client
}

func NewService(config Config) Client {
	return &service{config: config}
}

type idParams struct {
	ID string `json:"id"`
}

type terminalParams struct {
	ProcessID string `json:"process_id"`
	Data      []byte `json:"data,omitempty"`
	Width     uint16 `json:"width,omitempty"`
	Height    uint16 `json:"height,omitempty"`
}

func (s *service) Ping(ctx context.Context) error {
	_, err := call[struct{}](ctx, s, "runtime.ping", nil)
	return err
}

func (s *service) Status(ctx context.Context) (Status, error) {
	return call[Status](ctx, s, "runtime.status", nil)
}

func (s *service) Shutdown(ctx context.Context) error {
	_, err := call[struct{}](ctx, s, "runtime.shutdown", nil)
	return err
}

func (s *service) EnvironmentList(ctx context.Context) ([]environment.Environment, error) {
	return call[[]environment.Environment](ctx, s, "environment.list", nil)
}

func (s *service) EnvironmentCreate(ctx context.Context, id string) (environment.Environment, error) {
	return call[environment.Environment](ctx, s, "environment.create", idParams{id})
}

func (s *service) EnvironmentGet(ctx context.Context, id string) (environment.Environment, error) {
	return call[environment.Environment](ctx, s, "environment.get", idParams{id})
}

func (s *service) EnvironmentRemove(ctx context.Context, id string) error {
	_, err := call[struct{}](ctx, s, "environment.remove", idParams{id})
	return err
}

func (s *service) ProcessStart(ctx context.Context, req process.StartRequest) (process.Process, error) {
	return call[process.Process](ctx, s, "process.start", req)
}

func (s *service) ProcessGet(ctx context.Context, id string) (process.Process, error) {
	return call[process.Process](ctx, s, "process.get", idParams{id})
}

func (s *service) ProcessList(ctx context.Context, envID string) ([]process.Process, error) {
	return call[[]process.Process](ctx, s, "process.list", map[string]string{"environment_id": envID})
}

func (s *service) ProcessStop(ctx context.Context, id string) error {
	_, err := call[struct{}](ctx, s, "process.stop", idParams{id})
	return err
}

func (s *service) ProcessLogs(ctx context.Context, req process.LogsRequest) (Logs, error) {
	req.Follow = false
	return call[Logs](ctx, s, "process.logs", req)
}

func (s *service) ProcessLogsStream(ctx context.Context, req process.LogsRequest, out io.Writer) error {
	stream, done, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer done()
	if err := s.exchange(stream, "process.logs.stream", req, nil); err != nil {
		return err
	}
	// After the ack the daemon sends raw log bytes and closes the connection
	// when it is finished; cancelling ctx closes it from this side.
	_, err = io.Copy(out, stream.Conn())
	if ctx.Err() != nil {
		return nil // the caller stopped following
	}
	return err
}

func (s *service) TerminalResize(ctx context.Context, processID string, width, height uint16) error {
	_, err := call[struct{}](ctx, s, "terminal.resize", terminalParams{ProcessID: processID, Width: width, Height: height})
	return err
}

func (s *service) TerminalInput(ctx context.Context, processID string, data []byte) error {
	_, err := call[struct{}](ctx, s, "terminal.input", terminalParams{ProcessID: processID, Data: data})
	return err
}

func (s *service) TerminalAttach(ctx context.Context, processID string, in io.Reader, out io.Writer) error {
	stream, done, err := s.open(ctx)
	if err != nil {
		return err
	}
	defer done()
	if err := s.exchange(stream, "terminal.attach", terminalParams{ProcessID: processID}, nil); err != nil {
		return err
	}

	// After the ack the connection is a raw byte stream in both directions.
	conn := stream.Conn()
	finished := make(chan struct{}, 2)
	if in != nil {
		go func() { _, _ = io.Copy(conn, in); finished <- struct{}{} }()
	}
	if out == nil {
		out = io.Discard
	}
	go func() { _, _ = io.Copy(out, conn); finished <- struct{}{} }()

	<-finished // ctx cancellation closes the stream, which ends the copies
	return nil
}

// call performs one request/response exchange on a fresh connection.
func call[R any](ctx context.Context, s *service, method string, params any) (R, error) {
	var result R
	stream, done, err := s.open(ctx)
	if err != nil {
		return result, err
	}
	defer done()
	err = s.exchange(stream, method, params, &result)
	return result, err
}

// open dials the daemon. The returned func closes the connection; it is also
// closed as soon as ctx ends, so no read or write can outlive the caller.
func (s *service) open(ctx context.Context) (*protocol.Stream, func(), error) {
	if err := s.config.Validate(); err != nil {
		return nil, nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", s.config.SocketPath)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, nil, fmt.Errorf("%w (%s)", ErrUnavailable, s.config.SocketPath)
		}
		return nil, nil, fmt.Errorf("connect to runtime: %w", err)
	}
	stream := protocol.NewStream(conn, protocol.DefaultMaxMessageSize)
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	return stream, func() { stop(); _ = stream.Close() }, nil
}

// exchange sends one request and decodes the response into result (if non-nil).
func (s *service) exchange(stream *protocol.Stream, method string, params, result any) error {
	req, err := protocol.NewRequest(strconv.FormatUint(s.seq.Add(1), 10), method, params)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("%s: send: %w", method, err)
	}
	var resp protocol.Response
	if err := stream.Receive(&resp); err != nil {
		return fmt.Errorf("%s: receive: %w", method, err)
	}
	if err := protocol.CheckVersion(resp.Version); err != nil {
		return fmt.Errorf("%s: %w; restart the daemon with `hive stop` so it matches this CLI", method, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %w", method, resp.Error)
	}
	if result == nil {
		return nil
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("%s: %w", method, errMissingResult)
	}
	return json.Unmarshal(resp.Result, result)
}

var errMissingResult = errors.New("missing result")
