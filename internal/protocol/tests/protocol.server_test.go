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

func TestServe_Protocol1PipeGetsTheConnection(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("upgrade", protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		resp := protocol.Reply(req, protocol.Empty{})
		resp.Pipe = func(_ context.Context, p protocol.Pipe) error {
			buf := make([]byte, 3)
			if _, err := io.ReadFull(p, buf); err != nil {
				return err
			}
			_, err := p.Write([]byte("raw:" + string(buf)))
			return err
		}
		return resp
	}))
	s, done := serve(t, r)

	// The pipe's input is sent right behind the request, in the same write:
	// the server must not lose it to read-ahead.
	req, _ := protocol.NewRequest("1", "upgrade", nil)
	line, _ := json.Marshal(req)
	if _, err := s.Conn().Write(append(append(line, '\n'), "abc"...)); err != nil {
		t.Fatal(err)
	}
	var resp protocol.Response
	if err := s.Receive(&resp); err != nil || resp.Error != nil {
		t.Fatalf("unexpected reply %+v, %v", resp, err)
	}
	raw, _ := io.ReadAll(s.Conn())
	if string(raw) != "raw:abc" {
		t.Fatalf("expected piped bytes, got %q", raw)
	}
	if err := waitServe(t, done); err != nil {
		t.Fatalf("expected nil after the pipe ended, got %v", err)
	}
}

func TestServe_RejectsOtherProtocolVersionsButKeepsServing(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("echo", echo())
	s, done := serve(t, r)

	if err := s.Send(protocol.Request{Version: "99", Type: protocol.MessageTypeRequest, ID: "old", Method: "echo"}); err != nil {
		t.Fatal(err)
	}
	var resp protocol.Response
	if err := s.Receive(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrorCodeUnsupportedVersion || resp.ID != "old" {
		t.Fatalf("want unsupported_version for id old, got %+v", resp)
	}

	// Unversioned requests (hand-written ones) are treated as current.
	if err := s.Send(protocol.Request{ID: "bare", Method: "echo"}); err != nil {
		t.Fatal(err)
	}
	var bare protocol.Response
	if err := s.Receive(&bare); err != nil || bare.Error != nil {
		t.Fatalf("unversioned request should succeed: %+v, %v", bare, err)
	}
	_ = s.Close()
	if err := waitServe(t, done); err != nil {
		t.Fatal(err)
	}
}
