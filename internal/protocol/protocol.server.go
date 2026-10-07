package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// Serve answers requests on conn until the peer hangs up, the stream breaks,
// or a handler hijacks the connection. It always closes conn.
func Serve(ctx context.Context, conn net.Conn, h Handler, maxMessageSize int64) error {
	stream := NewStream(conn, maxMessageSize)
	defer stream.Close()

	for {
		var req Request
		err := stream.Receive(&req)
		if err == nil && req.Method == "" {
			err = fmt.Errorf("%w: missing method", ErrInvalidMessage)
		}
		switch {
		case err == nil:
		case errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, ErrInvalidMessage), errors.Is(err, ErrMessageTooLarge):
			// Tell the peer why before giving up on the connection.
			_ = stream.Send(Fail(req, NewError(ErrorCodeInvalidRequest, err)))
			return err
		default:
			return err
		}

		resp := h.Handle(ctx, req)
		if err := stream.Send(resp); err != nil {
			return err
		}
		if resp.Hijack != nil {
			resp.Hijack(ctx, stream.Conn())
			return nil
		}
	}
}
