package protocol

import (
	"context"
	"errors"
	"io"
	"net"
)

type Connection interface {
	Serve(context.Context, net.Conn) error
}

type connection struct {
	protocol Protocol
	codec    Codec
}

func NewConnection(
	protocol Protocol,
	codec Codec,
) Connection {
	return &connection{
		protocol: protocol,
		codec:    codec,
	}
}

func (c *connection) Serve(
	ctx context.Context,
	conn net.Conn,
) error {
	defer conn.Close()

	for {
		var request Request
		if err := c.codec.DecodeRequest(conn, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		response := c.protocol.Handle(ctx, request)

		if err := c.codec.EncodeResponse(conn, response); err != nil {
			return err
		}
	}
}
