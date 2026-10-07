package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

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
	if response.Result == nil {
		return Status{}, fmt.Errorf("runtime.status: missing result")
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		return Status{}, fmt.Errorf("runtime.status: marshal result: %w", err)
	}
	var status Status
	if err := json.Unmarshal(raw, &status); err != nil {
		return Status{}, fmt.Errorf("runtime.status: decode result: %w", err)
	}
	return status, nil
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
