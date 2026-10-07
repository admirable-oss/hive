package protocol_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

type echoHandler struct{}

func (echoHandler) Handle(
	_ context.Context,
	req protocol.Request,
) protocol.Response {
	return protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result: protocol.MustEncodeResult(map[string]any{
			"method": req.Method,
		}),
	}
}

func newTestProtocol() protocol.Protocol {
	s := protocol.NewService()
	_ = s.Register("echo", echoHandler{})
	return s
}

func writeRequest(conn net.Conn, req protocol.Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

func readResponse(conn net.Conn, resp *protocol.Response) error {
	dec := json.NewDecoder(conn)
	return dec.Decode(resp)
}

func TestConnection_Serve_RequestResponse(t *testing.T) {
	proto := newTestProtocol()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	conn := protocol.NewConnection(proto, codec)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- conn.Serve(context.Background(), serverConn)
	}()

	req := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "r1",
		Method:  "echo",
	}
	if err := writeRequest(clientConn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	var resp protocol.Response
	if err := readResponse(clientConn, &resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.ID != "r1" {
		t.Fatalf("expected ID r1, got %q", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["method"] != "echo" {
		t.Fatalf("unexpected result: %+v", result)
	}

	_ = clientConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return after client close")
	}
}

func TestConnection_Serve_MultipleRequests(t *testing.T) {
	proto := newTestProtocol()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	connHandler := protocol.NewConnection(proto, codec)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- connHandler.Serve(context.Background(), serverConn)
	}()

	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		req := protocol.Request{
			Version: protocol.Version,
			Type:    protocol.MessageTypeRequest,
			ID:      id,
			Method:  "echo",
		}
		if err := writeRequest(clientConn, req); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
		var resp protocol.Response
		if err := readResponse(clientConn, &resp); err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		if resp.ID != id {
			t.Fatalf("iteration %d: expected ID %q, got %q", i, id, resp.ID)
		}
	}

	_ = clientConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return after client close")
	}
}

func TestConnection_Serve_EOF(t *testing.T) {
	proto := newTestProtocol()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	connHandler := protocol.NewConnection(proto, codec)

	serverConn, clientConn := net.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- connHandler.Serve(context.Background(), serverConn)
	}()

	_ = clientConn.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on EOF, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return on EOF")
	}
}

func TestConnection_Serve_MalformedRequest(t *testing.T) {
	proto := newTestProtocol()
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	connHandler := protocol.NewConnection(proto, codec)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- connHandler.Serve(context.Background(), serverConn)
	}()

	_, _ = clientConn.Write([]byte("this is not valid json\n"))

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected decode error for malformed request, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return on malformed request")
	}
}
