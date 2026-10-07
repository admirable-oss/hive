package protocol_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/admirable-oss/hive/internal/protocol"
)

func echo() protocol.Handler {
	return protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		return protocol.Reply(req, map[string]string{"method": req.Method})
	})
}

func TestRouter_RegisterErrors(t *testing.T) {
	r := protocol.NewRouter()
	if err := r.Register("", echo()); !errors.Is(err, protocol.ErrInvalidMethod) {
		t.Fatalf("expected ErrInvalidMethod, got %v", err)
	}
	if err := r.Register("m", nil); !errors.Is(err, protocol.ErrNilHandler) {
		t.Fatalf("expected ErrNilHandler, got %v", err)
	}
	if err := r.Register("m", echo()); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register("m", echo()); !errors.Is(err, protocol.ErrMethodAlreadyExists) {
		t.Fatalf("expected ErrMethodAlreadyExists, got %v", err)
	}
}

func TestRouter_Dispatch(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("test.echo", echo())

	resp := r.Handle(context.Background(), protocol.Request{ID: "42", Method: "test.echo"})
	if resp.ID != "42" || resp.Error != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil || result["method"] != "test.echo" {
		t.Fatalf("unexpected result %s (%v)", resp.Result, err)
	}
}

func TestRouter_UnknownMethod(t *testing.T) {
	resp := protocol.NewRouter().Handle(context.Background(), protocol.Request{ID: "7", Method: "no.such.method"})
	if resp.ID != "7" || resp.Error == nil || resp.Error.Code != protocol.ErrorCodeUnknownMethod {
		t.Fatalf("expected unknown_method error, got %+v", resp)
	}
}

func TestMethod_TypedParamsAndErrors(t *testing.T) {
	type params struct {
		Name string `json:"name"`
	}
	h := protocol.Method(func(_ context.Context, p params) (string, error) {
		if p.Name == "" {
			return "", protocol.NewError(protocol.ErrorCodeNotFound, errors.New("nobody"))
		}
		if p.Name == "boom" {
			return "", errors.New("exploded")
		}
		return "hello " + p.Name, nil
	})

	call := func(raw string) protocol.Response {
		return h.Handle(context.Background(), protocol.Request{ID: "1", Params: json.RawMessage(raw)})
	}

	if resp := call(`{"name":"bee"}`); string(resp.Result) != `"hello bee"` {
		t.Fatalf("unexpected result: %s", resp.Result)
	}
	if resp := call(`{}`); resp.Error == nil || resp.Error.Code != protocol.ErrorCodeNotFound {
		t.Fatalf("expected not_found to keep its code, got %+v", resp.Error)
	}
	if resp := call(`{"name":"boom"}`); resp.Error == nil || resp.Error.Code != protocol.ErrorCodeInternal {
		t.Fatalf("expected internal_error, got %+v", resp.Error)
	}
	if resp := call(`{"name":`); resp.Error == nil || resp.Error.Code != protocol.ErrorCodeInvalidParams {
		t.Fatalf("expected invalid_params, got %+v", resp.Error)
	}
}
