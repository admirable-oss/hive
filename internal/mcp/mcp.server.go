// Package mcp serves tools over the Model Context Protocol: JSON-RPC 2.0,
// one message per line on stdin and stdout, as MCP clients (Claude Code,
// Codex, Cursor, …) run local servers. `hive mcp` uses it to let an agent
// drive Hive: start other agents, prompt them, wait for them, read them.
//
// Only what tools need is implemented: initialize, ping, tools/list,
// tools/call and cancellation. Calls run concurrently, so a long wait does
// not hold up a ping or another call.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sync"

	"github.com/admirable-oss/hive/internal/protocol"
)

// ProtocolVersions are the MCP revisions this server speaks, newest first.
var ProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one tool: its name, what it does, its input's JSON Schema, and
// the function that runs it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// ReadOnly marks tools that change nothing (a hint for clients).
	Annotations *Annotations `json:"annotations,omitempty"`

	call func(ctx context.Context, args json.RawMessage) (any, error)
}

// Annotations are hints about a tool's behaviour.
type Annotations struct {
	ReadOnly    bool `json:"readOnlyHint,omitempty"`
	Destructive bool `json:"destructiveHint,omitempty"`
}

// NewTool makes a tool whose arguments decode into P; its input schema is
// P's. The result is shown to the model as JSON (a string as itself).
func NewTool[P any](name, description string, fn func(ctx context.Context, args P) (any, error)) Tool {
	g := protocol.SchemaGenerator{Inline: true}
	schema, _ := g.Of(reflect.TypeFor[P]()).(map[string]any)
	if schema == nil || schema["type"] != "object" {
		schema = map[string]any{"type": "object"}
	}
	return Tool{
		Name: name, Description: description, InputSchema: schema,
		call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args P
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("invalid arguments: %w", err)
				}
			}
			return fn(ctx, args)
		},
	}
}

// Server is an MCP server.
type Server struct {
	Name, Version string
	// Instructions tell the model how to use the tools (sent at
	// initialize).
	Instructions string
	Tools        []Tool
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve answers messages from r on w until r ends or ctx is cancelled.
// Calls still running then are cancelled and waited for.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wmu      sync.Mutex
		enc      = json.NewEncoder(w)
		calls    sync.WaitGroup
		mu       sync.Mutex
		inflight = map[string]context.CancelFunc{}
	)
	defer func() {
		cancel() // the client is gone: so are its calls
		calls.Wait()
	}()
	send := func(m message) {
		m.JSONRPC = "2.0"
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(m)
	}
	reply := func(id json.RawMessage, result any, err *rpcError) {
		if len(id) == 0 {
			return // a notification: never answered
		}
		if err != nil {
			send(message{ID: id, Error: err})
			return
		}
		send(message{ID: id, Result: result})
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	lines := make(chan []byte)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		scanErr <- sc.Err()
	}()

	for {
		var line []byte
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok = <-lines:
		}
		if !ok {
			select {
			case err := <-scanErr:
				return err
			default:
				return nil
			}
		}
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			send(message{ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: err.Error()}})
			continue
		}
		switch m.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			version := ProtocolVersions[0]
			if slices.Contains(ProtocolVersions, p.ProtocolVersion) {
				version = p.ProtocolVersion
			}
			reply(m.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
				"instructions":    s.Instructions,
			}, nil)
		case "ping":
			reply(m.ID, map[string]any{}, nil)
		case "tools/list":
			reply(m.ID, map[string]any{"tools": s.Tools}, nil)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				reply(m.ID, nil, &rpcError{Code: codeInvalidParams, Message: err.Error()})
				continue
			}
			i := slices.IndexFunc(s.Tools, func(t Tool) bool { return t.Name == p.Name })
			if i < 0 {
				reply(m.ID, nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool " + p.Name})
				continue
			}
			tool := s.Tools[i]
			cctx, ccancel := context.WithCancel(ctx)
			key := string(m.ID)
			mu.Lock()
			inflight[key] = ccancel
			mu.Unlock()
			calls.Add(1)
			go func() {
				defer calls.Done()
				defer func() {
					mu.Lock()
					delete(inflight, key)
					mu.Unlock()
					ccancel()
				}()
				result, err := tool.call(cctx, p.Arguments)
				if cctx.Err() != nil && ctx.Err() == nil && errors.Is(err, context.Canceled) {
					return // cancelled by the client: no answer is expected
				}
				reply(m.ID, toolResult(result, err), nil)
			}()
		case "notifications/cancelled":
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil {
				mu.Lock()
				if c := inflight[string(p.RequestID)]; c != nil {
					c()
				}
				mu.Unlock()
			}
		default:
			if m.Method != "" {
				reply(m.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method})
			}
			// Notifications (initialized, progress) need nothing, and this
			// server sends no requests whose responses it would read.
		}
	}
}

// toolResult is a tools/call result: the value as text (JSON unless it is
// a string), or the error as text with isError set, so the model sees it.
func toolResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		}
	}
	text, ok := v.(string)
	if !ok {
		data, merr := json.MarshalIndent(v, "", "  ")
		if merr != nil {
			return toolResult(nil, merr)
		}
		text = string(data)
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}
