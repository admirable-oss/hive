package protocol_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
)

// muxPair serves r on one end of a pipe and handshakes protocol 2 on the
// other.
func muxPair(t *testing.T, r *protocol.Router) *protocol.MuxClient {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- protocol.ServeConn(context.Background(), server, r, protocol.ServerInfo{Version: "test", Capabilities: []string{"frames/1"}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := protocol.Handshake(ctx, client, protocol.Hello{Client: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop after the client closed")
		}
	})
	return c
}

func TestMux_WelcomeListsMethodsAndCapabilities(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("b.method", echo())
	r.MustRegister("a.method", echo())
	c := muxPair(t, r)
	w := c.Welcome()
	if w.Protocol != protocol.Version2 || w.ServerVersion != "test" || len(w.Capabilities) != 1 {
		t.Fatalf("welcome = %+v", w)
	}
	if strings.Join(w.Methods, ",") != "a.method,b.method" {
		t.Fatalf("methods = %v", w.Methods)
	}
}

// TestMux_ConcurrentCallsAreIndependent proves calls share the connection:
// a slow call does not hold up a fast one sent after it.
func TestMux_ConcurrentCallsAreIndependent(t *testing.T) {
	release := make(chan struct{})
	r := protocol.NewRouter()
	r.MustRegister("slow", protocol.Method(func(context.Context, struct{}) (string, error) {
		<-release
		return "slow", nil
	}))
	r.MustRegister("fast", protocol.Method(func(_ context.Context, n int) (int, error) { return n * 2, nil }))
	c := muxPair(t, r)

	slow := make(chan error, 1)
	go func() {
		var s string
		slow <- c.Call(context.Background(), "slow", nil, &s)
	}()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			var got int
			if err := c.Call(context.Background(), "fast", i, &got); err != nil || got != i*2 {
				t.Errorf("fast(%d) = %d, %v", i, got, err)
			}
		})
	}
	wg.Wait()
	close(release)
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}

func TestMux_ErrorsKeepTheirCodes(t *testing.T) {
	c := muxPair(t, protocol.NewRouter())
	err := c.Call(context.Background(), "missing", nil, nil)
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodeUnknownMethod {
		t.Fatalf("got %v, want unknown_method", err)
	}
}

func echoPipe() protocol.Handler {
	return protocol.PipeMethod(func(_ context.Context, prefix string) (string, protocol.PipeFunc, error) {
		return "open", func(_ context.Context, p protocol.Pipe) error {
			buf := make([]byte, 4096)
			for {
				n, err := p.Read(buf)
				if n > 0 {
					if _, werr := p.Write(append([]byte(prefix), buf[:n]...)); werr != nil {
						return werr
					}
				}
				if err != nil {
					return nil
				}
			}
		}, nil
	})
}

func TestMux_PipesAreBidirectionalAndIndependent(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("echo.pipe", echoPipe())
	r.MustRegister("ping", echo())
	c := muxPair(t, r)

	var pipes []*protocol.ClientPipe
	for _, prefix := range []string{"A:", "B:"} {
		var res string
		p, err := c.OpenPipe(context.Background(), "echo.pipe", prefix, &res)
		if err != nil || res != "open" {
			t.Fatalf("open: %v %q", err, res)
		}
		pipes = append(pipes, p)
	}
	for i, p := range pipes {
		msg := []byte("hello")
		if _, err := p.Write(msg); err != nil {
			t.Fatal(err)
		}
		want := []string{"A:hello", "B:hello"}[i]
		got := make([]byte, len(want))
		if _, err := io.ReadFull(p, got); err != nil || string(got) != want {
			t.Fatalf("pipe %d echoed %q, %v", i, got, err)
		}
	}
	// Requests still flow while pipes are open.
	if err := c.Call(context.Background(), "ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range pipes {
		_ = p.Close()
		_ = p.Close()
	}
}

func TestMux_LargePayloadsArriveIntact(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("echo.pipe", echoPipe())
	c := muxPair(t, r)
	p, err := c.OpenPipe(context.Background(), "echo.pipe", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), 40_000) // 640 KiB: many chunks
	go func() { _, _ = p.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload corrupted in transit")
	}
	_ = p.Close()
}

