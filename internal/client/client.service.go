package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/vt"
)

// service talks protocol 2 over one connection, dialled on first use and
// re-dialled after it breaks (a daemon restart). When the daemon only speaks
// protocol 1 it falls back to a connection per call.
type service struct {
	config Config

	mu     sync.Mutex
	mux    *protocol.MuxClient
	legacy bool

	seq atomic.Uint64 // protocol-1 request IDs
}

func NewService(config Config) Client {
	if config.Name == "" {
		config.Name = "cli"
	}
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
	ViewID    string `json:"view_id,omitempty"`
}

func (s *service) Ping(ctx context.Context) error {
	_, err := call[struct{}](ctx, s, "runtime.ping", nil)
	return err
}

func (s *service) Status(ctx context.Context) (Status, error) {
	return call[Status](ctx, s, "runtime.status", nil)
}

func (s *service) Shutdown(ctx context.Context, stopAgents bool) error {
	_, err := call[struct{}](ctx, s, "runtime.shutdown", map[string]bool{"stop_agents": stopAgents})
	return err
}

func (s *service) Close() error {
	s.mu.Lock()
	mux := s.mux
	s.mux = nil
	s.mu.Unlock()
	if mux != nil {
		return mux.Close()
	}
	return nil
}

func (s *service) EnvironmentList(ctx context.Context) ([]environment.Environment, error) {
	return call[[]environment.Environment](ctx, s, "environment.list", nil)
}

func (s *service) EnvironmentCreate(ctx context.Context, req environment.CreateRequest) (environment.Environment, error) {
	return call[environment.Environment](ctx, s, "environment.create", req)
}

func (s *service) EnvironmentUpdate(ctx context.Context, req environment.UpdateRequest) (environment.Environment, error) {
	return call[environment.Environment](ctx, s, "environment.update", req)
}

func (s *service) Call(ctx context.Context, method string, params, result any) error {
	raw, err := call[json.RawMessage](ctx, s, method, params)
	if err != nil || result == nil || len(raw) == 0 {
		return err
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("%s: decode result: %w", method, err)
	}
	return nil
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
	p, err := s.openPipe(ctx, "process.logs.stream", req, nil)
	if err != nil {
		return err
	}
	defer p.Close()
	stop := context.AfterFunc(ctx, func() { _ = p.Close() })
	defer stop()
	// The daemon sends the log and closes the pipe when it is finished;
	// cancelling ctx closes it from this side.
	_, err = io.Copy(out, p)
	if ctx.Err() != nil {
		return nil // the caller stopped following
	}
	return err
}

func (s *service) TerminalAttach(ctx context.Context, req ViewRequest) (*Attachment, error) {
	var view View
	p, err := s.openPipe(ctx, "terminal.attach", req, &view)
	if err != nil {
		return nil, err
	}
	return &Attachment{pipe: p, View: view, processID: req.ProcessID, client: s}, nil
}

func (s *service) TerminalFrames(ctx context.Context, req ViewRequest) (*FrameStream, error) {
	if err := s.requireMux(ctx); err != nil {
		return nil, err
	}
	var view View
	p, err := s.openPipe(ctx, "terminal.frames", req, &view)
	if err != nil {
		return nil, err
	}
	a := &Attachment{pipe: p, View: view, processID: req.ProcessID, client: s}
	return &FrameStream{Attachment: a, reader: vt.NewFrameReader(p)}, nil
}

func (s *service) TerminalSnapshot(ctx context.Context, req SnapshotRequest) (Snapshot, error) {
	if err := s.requireMux(ctx); err != nil {
		return Snapshot{}, err
	}
	return call[Snapshot](ctx, s, "terminal.snapshot", req)
}

func (s *service) TerminalResize(ctx context.Context, processID string, width, height uint16) error {
	_, err := call[struct{}](ctx, s, "terminal.resize", terminalParams{ProcessID: processID, Width: width, Height: height})
	return err
}

func (s *service) TerminalInput(ctx context.Context, processID string, data []byte) error {
	_, err := call[struct{}](ctx, s, "terminal.input", terminalParams{ProcessID: processID, Data: data})
	return err
}

