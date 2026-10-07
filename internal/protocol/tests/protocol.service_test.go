package protocol_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admirable-oss/hive/internal/protocol"
)

type testHandler struct {
	called bool
	result any
}

func newTestHandler(result any) *testHandler {
	return &testHandler{result: result}
}

func (h *testHandler) Handle(
	_ context.Context,
	req protocol.Request,
) protocol.Response {
	h.called = true
	return protocol.Response{
		Version: protocol.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  h.result,
	}
}

func TestService_Register_Success(t *testing.T) {
	s := protocol.NewService()
	h := newTestHandler(nil)

	err := s.Register("test.method", h)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestService_Register_EmptyMethod(t *testing.T) {
	s := protocol.NewService()
	h := newTestHandler(nil)

	err := s.Register("", h)
	if !errors.Is(err, protocol.ErrInvalidMethod) {
		t.Fatalf("expected ErrInvalidMethod, got: %v", err)
	}
}

func TestService_Register_NilHandler(t *testing.T) {
	s := protocol.NewService()

	err := s.Register("test.method", nil)
	if !errors.Is(err, protocol.ErrNilHandler) {
		t.Fatalf("expected ErrNilHandler, got: %v", err)
	}
}

func TestService_Register_DuplicateMethod(t *testing.T) {
	s := protocol.NewService()
	h1 := newTestHandler(nil)
	h2 := newTestHandler(nil)

	err := s.Register("test.method", h1)
	if err != nil {
		t.Fatalf("unexpected first register error: %v", err)
	}

	err = s.Register("test.method", h2)
	if !errors.Is(err, protocol.ErrMethodAlreadyExists) {
		t.Fatalf("expected ErrMethodAlreadyExists, got: %v", err)
	}
}

func TestService_Handle_Dispatch(t *testing.T) {
	s := protocol.NewService()
	expected := map[string]any{"ok": true}
	h := newTestHandler(expected)

	_ = s.Register("test.ping", h)

	req := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "42",
		Method:  "test.ping",
	}

	resp := s.Handle(context.Background(), req)

	if !h.called {
		t.Fatal("handler was not called")
	}
	if resp.ID != "42" {
		t.Fatalf("expected ID 42, got %q", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result to be map[string]any, got %T", resp.Result)
	}
	if result["ok"] != true {
		t.Fatalf("expected ok=true, got %v", result["ok"])
	}
}

func TestService_Handle_UnknownMethod(t *testing.T) {
	s := protocol.NewService()

	req := protocol.Request{
		Version: protocol.Version,
		Type:    protocol.MessageTypeRequest,
		ID:      "7",
		Method:  "no.such.method",
	}

	resp := s.Handle(context.Background(), req)

	if resp.ID != "7" {
		t.Fatalf("expected ID 7, got %q", resp.ID)
	}
	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != protocol.ErrorCodeUnknownMethod {
		t.Fatalf(
			"expected error code %q, got %q",
			protocol.ErrorCodeUnknownMethod,
			resp.Error.Code,
		)
	}
}