func TestMux_ServerClosingAPipeEndsReadsWithItsError(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("finite", protocol.PipeMethod(func(context.Context, struct{}) (protocol.Empty, protocol.PipeFunc, error) {
		return protocol.Empty{}, func(_ context.Context, p protocol.Pipe) error {
			_, _ = p.Write([]byte("bye"))
			return nil
		}, nil
	}))
	r.MustRegister("failing", protocol.PipeMethod(func(context.Context, struct{}) (protocol.Empty, protocol.PipeFunc, error) {
		return protocol.Empty{}, func(context.Context, protocol.Pipe) error {
			return protocol.NewError(protocol.ErrorCodeNotFound, errors.New("agent vanished"))
		}, nil
	}))
	c := muxPair(t, r)

	p, err := c.OpenPipe(context.Background(), "finite", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(p); err != nil || string(got) != "bye" {
		t.Fatalf("finite pipe: %q, %v", got, err)
	}
	p, err = c.OpenPipe(context.Background(), "failing", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(p)
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodeNotFound {
		t.Fatalf("got %v, want the server's not_found", err)
	}
}

func TestMux_ClientCloseCancelsThePipe(t *testing.T) {
	ended := make(chan struct{})
	r := protocol.NewRouter()
	r.MustRegister("forever", protocol.PipeMethod(func(context.Context, struct{}) (protocol.Empty, protocol.PipeFunc, error) {
		return protocol.Empty{}, func(ctx context.Context, p protocol.Pipe) error {
			defer close(ended)
			ctx, stop := protocol.UntilClosed(ctx, p)
			defer stop()
			<-ctx.Done()
			return nil
		}, nil
	}))
	c := muxPair(t, r)
	p, err := c.OpenPipe(context.Background(), "forever", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the pipe did not end the server side")
	}
}

func TestMux_AfterSendRunsAfterTheReply(t *testing.T) {
	var replied atomic.Bool
	after := make(chan bool, 1)
	r := protocol.NewRouter()
	r.MustRegister("stop", protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		resp := protocol.Reply(req, protocol.Empty{})
		resp.AfterSend = func() { after <- true }
		return resp
	}))
	c := muxPair(t, r)
	if err := c.Call(context.Background(), "stop", nil, nil); err != nil {
		t.Fatal(err)
	}
	replied.Store(true)
	select {
	case <-after:
	case <-time.After(time.Second):
		t.Fatal("AfterSend did not run")
	}
}

func TestMux_CallsFailWhenTheConnectionEnds(t *testing.T) {
	server, client := net.Pipe()
	go func() {
		// A server that welcomes and then hangs up.
		s := protocol.NewStream(server, protocol.DefaultMaxMessageSize)
		var hello protocol.Message
		_ = s.Receive(&hello)
		_ = s.Send(protocol.Message{Type: protocol.MessageTypeWelcome, Result: []byte(`{"protocol":"2"}`)})
		_ = server.Close()
	}()
	c, err := protocol.Handshake(context.Background(), client, protocol.Hello{Client: "t"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, protocol.ErrConnClosed) {
		t.Fatalf("got %v, want ErrConnClosed", err)
	}
	_ = c.Close()
}

func TestHandshake_DetectsProtocol1Servers(t *testing.T) {
	server, client := net.Pipe()
	go func() {
		// A pre-protocol-2 server: a hello has no method, so it answers
		// invalid_request and hangs up.
		s := protocol.NewStream(server, protocol.DefaultMaxMessageSize)
		var m protocol.Request
		_ = s.Receive(&m)
		_ = s.Send(protocol.Response{Type: protocol.MessageTypeResponse, Error: &protocol.Error{Code: protocol.ErrorCodeInvalidRequest, Message: "missing method"}})
		_ = server.Close()
	}()
	if _, err := protocol.Handshake(context.Background(), client, protocol.Hello{Client: "t"}, 0); !errors.Is(err, protocol.ErrProtocol1Only) {
		t.Fatalf("got %v, want ErrProtocol1Only", err)
	}
}

func TestHandshake_RespectsContext(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	go func() { _, _ = io.Copy(io.Discard, server) }() // never answers
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := protocol.Handshake(ctx, client, protocol.Hello{Client: "t"}, 0); err == nil {
		t.Fatal("expected a timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("handshake ignored its deadline")
	}
}

func TestMux_Protocol1StillWorks(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("echo", echo())
	s, done := serve(t, r)
	for _, id := range []string{"1", "2"} {
		if resp := roundTrip(t, s, id, "echo"); resp.Error != nil || resp.ID != id {
			t.Fatalf("protocol-1 request %s: %+v", id, resp)
		}
	}
	_ = s.Close()
	if err := waitServe(t, done); err != nil {
		t.Fatal(err)
	}
}

// TestMux_PipeClosedRightAwayStillOpens guards a race: the server may open
// and close a pipe before the caller of OpenPipe even runs. The caller must
// still get the pipe (with its data and end), not an error.
func TestMux_PipeClosedRightAwayStillOpens(t *testing.T) {
	r := protocol.NewRouter()
	r.MustRegister("instant", protocol.PipeMethod(func(context.Context, struct{}) (protocol.Empty, protocol.PipeFunc, error) {
		return protocol.Empty{}, func(_ context.Context, p protocol.Pipe) error {
			_, err := p.Write([]byte("x"))
			return err
		}, nil
	}))
	c := muxPair(t, r)
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			p, err := c.OpenPipe(context.Background(), "instant", nil, nil)
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			if got, err := io.ReadAll(p); err != nil || string(got) != "x" {
				t.Errorf("read %q, %v", got, err)
			}
		})
	}
	wg.Wait()
}
