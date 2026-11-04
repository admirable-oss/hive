package editor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"

	"github.com/admirable-oss/hive/internal/protocol"
)

// MaxMessageBytes bounds one message from the editor.
const MaxMessageBytes = 16 << 20

// codec reads and writes whole JSON-RPC messages.
type codec interface {
	read() ([]byte, error)
	write(msg []byte) error
}

// lineCodec is one message per line, as `hive editor serve` speaks.
type lineCodec struct {
	sc *bufio.Scanner
	mu sync.Mutex
	w  io.Writer
}

func newLineCodec(r io.Reader, w io.Writer) *lineCodec {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), MaxMessageBytes)
	return &lineCodec{sc: sc, w: w}
}

func (c *lineCodec) read() ([]byte, error) {
	for c.sc.Scan() {
		if line := c.sc.Bytes(); len(strings.TrimSpace(string(line))) > 0 {
			return append([]byte(nil), line...), nil
		}
	}
	if err := c.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (c *lineCodec) write(msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.w.Write(append(msg, '\n'))
	return err
}

// headerCodec is Content-Length framing, as language servers speak.
type headerCodec struct {
	r  *textproto.Reader
	br *bufio.Reader
	mu sync.Mutex
	w  io.Writer
}

func newHeaderCodec(r io.Reader, w io.Writer) *headerCodec {
	br := bufio.NewReaderSize(r, 64<<10)
	return &headerCodec{r: textproto.NewReader(br), br: br, w: w}
}

func (c *headerCodec) read() ([]byte, error) {
	h, err := c.r.ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) && len(h) == 0 {
			return nil, io.EOF
		}
		return nil, err
	}
	n, err := strconv.Atoi(h.Get("Content-Length"))
	if err != nil || n < 0 || n > MaxMessageBytes {
		return nil, fmt.Errorf("editor: bad Content-Length %q", h.Get("Content-Length"))
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *headerCodec) write(msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(msg)); err != nil {
		return err
	}
	_, err := c.w.Write(msg)
	return err
}

// message is a JSON-RPC 2.0 request, response or notification.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error. Data carries Hive's own error code
// (not_found, conflict, …), which editors act on.
type rpcError struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    *errorData `json:"data,omitempty"`
}

type errorData struct {
	Code string `json:"code"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	// codeHive is a Hive error; its data says which.
	codeHive = -32000
	// codeCancelled is LSP's RequestCancelled.
	codeCancelled = -32800
)

// toRPCError maps a Hive error to its JSON-RPC form.
func toRPCError(err error) *rpcError {
	if errors.Is(err, context.Canceled) {
		return &rpcError{Code: codeCancelled, Message: "cancelled"}
	}
	pe := protocol.AsError(err)
	e := &rpcError{Code: codeHive, Message: pe.Message, Data: &errorData{Code: pe.Code}}
	switch pe.Code {
	case protocol.ErrorCodeUnknownMethod:
		e.Code = codeMethodNotFound
	case protocol.ErrorCodeInvalidParams:
		e.Code = codeInvalidParams
	case protocol.ErrorCodeInternal:
		e.Code = codeInternal
	}
	return e
}

// handler answers a request (and is given notifications, whose result is
// dropped).
type handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// conn serves one editor: requests run concurrently, so a long call (a
// gate run, say) holds up nothing else, and notifications go out between
// them.
type conn struct {
	codec  codec
	handle handler

	mu       sync.Mutex
	inflight map[string]context.CancelFunc
	calls    sync.WaitGroup
}

func newConn(c codec, h handler) *conn {
	return &conn{codec: c, handle: h, inflight: map[string]context.CancelFunc{}}
}

// notify sends a notification. It fails once the editor is gone.
func (c *conn) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	msg, err := json.Marshal(message{JSONRPC: "2.0", Method: method, Params: raw})
	if err != nil {
		return err
	}
	return c.codec.write(msg)
}

func (c *conn) reply(id json.RawMessage, result any, err error) {
	m := message{JSONRPC: "2.0", ID: id}
	if err != nil {
		m.Error = toRPCError(err)
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			m.Error = toRPCError(merr)
		} else {
			m.Result = raw
		}
	}
	msg, _ := json.Marshal(m)
	_ = c.codec.write(msg)
}

// serve answers messages until the editor closes its end or ctx ends;
// calls still running are then cancelled and waited for.
func (c *conn) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		c.calls.Wait()
	}()
	type read struct {
		msg []byte
		err error
	}
	reads := make(chan read)
	go func() {
		for {
			msg, err := c.codec.read()
			select {
			case reads <- read{msg, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var r read
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r = <-reads:
		}
		if r.err != nil {
			if errors.Is(r.err, io.EOF) {
				return nil
			}
			return r.err
		}
		var m message
		if err := json.Unmarshal(r.msg, &m); err != nil {
			msg, _ := json.Marshal(message{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: err.Error()}})
			_ = c.codec.write(msg)
			continue
		}
		if m.Method == "" {
			continue // a response: the bridge sends no requests
		}
		if m.Method == "$/cancelRequest" {
			var p struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				c.mu.Lock()
				if stop, ok := c.inflight[string(p.ID)]; ok {
					stop()
				}
				c.mu.Unlock()
			}
			continue
		}
		c.calls.Add(1)
		go c.call(ctx, m)
	}
}

func (c *conn) call(ctx context.Context, m message) {
	defer c.calls.Done()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	key := string(m.ID)
	if len(m.ID) > 0 {
		c.mu.Lock()
		c.inflight[key] = stop
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
		}()
	}
	result, err := c.handle(ctx, m.Method, m.Params)
	if len(m.ID) == 0 {
		return // a notification: never answered
	}
	c.reply(m.ID, result, err)
}

// routerHandler dispatches requests to a protocol router, whose typed
// methods give the protocol its schema.
func routerHandler(r *protocol.Router) handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		resp := r.Handle(ctx, protocol.Request{Version: protocol.Version, Type: protocol.MessageTypeRequest, ID: "1", Method: method, Params: params})
		if resp.Error != nil {
			return nil, resp.Error
		}
		return json.RawMessage(resp.Result), nil
	}
}
