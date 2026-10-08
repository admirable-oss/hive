// Package protocol is Hive's wire layer: newline-delimited JSON requests and
// responses over a unix socket.
//
// It knows nothing about environments or processes. Domain packages plug in
// through the Handler contract (usually via the typed Method adapter) and the
// runtime serves a Router over each accepted connection.
package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
)

// Version is stamped on every message so incompatible peers can be detected.
// A message without a version is accepted as the current one, which keeps
// the socket easy to drive by hand (e.g. with nc).
const Version = "1"

// CheckVersion reports whether v is a version this side speaks.
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
)

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

	// Hijack is never sent. When set, the server writes the response and then
	// gives the raw connection to Hijack, which owns it until it returns.
	// terminal.attach uses this to turn the socket into a terminal stream.
	Hijack HijackFunc `json:"-"`
}

// HijackFunc takes over a connection after its response has been flushed.
type HijackFunc func(ctx context.Context, conn net.Conn)

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
