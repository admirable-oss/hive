package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type echoArgs struct {
	Text  string `json:"text" desc:"what to say"`
	Times int    `json:"times,omitempty"`
}

// session runs a server over pipes and reads its answers.
type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
}

func serve(t *testing.T, tools ...Tool) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	srv := &Server{Name: "test", Version: "1", Instructions: "be nice", Tools: tools}
	go func() {
		err := srv.Serve(context.Background(), inR, outW)
		_ = outW.Close()
		s.done <- err
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		go func() { _, _ = io.Copy(io.Discard, outR) }()
		if err := <-s.done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return s
}

func (s *session) send(msg string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, msg+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) next() map[string]any {
	s.t.Helper()
	if !s.out.Scan() {
		s.t.Fatalf("no answer: %v", s.out.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(s.out.Bytes(), &m); err != nil {
		s.t.Fatalf("%s: %v", s.out.Bytes(), err)
	}
	return m
}

func text(m map[string]any) string {
	content := m["result"].(map[string]any)["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func TestInitializeListAndCall(t *testing.T) {
	echo := NewTool("echo", "says it back", func(_ context.Context, a echoArgs) (any, error) {
		if a.Text == "" {
			return nil, errors.New("nothing to say")
		}
		return map[string]any{"said": strings.Repeat(a.Text, max(a.Times, 1))}, nil
	})
	s := serve(t, echo)

	s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	init := s.next()["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["instructions"] != "be nice" || init["serverInfo"].(map[string]any)["name"] != "test" {
		t.Fatalf("initialize = %v", init)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`) // no answer

	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	m := s.next()
	if m["id"].(float64) != 2 {
		t.Fatalf("answered %v", m)
	}
	tool := m["result"].(map[string]any)["tools"].([]any)[0].(map[string]any)
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if tool["name"] != "echo" || props["text"].(map[string]any)["description"] != "what to say" ||
		props["times"].(map[string]any)["type"] != "integer" {
		t.Fatalf("tool = %v", tool)
	}
	if req := tool["inputSchema"].(map[string]any)["required"].([]any); len(req) != 1 || req[0] != "text" {
		t.Fatalf("required = %v", req)
	}

	s.send(`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"echo","arguments":{"text":"hi","times":2}}}`)
	if got := s.next(); !strings.Contains(text(got), `"said": "hihi"`) || got["id"] != "a" {
		t.Fatalf("call = %v", got)
	}
	s.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	if got := s.next(); text(got) != "nothing to say" || got["result"].(map[string]any)["isError"] != true {
		t.Fatalf("failing call = %v", got)
	}
	s.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`)
	if got := s.next(); got["error"].(map[string]any)["code"].(float64) != codeInvalidParams {
		t.Fatalf("unknown tool = %v", got)
	}
	s.send(`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`)
	if got := s.next(); got["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Fatalf("unknown method = %v", got)
	}
	s.send(`not json`)
	if got := s.next(); got["error"].(map[string]any)["code"].(float64) != codeParse {
		t.Fatalf("garbage = %v", got)
	}
}

func TestCallsRunConcurrentlyAndCancel(t *testing.T) {
	started := make(chan struct{})
	block := NewTool("block", "waits until cancelled", func(ctx context.Context, _ struct{}) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s := serve(t, block)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`)
	<-started
	s.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if got := s.next(); got["id"].(float64) != 2 {
		t.Fatalf("ping behind a running call: %v", got)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	s.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	// The cancelled call is not answered; the next answer is the ping's.
	deadline := time.After(5 * time.Second)
	got := make(chan map[string]any, 1)
	go func() { got <- s.next() }()
	select {
	case m := <-got:
		if m["id"].(float64) != 3 {
			t.Fatalf("after cancel: %v", m)
		}
	case <-deadline:
		t.Fatal("no answer after cancelling")
	}
}

func TestHiveToolsHaveSchemas(t *testing.T) {
	tools := HiveTools(nil)
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" || tool.InputSchema["type"] != "object" {
			t.Errorf("%s: description %q, schema %v", tool.Name, tool.Description, tool.InputSchema)
		}
	}
	for _, want := range []string{"agent_start", "agent_prompt", "agent_wait", "agent_read", "agent_send_keys", "pane_read", "env_list"} {
		if !seen[want] {
			t.Errorf("no %s tool", want)
		}
	}
	data, _ := json.Marshal(tools)
	if strings.Contains(string(data), "$ref") {
		t.Error("tool schemas use $ref, which some clients do not resolve")
	}
}

func TestClosingStdinCancelsRunningCalls(t *testing.T) {
	started := make(chan struct{})
	block := NewTool("block", "waits until cancelled", func(ctx context.Context, _ struct{}) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s := serve(t, block)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block"}}`)
	<-started
	// Cleanup closes stdin; Serve must return rather than wait forever.
}
