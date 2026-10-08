package protocol_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/protocol"
)

// streamOver returns a Stream whose peer writes raw and then hangs up.
func streamOver(t *testing.T, raw string, limit int64) *protocol.Stream {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	go func() {
		_, _ = client.Write([]byte(raw))
		_ = client.Close()
	}()
	return protocol.NewStream(server, limit)
}

func TestStream_SendWritesOneLine(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_ = protocol.NewStream(server, protocol.DefaultMaxMessageSize).
			Send(protocol.Reply(protocol.Request{ID: "1"}, map[string]bool{"pong": true}))
	}()

	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "1" || resp.Type != protocol.MessageTypeResponse {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestStream_ReceiveRequest(t *testing.T) {
	s := streamOver(t, `{"version":"1","type":"request","id":"abc","method":"runtime.ping"}`+"\n", protocol.DefaultMaxMessageSize)

	var req protocol.Request
	if err := s.Receive(&req); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if req.Version != "1" || req.ID != "abc" || req.Method != "runtime.ping" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestStream_ReceiveMalformed(t *testing.T) {
	s := streamOver(t, "not json at all\n", protocol.DefaultMaxMessageSize)

	var req protocol.Request
	if err := s.Receive(&req); !errors.Is(err, protocol.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage, got %v", err)
	}
}

func TestStream_ReceiveOversized(t *testing.T) {
	big := `{"id":"1","method":"x","params":"` + strings.Repeat("A", 4096) + `"}` + "\n"
	s := streamOver(t, big, 32)

	var req protocol.Request
	if err := s.Receive(&req); !errors.Is(err, protocol.ErrMessageTooLarge) {
		t.Fatalf("expected ErrMessageTooLarge, got %v", err)
	}
}

// Bytes that arrive right behind a frame (terminal data after the attach ack)
// must reach whoever takes over the connection.
func TestStream_ConnKeepsBytesAfterFrame(t *testing.T) {
	s := streamOver(t, `{"id":"1","method":"terminal.attach"}`+"\nraw terminal bytes", protocol.DefaultMaxMessageSize)

	var req protocol.Request
	if err := s.Receive(&req); err != nil {
		t.Fatalf("receive: %v", err)
	}
	rest, err := io.ReadAll(s.Conn())
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if string(rest) != "raw terminal bytes" {
		t.Fatalf("expected trailing bytes to survive, got %q", rest)
	}
}
