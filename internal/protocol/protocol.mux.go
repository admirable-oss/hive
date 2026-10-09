package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// Protocol-2 limits. They bound what one connection can make the server
// hold: goroutines, queued output and buffered pipe input.
const (
	maxConcurrentRequests = 64
	maxPipesPerConn       = 256
	outQueue              = 256
	pipeInQueue           = 64
	// PipeChunk is the largest payload of one data message.
	PipeChunk = 32 << 10
	// pipeInputStall bounds how long a pipe may leave its input unread
	// before it is closed; until then the client's other traffic waits.
	pipeInputStall = 30 * time.Second
)

type outMsg struct {
	msg   Message
	after func()
}

type muxServer struct {
	stream *Stream
	h      Handler
	ctx    context.Context
	cancel context.CancelFunc
	out    chan outMsg
	sem    chan struct{}

	mu       sync.Mutex
	pipes    map[string]*serverPipe
	nextPipe uint64

	wg sync.WaitGroup
}

func serveMux(ctx context.Context, stream *Stream, h Handler, info ServerInfo, hello Message) error {
	ctx, cancel := context.WithCancel(ctx)
	m := &muxServer{
		stream: stream,
		h:      h,
		ctx:    ctx,
		cancel: cancel,
		out:    make(chan outMsg, outQueue),
		sem:    make(chan struct{}, maxConcurrentRequests),
		pipes:  make(map[string]*serverPipe),
	}
	writerDone := make(chan struct{})
	go m.writeLoop(writerDone)

	welcome := Welcome{Protocol: Version2, ServerVersion: info.Version, Capabilities: info.Capabilities}
	if lister, ok := h.(interface{ Methods() []string }); ok {
		welcome.Methods = lister.Methods()
	}
	raw, _ := json.Marshal(welcome)
	_ = m.send(Message{Version: Version2, Type: MessageTypeWelcome, ID: hello.ID, Result: raw}, nil)

	err := m.readLoop()
	cancel()
	m.wg.Wait()
	<-writerDone
	return err
}

func (m *muxServer) readLoop() error {
	for {
		var msg Message
		if err := m.stream.Receive(&msg); err != nil {
			switch {
			case errors.Is(err, io.EOF):
				return nil
			case errors.Is(err, ErrInvalidMessage):
				// The bad frame was consumed; the connection stays usable.
				_ = m.send(Message{Type: MessageTypeResponse, Error: NewError(ErrorCodeInvalidRequest, err)}, nil)
				continue
			default:
				return err
			}
		}
		switch msg.Type {
		case MessageTypeRequest:
			m.handleRequest(msg)
		case MessageTypeData:
			m.pipeData(msg)
		case MessageTypeClose:
			m.pipeClose(msg)
		default:
			_ = m.send(Message{Type: MessageTypeResponse, ID: msg.ID, Error: &Error{Code: ErrorCodeInvalidRequest, Message: fmt.Sprintf("unexpected message type %q", msg.Type)}}, nil)
		}
	}
}

