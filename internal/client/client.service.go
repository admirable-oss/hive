package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/process"
	"github.com/admirable-oss/hive/internal/protocol"
)

type service struct {
	config Config
	codec  protocol.Codec
}

func NewService(config Config) Client {
	return &service{
		config: config,
		codec:  protocol.NewJSONCodec(protocol.DefaultMaxMessageSize),
	}
}

func (s *service) Ping(ctx context.Context) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "ping",
		Method:  "runtime.ping",
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf(
			"runtime.ping: %s",
			response.Error.Message,
		)
	}
	return nil
}

func (s *service) Status(ctx context.Context) (Status, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "status",
		Method:  "runtime.status",
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return Status{}, err
	}
	if response.Error != nil {
		return Status{}, fmt.Errorf(
			"runtime.status: %s",
			response.Error.Message,
		)
	}
	var status Status
	if err := decodeResult(response.Result, &status); err != nil {
		return Status{}, fmt.Errorf("decode runtime status: %w", err)
	}
	return status, nil
}

func (s *service) Shutdown(ctx context.Context) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "shutdown",
		Method:  "runtime.shutdown",
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf(
			"runtime.shutdown: %s",
			response.Error.Message,
		)
	}
	return nil
}

func (s *service) request(
	ctx context.Context,
	request protocol.Request,
) (protocol.Response, error) {
	if err := s.config.Validate(); err != nil {
		return protocol.Response{}, err
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", s.config.SocketPath)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("connect to runtime: %w", err)
	}
	defer conn.Close()

	if err := s.codec.EncodeRequest(conn, request); err != nil {
		return protocol.Response{}, fmt.Errorf("encode request: %w", err)
	}

	var response protocol.Response
	if err := s.codec.DecodeResponse(conn, &response); err != nil {
		return protocol.Response{}, fmt.Errorf("decode response: %w", err)
	}

	return response, nil
}

var errMissingResult = errors.New("missing result")

func decodeResult[T any](raw json.RawMessage, target *T) error {
	if len(raw) == 0 {
		return errMissingResult
	}
	return json.Unmarshal(raw, target)
}

func (s *service) EnvironmentList(ctx context.Context) ([]environment.Environment, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "env-list",
		Method:  "environment.list",
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("environment.list: %s", response.Error.Message)
	}
	var envs []environment.Environment
	if err := decodeResult(response.Result, &envs); err != nil {
		return nil, fmt.Errorf("decode env list: %w", err)
	}
	return envs, nil
}

func (s *service) EnvironmentCreate(ctx context.Context, id string) (environment.Environment, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "env-create",
		Method:  "environment.create",
		Params:  map[string]interface{}{"id": id},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return environment.Environment{}, err
	}
	if response.Error != nil {
		return environment.Environment{}, fmt.Errorf("environment.create: %s", response.Error.Message)
	}
	var env environment.Environment
	if err := decodeResult(response.Result, &env); err != nil {
		return environment.Environment{}, fmt.Errorf("decode env create: %w", err)
	}
	return env, nil
}

func (s *service) EnvironmentGet(ctx context.Context, id string) (environment.Environment, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "env-get",
		Method:  "environment.get",
		Params:  map[string]interface{}{"id": id},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return environment.Environment{}, err
	}
	if response.Error != nil {
		return environment.Environment{}, fmt.Errorf("environment.get: %s", response.Error.Message)
	}
	var env environment.Environment
	if err := decodeResult(response.Result, &env); err != nil {
		return environment.Environment{}, fmt.Errorf("decode env get: %w", err)
	}
	return env, nil
}

func (s *service) EnvironmentRemove(ctx context.Context, id string) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "env-remove",
		Method:  "environment.remove",
		Params:  map[string]interface{}{"id": id},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("environment.remove: %s", response.Error.Message)
	}
	return nil
}

func (s *service) ProcessStart(ctx context.Context, envID string, command string, args []string) (process.Process, error) {
	if args == nil {
		args = []string{}
	}
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "proc-start",
		Method:  "process.start",
		Params: process.StartRequest{
			EnvironmentID: envID,
			Command:       command,
			Args:          args,
		},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return process.Process{}, err
	}
	if response.Error != nil {
		return process.Process{}, fmt.Errorf("process.start: %s", response.Error.Message)
	}
	var p process.Process
	if err := decodeResult(response.Result, &p); err != nil {
		return process.Process{}, fmt.Errorf("decode process start: %w", err)
	}
	return p, nil
}

func (s *service) ProcessGet(ctx context.Context, id string) (process.Process, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "proc-get",
		Method:  "process.get",
		Params:  map[string]interface{}{"id": id},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return process.Process{}, err
	}
	if response.Error != nil {
		return process.Process{}, fmt.Errorf("process.get: %s", response.Error.Message)
	}
	var p process.Process
	if err := decodeResult(response.Result, &p); err != nil {
		return process.Process{}, fmt.Errorf("decode process get: %w", err)
	}
	return p, nil
}

func (s *service) ProcessList(ctx context.Context, envID string) ([]process.Process, error) {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "proc-list",
		Method:  "process.list",
		Params:  map[string]interface{}{"environment_id": envID},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("process.list: %s", response.Error.Message)
	}
	var procs []process.Process
	if err := decodeResult(response.Result, &procs); err != nil {
		return nil, fmt.Errorf("decode process list: %w", err)
	}
	return procs, nil
}

func (s *service) ProcessStop(ctx context.Context, id string) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "proc-stop",
		Method:  "process.stop",
		Params:  map[string]interface{}{"id": id},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("process.stop: %s", response.Error.Message)
	}
	return nil
}

func (s *service) ProcessStartRequest(ctx context.Context, req process.StartRequest) (process.Process, error) {
	if req.Args == nil {
		req.Args = []string{}
	}
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "proc-start",
		Method:  "process.start",
		Params:  req,
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return process.Process{}, err
	}
	if response.Error != nil {
		return process.Process{}, fmt.Errorf("process.start: %s", response.Error.Message)
	}
	var p process.Process
	if err := decodeResult(response.Result, &p); err != nil {
		return process.Process{}, fmt.Errorf("decode process start: %w", err)
	}
	return p, nil
}

func (s *service) TerminalAttach(ctx context.Context, processID string, in io.Reader, out io.Writer) error {
	if err := s.config.Validate(); err != nil {
		return err
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer conn.Close()

	req := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "term-attach",
		Method:  "terminal.attach",
		Params:  map[string]any{"process_id": processID},
	}

	if err := s.codec.EncodeRequest(conn, req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp protocol.Response
	if err := s.codec.DecodeResponse(conn, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("terminal.attach: %s", resp.Error.Message)
	}

	done := make(chan struct{}, 2)

	go func() {
		if in != nil {
			_, _ = io.Copy(conn, in)
		}
		done <- struct{}{}
	}()

	go func() {
		if out != nil {
			_, _ = io.Copy(out, conn)
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	return nil
}

func (s *service) TerminalResize(ctx context.Context, processID string, width, height uint16) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "term-resize",
		Method:  "terminal.resize",
		Params: map[string]any{
			"process_id": processID,
			"width":      width,
			"height":     height,
		},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("terminal.resize: %s", response.Error.Message)
	}
	return nil
}

func (s *service) TerminalInput(ctx context.Context, processID string, data []byte) error {
	request := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "term-input",
		Method:  "terminal.input",
		Params: map[string]any{
			"process_id": processID,
			"data":       string(data),
		},
	}
	response, err := s.request(ctx, request)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("terminal.input: %s", response.Error.Message)
	}
	return nil
}
