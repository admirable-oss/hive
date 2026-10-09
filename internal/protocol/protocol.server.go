package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// ServerInfo describes the server in protocol-2 welcomes.
type ServerInfo struct {
	Version      string
	Capabilities []string
	// MaxMessageSize caps one framed message; zero means the default.
	MaxMessageSize int64
}

// Serve answers requests on conn until the peer hangs up, the stream breaks,
// or a protocol-1 pipe takes over the connection. It always closes conn.
func Serve(ctx context.Context, conn net.Conn, h Handler, maxMessageSize int64) error {
	return ServeConn(ctx, conn, h, ServerInfo{MaxMessageSize: maxMessageSize})
}

// ServeConn is Serve with server details for protocol-2 clients. The first
// message decides the protocol: a hello starts protocol 2, anything else is
// a protocol-1 request.
func ServeConn(ctx context.Context, conn net.Conn, h Handler, info ServerInfo) error {
	if info.MaxMessageSize <= 0 {
		info.MaxMessageSize = DefaultMaxMessageSize
	}
	stream := NewStream(conn, info.MaxMessageSize)
	defer stream.Close()

	var first Message
	if err := stream.Receive(&first); err != nil {
		return receiveError(stream, Message{}, err)
	}
	if first.Type == MessageTypeHello {
		return serveMux(ctx, stream, h, info, first)
	}
	return serveV1(ctx, stream, h, first)
}

// receiveError tells the peer why a frame was rejected, when that is
// possible, and reports whether serving should stop.
func receiveError(stream *Stream, m Message, err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case errors.Is(err, ErrInvalidMessage), errors.Is(err, ErrMessageTooLarge):
		_ = stream.Send(Fail(m.request(), NewError(ErrorCodeInvalidRequest, err)))
		return err
	default:
		return err
	}
}

// serveV1 answers protocol-1 requests in order, starting with first.
func serveV1(ctx context.Context, stream *Stream, h Handler, first Message) error {
	m := first
	for {
		var err error
		if m.Method == "" {
			err = fmt.Errorf("%w: missing method", ErrInvalidMessage)
		}
		if err != nil {
			return receiveError(stream, m, err)
		}
		resp := handleV1(ctx, h, m.request())
		if err := stream.Send(resp); err != nil {
			return err
		}
		if resp.AfterSend != nil {
			resp.AfterSend()
		}
		if resp.Pipe != nil {
			// The rest of the connection belongs to the pipe; the stream's
			// reader still holds any bytes that arrived behind the request.
			conn := stream.Conn()
			pctx, cancel := context.WithCancel(ctx)
			err := resp.Pipe(pctx, conn)
			cancel()
			return err
		}
		m = Message{}
		if err := stream.Receive(&m); err != nil {
			return receiveError(stream, m, err)
		}
	}
}

func handleV1(ctx context.Context, h Handler, req Request) Response {
	if err := CheckVersion(req.Version); err != nil {
		return Fail(req, NewError(ErrorCodeUnsupportedVersion, err))
	}
	return h.Handle(ctx, req)
}
