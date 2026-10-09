package protocol

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
	"time"
)

// ErrProtocol1Only means the server answered a hello as a protocol-1
// request: it predates protocol 2. Callers fall back to protocol 1.
var ErrProtocol1Only = errors.New("protocol: server speaks only protocol 1")

// ErrConnClosed is returned for calls on a closed or broken connection.
var ErrConnClosed = errors.New("protocol: connection closed")

// MuxClient is the client end of a protocol-2 connection. It is safe for
// concurrent use: any number of calls and pipes share the connection.
type MuxClient struct {
	conn    net.Conn
	stream  *Stream
	welcome Welcome
	seq     atomic.Uint64

	wmu sync.Mutex // serialises writes

	mu      sync.Mutex
	pending map[string]chan pendingReply
	pipes   map[string]*ClientPipe

	done chan struct{}
	err  error // why the connection ended; set before done is closed
}

// Handshake starts protocol 2 on conn: it sends hello and waits for the
// welcome. On failure conn is closed.
func Handshake(ctx context.Context, conn net.Conn, hello Hello, maxMessageSize int64) (*MuxClient, error) {
	if maxMessageSize <= 0 {
		maxMessageSize = DefaultMaxMessageSize
	}
	stream := NewStream(conn, maxMessageSize)
	fail := func(err error) (*MuxClient, error) {
		_ = conn.Close()
		return nil, err
	}
	params, err := json.Marshal(hello)
	if err != nil {
		return fail(err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if err := stream.Send(Message{Version: Version2, Type: MessageTypeHello, ID: "hello", Params: params}); err != nil {
		return fail(fmt.Errorf("send hello: %w", err))
	}
	var reply Message
	if err := stream.Receive(&reply); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fail(fmt.Errorf("receive welcome: %w", err))
	}
	if !stop() {
		return fail(ctx.Err())
	}
	_ = conn.SetDeadline(time.Time{})
	switch {
	case reply.Type == MessageTypeWelcome:
	case reply.Type == MessageTypeResponse && reply.Error != nil:
		return fail(fmt.Errorf("%w (%s)", ErrProtocol1Only, reply.Error.Message))
	default:
		return fail(fmt.Errorf("%w: unexpected %q instead of welcome", ErrInvalidMessage, reply.Type))
	}
	c := &MuxClient{
		conn:    conn,
		stream:  stream,
		pending: make(map[string]chan pendingReply),
		pipes:   make(map[string]*ClientPipe),
		done:    make(chan struct{}),
	}
	if err := json.Unmarshal(reply.Result, &c.welcome); err != nil {
		return fail(fmt.Errorf("%w: welcome: %w", ErrInvalidMessage, err))
	}
	go c.readLoop()
	return c, nil
}

// Welcome returns what the server said about itself.
func (c *MuxClient) Welcome() Welcome { return c.welcome }

// Done is closed when the connection ends.
func (c *MuxClient) Done() <-chan struct{} { return c.done }

// Err reports why the connection ended (nil while it is open).
func (c *MuxClient) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close ends the connection. Pending calls fail and pipes end.
func (c *MuxClient) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}

func (c *MuxClient) write(m Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return ErrConnClosed
	default:
	}
	if err := c.stream.Send(m); err != nil {
		_ = c.conn.Close() // a failed write leaves the framing unknown
		return fmt.Errorf("%w: %w", ErrConnClosed, err)
	}
	return nil
}

