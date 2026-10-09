package protocol

import (
	"context"
	"io"
)

// UntilClosed returns a context that ends when the client closes its end of
// p (its reads return), for pipes that only send. Input is discarded. Call
// stop when done; it waits for the watcher to finish.
//
// On protocol 1 the connection itself is the pipe, so stop only returns
// once the connection is closed: call it after the pipe function returns,
// when the server closes the connection.
func UntilClosed(ctx context.Context, p Pipe) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, p)
		cancel()
	}()
	return ctx, func() {
		cancel()
		if c, ok := p.(io.Closer); ok {
			_ = c.Close()
		}
		<-done
	}
}
