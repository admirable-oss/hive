package protocol

import (
	"context"
	"errors"
	"io"
	"net"
)

// HijackSignal is a sentinel error a handler returns inside its response to
// signal that it wants to take over the raw net.Conn after the response is
// sent. The connection server will call the HijackFunc with the conn.
type HijackSignal struct {
	Fn HijackFunc
}

func (h HijackSignal) Error() string { return "protocol: hijack" }

// HijackFunc receives the raw connection after the response has been flushed.
type HijackFunc func(ctx context.Context, conn net.Conn)

// HijackableResponse extends Response to carry an optional HijackFunc.
// When Hijack is non-nil, the connection server yields the conn to it.
type HijackableResponse struct {
	Response
	Hijack HijackFunc
}

// Connection interface now returns HijackableResponse so handlers can
// signal a connection upgrade.
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

		// Check if the handler requested a hijack via a special error marker
		// stored in the response metadata. We smuggle the HijackFunc via a
		// package-level registry keyed by request ID.
		hijackFn := drainHijack(request.ID)

		if err := c.codec.EncodeResponse(conn, response); err != nil {
			return err
		}

		if hijackFn != nil {
			// Yield the connection — the handler owns it from here.
			hijackFn(ctx, conn)
			return nil
		}
	}
}

// hijackRegistry is a request-scoped one-shot store for HijackFuncs.
// A handler calls RegisterHijack(requestID, fn) to schedule a hijack.
// The connection server drains it after sending the response.
var hijackRegistry = newHijackStore()

func RegisterHijack(requestID string, fn HijackFunc) {
	hijackRegistry.set(requestID, fn)
}

func drainHijack(requestID string) HijackFunc {
	return hijackRegistry.drain(requestID)
}
