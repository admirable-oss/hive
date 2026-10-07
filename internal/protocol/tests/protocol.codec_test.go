package protocol_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/protocol"
)

func TestJSONCodec_Encode_Response(t *testing.T) {
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	resp := protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      "1",
		Result: map[string]any{
			"pong": true,
		},
	}

	var buf bytes.Buffer
	err := codec.EncodeResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}

	data := buf.Bytes()
	if len(data) == 0 {
		t.Fatal("encoded data is empty")
	}
	if data[len(data)-1] != '\n' {
		t.Fatal("encoded data should end with newline")
	}

	var decoded protocol.Response
	if err := json.Unmarshal(bytes.TrimRight(data, "\n"), &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.ID != "1" {
		t.Fatalf("expected ID 1, got %q", decoded.ID)
	}
}

func TestJSONCodec_Decode_Request(t *testing.T) {
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	raw := `{"version":"1","type":"request","id":"abc","method":"runtime.ping"}` + "\n"
	reader := strings.NewReader(raw)

	var req protocol.Request
	err := codec.DecodeRequest(reader, &req)
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if req.Version != "1" {
		t.Fatalf("expected version 1, got %q", req.Version)
	}
	if req.ID != "abc" {
		t.Fatalf("expected ID abc, got %q", req.ID)
	}
	if req.Method != "runtime.ping" {
		t.Fatalf("expected method runtime.ping, got %q", req.Method)
	}
}

func TestJSONCodec_Decode_MalformedJSON(t *testing.T) {
	codec := protocol.NewJSONCodec(protocol.DefaultMaxMessageSize)
	reader := strings.NewReader("not json at all\n")

	var req protocol.Request
	err := codec.DecodeRequest(reader, &req)
	if err == nil {
		t.Fatal("expected decode error for malformed JSON, got nil")
	}
}

func TestJSONCodec_Decode_OversizedRequest(t *testing.T) {
	codec := protocol.NewJSONCodec(32)
	big := `{"version":"1","type":"request","id":"1","method":"x","params":"` +
		strings.Repeat("A", 4096) +
		`"}` + "\n"
	reader := strings.NewReader(big)

	var req protocol.Request
	err := codec.DecodeRequest(reader, &req)
	if err == nil {
		t.Fatal("expected error for oversized request, got nil")
	}
}