func (c *MuxClient) readLoop() {
	var err error
	for {
		var msg Message
		if err = c.stream.Receive(&msg); err != nil {
			break
		}
		switch msg.Type {
		case MessageTypeResponse:
			c.deliverResponse(msg)
		case MessageTypeData:
			if p := c.pipe(msg.Stream); p != nil {
				p.deliver(msg.Data)
			}
		case MessageTypeClose:
			c.mu.Lock()
			p := c.pipes[msg.Stream]
			delete(c.pipes, msg.Stream)
			c.mu.Unlock()
			if p != nil {
				p.finish(msg.Error)
			}
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = ErrConnClosed
	}
	c.mu.Lock()
	c.err = err
	pipes := c.pipes
	c.pipes = map[string]*ClientPipe{}
	c.mu.Unlock()
	close(c.done)
	for _, p := range pipes {
		p.finish(&Error{Code: ErrorCodeUnavailable, Message: err.Error()})
	}
	_ = c.conn.Close()
}

// pendingReply is a response and, when it opened one, its pipe.
type pendingReply struct {
	msg  Message
	pipe *ClientPipe
}

// deliverResponse hands a response to its caller. A response that opens a
// pipe registers the pipe first, so data that follows finds it, and hands
// the pipe over with the response: the server may already have closed it
// (and this reader removed it) by the time the caller runs.
func (c *MuxClient) deliverResponse(msg Message) {
	c.mu.Lock()
	ch := c.pending[msg.ID]
	delete(c.pending, msg.ID)
	var p *ClientPipe
	if msg.Stream != "" && ch != nil {
		p = newClientPipe(c, msg.Stream)
		c.pipes[msg.Stream] = p
	}
	c.mu.Unlock()
	if ch == nil {
		if msg.Stream != "" {
			// The caller gave up; do not leave the pipe open on the server.
			go func() { _ = c.write(Message{Type: MessageTypeClose, Stream: msg.Stream}) }()
		}
		return
	}
	ch <- pendingReply{msg: msg, pipe: p}
}

func (c *MuxClient) pipe(id string) *ClientPipe {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pipes[id]
}

// roundTrip sends a request and waits for its response (and its pipe, if it
// opened one).
func (c *MuxClient) roundTrip(ctx context.Context, method string, params any) (Message, *ClientPipe, error) {
	id := strconv.FormatUint(c.seq.Add(1), 10)
	m := Message{Type: MessageTypeRequest, ID: id, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return Message{}, nil, err
		}
		m.Params = raw
	}
	ch := make(chan pendingReply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(m); err != nil {
		c.forget(id)
		return Message{}, nil, err
	}
	select {
	case r := <-ch:
		if r.msg.Error != nil {
			if r.pipe != nil {
				_ = r.pipe.Close()
			}
			return r.msg, nil, r.msg.Error
		}
		return r.msg, r.pipe, nil
	case <-ctx.Done():
		c.forget(id)
		// A response may have raced in; if it opened a pipe, close it.
		select {
		case r := <-ch:
			if r.pipe != nil {
				_ = r.pipe.Close()
			}
		default:
		}
		return Message{}, nil, ctx.Err()
	case <-c.done:
		return Message{}, nil, c.Err()
	}
}

func (c *MuxClient) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Call sends a request and decodes its result into result (when non-nil).
func (c *MuxClient) Call(ctx context.Context, method string, params, result any) error {
	resp, p, err := c.roundTrip(ctx, method, params)
	if err != nil {
		return err
	}
	if p != nil {
		_ = p.Close() // not expected by this caller
	}
	return decodeResult(resp.Result, result)
}

// OpenPipe sends a request that opens a pipe, decodes its result into result
// (when non-nil) and returns the pipe. ctx bounds only the opening.
func (c *MuxClient) OpenPipe(ctx context.Context, method string, params, result any) (*ClientPipe, error) {
	resp, p, err := c.roundTrip(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("%w: %s did not open a stream", ErrInvalidMessage, method)
	}
	if err := decodeResult(resp.Result, result); err != nil {
		_ = p.Close()
		return nil, err
	}
	return p, nil
}

func decodeResult(raw json.RawMessage, result any) error {
	if result == nil {
		return nil
	}
	if len(raw) == 0 {
		return fmt.Errorf("%w: missing result", ErrInvalidMessage)
	}
	return json.Unmarshal(raw, result)
}

// ClientPipe is the client end of a pipe. Reads return what the server
// sends, then io.EOF (or the server's error) when it closes the pipe.
// Writes send bytes to the server. Close ends the pipe from this side.
type ClientPipe struct {
	c    *MuxClient
	id   string
	in   chan []byte
	rest []byte

	once   sync.Once
	closed chan struct{} // closed by finish or Close
	err    error         // the server's error, if it sent one
}

func newClientPipe(c *MuxClient, id string) *ClientPipe {
	return &ClientPipe{c: c, id: id, in: make(chan []byte, pipeInQueue), closed: make(chan struct{})}
}

// deliver queues data from the server. It blocks while the reader of this
// pipe is behind, which applies backpressure to the whole connection.
func (p *ClientPipe) deliver(b []byte) {
	select {
	case p.in <- b:
	case <-p.closed:
	}
}

func (p *ClientPipe) finish(e *Error) {
	p.once.Do(func() {
		if e != nil {
			p.err = e
		}
		close(p.closed)
	})
}

func (p *ClientPipe) Read(b []byte) (int, error) {
	if len(p.rest) == 0 {
		select {
		case chunk := <-p.in:
			p.rest = chunk
		case <-p.closed:
			// Everything sent before the close was queued before it.
			select {
			case chunk := <-p.in:
				p.rest = chunk
			default:
				if p.err != nil {
					return 0, p.err
				}
				return 0, io.EOF
			}
		}
	}
	n := copy(b, p.rest)
	p.rest = p.rest[n:]
	return n, nil
}

func (p *ClientPipe) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		select {
		case <-p.closed:
			return written, io.ErrClosedPipe
		default:
		}
		n := min(len(b), PipeChunk)
		if err := p.c.write(Message{Type: MessageTypeData, Stream: p.id, Data: b[:n]}); err != nil {
			return written, err
		}
		written += n
		b = b[n:]
	}
	return written, nil
}

// Close ends the pipe. It is safe to call more than once.
func (p *ClientPipe) Close() error {
	first := false
	p.once.Do(func() {
		first = true
		close(p.closed)
	})
	if !first {
		return nil
	}
	p.c.mu.Lock()
	delete(p.c.pipes, p.id)
	p.c.mu.Unlock()
	err := p.c.write(Message{Type: MessageTypeClose, Stream: p.id})
	if errors.Is(err, ErrConnClosed) {
		return nil
	}
	return err
}
