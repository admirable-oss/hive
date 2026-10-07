package protocol_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// serve runs protocol.Serve on one end of a pipe and returns the other end
// wrapped in a Stream, plus a channel with Serve's result.
func serve(t *testing.T, h protocol.Handler) (*protocol.Stream, <-chan error) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { client.Close() })
	done := make(chan error, 1)
	go func() { done <- protocol.Serve(context.Background(), server, h, protocol.DefaultMaxMessageSize) }()
	return protocol.NewStream(client, protocol.DefaultMaxMessageSize), done
}

func waitServe(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func roundTrip(t *testing.T, s *protocol.Stream, id, method string) protocol.Response {
	t.Helper()
	if err := s.Send(protocol.Request{Version: protocol.Version, Type: protocol.MessageTypeRequest, ID: id, Method: method}); err != nil {
		t.Fatalf("send: %v", err)
	}
	var resp protocol.Response
	if err := s.Receive(&resp); err != nil {
		t.Fatalf("receive: %v", err)
	}
	return resp
}

func TestServe_MultipleRequestsOnOneConnection(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("echo", echo())
	s, done := serve(t, r)

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if resp := roundTrip(t, s, id, "echo"); resp.ID != id || resp.Error != nil {
			t.Fatalf("request %s: unexpected response %+v", id, resp)
		}
	}
	_ = s.Close()
	if err := waitServe(t, done); err != nil {
		t.Fatalf("expected nil after client close, got %v", err)
	}
}

func TestServe_EOF(t *testing.T) {
	s, done := serve(t, protocol.NewRouter())
	_ = s.Close()
	if err := waitServe(t, done); err != nil {
		t.Fatalf("expected nil on EOF, got %v", err)
	}
}

func TestServe_MalformedRequestIsAnsweredThenClosed(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		done <- protocol.Serve(context.Background(), server, protocol.NewRouter(), protocol.DefaultMaxMessageSize)
	}()

	go func() { _, _ = client.Write([]byte("this is not valid json\n")) }()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("expected an error reply, got %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid_request, got %+v", resp.Error)
	}
	if err := waitServe(t, done); err == nil {
		t.Fatal("expected Serve to report the malformed request")
	}
}

func TestServe_HijackGetsTheConnection(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("upgrade", protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		resp := protocol.Reply(req, protocol.Empty{})
		resp.Hijack = func(_ context.Context, conn net.Conn) { _, _ = conn.Write([]byte("raw")) }
		return resp
	}))
	s, done := serve(t, r)

	if resp := roundTrip(t, s, "1", "upgrade"); resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	raw, _ := io.ReadAll(s.Conn())
	if string(raw) != "raw" {
		t.Fatalf("expected hijacked bytes, got %q", raw)
	}
	if err := waitServe(t, done); err != nil {
		t.Fatalf("expected nil after hijack, got %v", err)
	}
}
