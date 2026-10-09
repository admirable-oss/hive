// Package protocol is Hive's wire layer: newline-delimited JSON over a unix
// socket, in two flavours on the same framing.
//
//   - Protocol 1: one request, one response, in order. A request may open a
//     byte pipe, which then takes over the connection. Hand-written clients
//     (nc, scripts) speak this.
//   - Protocol 2: the client starts with a hello and the connection becomes
//     multiplexed: concurrent requests, any number of pipes, one socket.
//
// It knows nothing about environments or processes. Domain packages plug in
// through the Handler contract (usually via the typed Method and PipeMethod
// adapters) and the runtime serves a Router over each accepted connection.
package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the protocol-1 envelope version stamped on requests and
// responses. A message without a version is accepted as the current one,
// which keeps the socket easy to drive by hand (e.g. with nc).
const Version = "1"

// Version2 is the multiplexed protocol a client asks for in its hello.
const Version2 = "2"

// CheckVersion reports whether v is a protocol-1 envelope version this side
// speaks.
func CheckVersion(v string) error {
	if v == "" || v == Version {
		return nil
	}
	return fmt.Errorf("%w: peer speaks protocol %q, this build speaks %q", ErrVersionMismatch, v, Version)
}

// DefaultMaxMessageSize caps a single framed message (1 MiB).
const DefaultMaxMessageSize int64 = 1 << 20

type MessageType string

const (
	MessageTypeRequest  MessageType = "request"
	MessageTypeResponse MessageType = "response"
	// Protocol 2 only.
	MessageTypeHello   MessageType = "hello"
	MessageTypeWelcome MessageType = "welcome"
	MessageTypeData    MessageType = "data"
	MessageTypeClose   MessageType = "close"
)

// Message is the wire envelope. Protocol-1 traffic uses only requests and
// responses; protocol 2 adds the handshake and pipe traffic.
type Message struct {
	Version string          `json:"version,omitempty"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	// Stream names a pipe: set on the response that opened it, and on its
	// data and close messages.
	Stream string `json:"stream,omitempty"`
	// Data carries pipe bytes (base64 in JSON).
	Data []byte `json:"data,omitempty"`
}

// Hello is a protocol-2 client's first message (in Params).
type Hello struct {
	Client        string   `json:"client"`
	ClientVersion string   `json:"client_version,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

// Welcome is the server's answer to a hello (in Result).
type Welcome struct {
	Protocol      string   `json:"protocol"`
	ServerVersion string   `json:"server_version,omitempty"`
	Methods       []string `json:"methods,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

// Request is one call from a client to the runtime.
type Request struct {
	Version string          `json:"version"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response answers exactly one Request.
type Response struct {
	Version string          `json:"version"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	// Stream is the pipe ID when the request opened a pipe (protocol 2).
	Stream string `json:"stream,omitempty"`

	// Pipe is never sent. When set, the request opens a byte pipe: the
	// response goes out first, then Pipe runs until it returns, reading what
	// the client sends and writing what the client receives. In protocol 1
	// the pipe is the rest of the connection; in protocol 2 it is one of
	// many streams on it.
	Pipe PipeFunc `json:"-"`
	// AfterSend is never sent. It runs once the response is written, e.g.
	// to stop the daemon only after its caller has the answer.
	AfterSend func() `json:"-"`
}

// Pipe is the server's end of a byte pipe. Reads return what the client
// sends and io.EOF once it closes its end; writes go to the client.
type Pipe interface {
	io.Reader
	io.Writer
}

// PipeFunc serves one pipe. Its context ends when the client goes away; the
// pipe closes when it returns, carrying the error (if any) to the client.
type PipeFunc func(ctx context.Context, p Pipe) error

// Empty is the result of calls that succeed without returning data.
type Empty struct{}

// NewRequest builds a request, encoding params as JSON when non-nil.
func NewRequest(id, method string, params any) (Request, error) {
	req := Request{Version: Version, Type: MessageTypeRequest, ID: id, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return Request{}, err
		}
		req.Params = raw
	}
	return req, nil
}

// Reply builds a successful response carrying result encoded as JSON.
func Reply(req Request, result any) Response {
	raw, err := json.Marshal(result)
	if err != nil {
		return Fail(req, err)
	}
	return Response{Version: Version, Type: MessageTypeResponse, ID: req.ID, Result: raw}
}

// Fail builds an error response. An *Error anywhere in err's chain keeps its
// code; anything else is reported as an internal error.
func Fail(req Request, err error) Response {
	return Response{Version: Version, Type: MessageTypeResponse, ID: req.ID, Error: AsError(err)}
}

func (m Message) request() Request {
	return Request{Version: m.Version, Type: m.Type, ID: m.ID, Method: m.Method, Params: m.Params}
}