func (s *service) Events(ctx context.Context, types ...string) (*EventStream, error) {
	if err := s.requireMux(ctx); err != nil {
		return nil, err
	}
	p, err := s.openPipe(ctx, "events.subscribe", map[string][]string{"types": types}, nil)
	if err != nil {
		return nil, err
	}
	return newEventStream(p), nil
}

// errLegacy routes a call to protocol 1.
var errLegacy = errors.New("protocol 1")

// conn returns the protocol-2 connection, dialling it when needed.
func (s *service) conn(ctx context.Context) (*protocol.MuxClient, error) {
	if err := s.config.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.legacy {
		return nil, errLegacy
	}
	if s.mux != nil {
		if s.mux.Err() == nil {
			return s.mux, nil
		}
		_ = s.mux.Close()
		s.mux = nil
	}
	raw, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	mux, err := protocol.Handshake(ctx, raw, protocol.Hello{
		Client: s.config.Name, ClientVersion: buildinfo.Get().Version, Capabilities: []string{vt.FrameCapability},
	}, protocol.DefaultMaxMessageSize)
	if errors.Is(err, protocol.ErrProtocol1Only) {
		s.legacy = true
		return nil, errLegacy
	}
	if err != nil {
		return nil, fmt.Errorf("connect to runtime: %w", err)
	}
	s.mux = mux
	return mux, nil
}

func (s *service) requireMux(ctx context.Context) error {
	_, err := s.conn(ctx)
	if errors.Is(err, errLegacy) {
		return ErrDaemonTooOld
	}
	return err
}

func (s *service) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", s.config.SocketPath)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w (%s)", ErrUnavailable, s.config.SocketPath)
		}
		return nil, fmt.Errorf("connect to runtime: %w", err)
	}
	return conn, nil
}

// call performs one request and decodes its result.
func call[R any](ctx context.Context, s *service, method string, params any) (R, error) {
	var result R
	mux, err := s.conn(ctx)
	switch {
	case errors.Is(err, errLegacy):
		err = s.legacyCall(ctx, method, params, &result)
	case err != nil:
		return result, err
	default:
		err = mux.Call(ctx, method, params, &result)
	}
	if err != nil {
		return result, fmt.Errorf("%s: %w", method, err)
	}
	return result, nil
}

// openPipe opens a pipe; on protocol 1 the pipe is the rest of a dedicated
// connection.
func (s *service) openPipe(ctx context.Context, method string, params, result any) (io.ReadWriteCloser, error) {
	mux, err := s.conn(ctx)
	switch {
	case errors.Is(err, errLegacy):
		return s.legacyPipe(ctx, method, params, result)
	case err != nil:
		return nil, err
	}
	p, err := mux.OpenPipe(ctx, method, params, result)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return p, nil
}

// Protocol 1: a connection per call.

func (s *service) legacyCall(ctx context.Context, method string, params, result any) error {
	stream, done, err := s.legacyOpen(ctx)
	if err != nil {
		return err
	}
	defer done()
	return s.legacyExchange(stream, method, params, result)
}

func (s *service) legacyPipe(ctx context.Context, method string, params, result any) (io.ReadWriteCloser, error) {
	conn, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	stream := protocol.NewStream(conn, protocol.DefaultMaxMessageSize)
	if err := s.legacyExchange(stream, method, params, result); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return stream.Conn(), nil
}

func (s *service) legacyOpen(ctx context.Context) (*protocol.Stream, func(), error) {
	conn, err := s.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	stream := protocol.NewStream(conn, protocol.DefaultMaxMessageSize)
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	return stream, func() { stop(); _ = stream.Close() }, nil
}

func (s *service) legacyExchange(stream *protocol.Stream, method string, params, result any) error {
	req, err := protocol.NewRequest(strconv.FormatUint(s.seq.Add(1), 10), method, params)
	if err != nil {
		return err
	}
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	var resp protocol.Response
	if err := stream.Receive(&resp); err != nil {
		return fmt.Errorf("receive: %w", err)
	}
	if err := protocol.CheckVersion(resp.Version); err != nil {
		return fmt.Errorf("%w; restart the daemon with `hive daemon restart`", err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result == nil {
		return nil
	}
	if len(resp.Result) == 0 {
		return errMissingResult
	}
	return jsonUnmarshal(resp.Result, result)
}

var errMissingResult = errors.New("missing result")