// send queues msg for the writer; after, if set, runs once it is written.
func (m *muxServer) send(msg Message, after func()) error {
	select {
	case m.out <- outMsg{msg, after}:
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

func (m *muxServer) writeLoop(done chan struct{}) {
	defer close(done)
	for {
		select {
		case o := <-m.out:
			if err := m.stream.Send(o.msg); err != nil {
				m.cancel()
				return
			}
			if o.after != nil {
				o.after()
			}
		case <-m.ctx.Done():
			// Flush what is already queued (e.g. a shutdown reply), without
			// waiting for more.
			for {
				select {
				case o := <-m.out:
					if m.stream.Send(o.msg) != nil {
						return
					}
					if o.after != nil {
						o.after()
					}
				default:
					return
				}
			}
		}
	}
}

func (m *muxServer) handleRequest(msg Message) {
	req := msg.request()
	select {
	case m.sem <- struct{}{}:
	case <-m.ctx.Done():
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		var resp Response
		if req.Version != "" && req.Version != Version && req.Version != Version2 {
			resp = Fail(req, NewError(ErrorCodeUnsupportedVersion, fmt.Errorf("%w: request version %q", ErrVersionMismatch, req.Version)))
		} else {
			resp = m.h.Handle(m.ctx, req)
		}
		<-m.sem // a pipe may run for hours; it must not hold a request slot

		out := Message{Type: MessageTypeResponse, ID: req.ID, Result: resp.Result, Error: resp.Error}
		if resp.Pipe == nil || resp.Error != nil {
			_ = m.send(out, resp.AfterSend)
			return
		}
		p, err := m.openPipe()
		if err != nil {
			out.Result, out.Error = nil, AsError(err)
			_ = m.send(out, resp.AfterSend)
			return
		}
		out.Stream = p.id
		if m.send(out, resp.AfterSend) != nil {
			m.closePipe(p, nil)
			return
		}
		err = resp.Pipe(p.ctx, p)
		m.closePipe(p, err)
	}()
}

func (m *muxServer) openPipe() (*serverPipe, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pipes) >= maxPipesPerConn {
		return nil, NewError(ErrorCodeUnavailable, fmt.Errorf("too many open streams (limit %d)", maxPipesPerConn))
	}
	m.nextPipe++
	ctx, cancel := context.WithCancel(m.ctx)
	p := &serverPipe{m: m, id: "s" + strconv.FormatUint(m.nextPipe, 10), ctx: ctx, cancel: cancel, in: make(chan []byte, pipeInQueue)}
	m.pipes[p.id] = p
	return p, nil
}

// closePipe ends p and tells the client, with err unless it was only the
// pipe's context ending.
func (m *muxServer) closePipe(p *serverPipe, err error) {
	p.cancel()
	m.mu.Lock()
	delete(m.pipes, p.id)
	m.mu.Unlock()
	msg := Message{Type: MessageTypeClose, Stream: p.id}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		msg.Error = AsError(err)
	}
	_ = m.send(msg, nil)
}

func (m *muxServer) lookup(id string) *serverPipe {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pipes[id]
}

func (m *muxServer) pipeData(msg Message) {
	p := m.lookup(msg.Stream)
	if p == nil {
		return // the pipe already ended; late input is dropped
	}
	stall := time.NewTimer(pipeInputStall)
	defer stall.Stop()
	select {
	case p.in <- msg.Data:
	case <-p.ctx.Done():
	case <-stall.C:
		p.cancel() // it stopped reading; free the connection
	}
}

func (m *muxServer) pipeClose(msg Message) {
	if p := m.lookup(msg.Stream); p != nil {
		p.cancel()
	}
}

// serverPipe is one pipe on a protocol-2 connection.
type serverPipe struct {
	m      *muxServer
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	in     chan []byte
	rest   []byte
}

func (p *serverPipe) Read(b []byte) (int, error) {
	if len(p.rest) == 0 {
		select {
		case chunk := <-p.in:
			p.rest = chunk
		case <-p.ctx.Done():
			return 0, io.EOF
		}
	}
	n := copy(b, p.rest)
	p.rest = p.rest[n:]
	return n, nil
}

// Close ends the pipe's reads early; the pipe itself closes when its
// function returns.
func (p *serverPipe) Close() error {
	p.cancel()
	return nil
}

func (p *serverPipe) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		if p.ctx.Err() != nil {
			return written, io.ErrClosedPipe
		}
		n := min(len(b), PipeChunk)
		data := append([]byte(nil), b[:n]...) // encoded later by the writer
		if err := p.m.send(Message{Type: MessageTypeData, Stream: p.id, Data: data}, nil); err != nil {
			return written, io.ErrClosedPipe
		}
		written += n
		b = b[n:]
	}
	return written, nil
}
