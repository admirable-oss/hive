package client_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/client"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// imageSession records input; nothing else of terminal.Session is used.
type imageSession struct {
	terminal.Session
	mu  sync.Mutex
	in  bytes.Buffer
	cmd int // complete writes
}

func (s *imageSession) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmd++
	return s.in.Write(b)
}

func (s *imageSession) Snapshot(context.Context) (*vt.Screen, error) {
	sc := vt.NewScreen(1, 1)
	sc.Modes |= vt.ModeBracketedPaste
	return sc, nil
}

func (s *imageSession) typed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.in.String()
}

type imageTerms struct {
	terminal.Service
	sess *imageSession
}

func (t imageTerms) Get(id string) (terminal.Session, error) {
	if id != "proc-1" {
		return nil, terminal.ErrSessionNotFound
	}
	return t.sess, nil
}

func (t imageTerms) Interact(string, string) error { return nil }

// cutConn breaks after the client has written limit bytes, like an SSH
// link dropping mid-transfer.
type cutConn struct {
	net.Conn
	left atomic.Int64
}

func (c *cutConn) Write(p []byte) (int, error) {
	if c.left.Add(-int64(len(p))) < 0 {
		_ = c.Close()
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func imageServer(t *testing.T, input terminal.ImageInput, cut int64) (client.Client, *imageSession, *atomic.Int32) {
	t.Helper()
	sess := &imageSession{}
	r := protocol.NewRouter()
	terminal.RegisterImages(r, imageTerms{sess: sess}, terminal.ImageConfig{
		Dir:   t.TempDir() + "/images",
		Input: func(string) terminal.ImageInput { return input },
	})
	var dials atomic.Int32
	var wg sync.WaitGroup
	c := client.NewService(client.Config{Name: "test", Dial: func(context.Context) (net.Conn, error) {
		server, conn := net.Pipe()
		wg.Go(func() { _ = protocol.ServeConn(context.Background(), server, r, protocol.ServerInfo{Version: "test"}) })
		if dials.Add(1) == 1 && cut > 0 {
			cc := &cutConn{Conn: conn}
			cc.left.Store(cut)
			return cc, nil
		}
		return conn, nil
	}})
	t.Cleanup(func() {
		_ = c.Close()
		wg.Wait()
	})
	return c, sess, &dials
}

func testPNG(t testing.TB, w, h int, pad int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return append(b.Bytes(), make([]byte, pad)...)
}

// TestTerminalImage_RetriesAfterTheLinkDrops: the connection breaks
// half-way through the upload; the client reconnects, sends the image
// again under the same transfer ID, and the program receives it once.
func TestTerminalImage_RetriesAfterTheLinkDrops(t *testing.T) {
	data := testPNG(t, 64, 64, 300<<10)
	c, sess, dials := imageServer(t, terminal.ImageInputPath, 150<<10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.TerminalImage(ctx, client.ImageRequest{ProcessID: "proc-1", PNG: data})
	if err != nil {
		t.Fatal(err)
	}
	if dials.Load() < 2 {
		t.Fatalf("dialled %d times; the cut should have forced a reconnect", dials.Load())
	}
	want := "\x1b[200~" + res.Path + "\x1b[201~"
	if res.Delivery != "path" || sess.typed() != want {
		t.Fatalf("result %+v, typed %q", res, sess.typed())
	}
}

func TestTerminalImage_RefusalIsNotRetried(t *testing.T) {
	c, sess, dials := imageServer(t, terminal.ImageInputNone, 0)
	_, err := c.TerminalImage(context.Background(), client.ImageRequest{ProcessID: "proc-1", PNG: testPNG(t, 2, 2, 0)})
	if pe, ok := errors.AsType[*protocol.Error](err); !ok || pe.Code != protocol.ErrorCodeUnsupported {
		t.Fatalf("got %v, want unsupported", err)
	}
	if dials.Load() != 1 || sess.typed() != "" {
		t.Fatalf("dials %d, typed %q", dials.Load(), sess.typed())
	}
}

// TestTerminalImage_KittyCommandsAreWrittenWhole: each Kitty command is
// one write, so another view's keystrokes can only fall between commands.
func TestTerminalImage_KittyCommandsAreWrittenWhole(t *testing.T) {
	data := testPNG(t, 32, 32, 40<<10)
	c, sess, _ := imageServer(t, terminal.ImageInputKitty, 0)
	if _, err := c.TerminalImage(context.Background(), client.ImageRequest{ProcessID: "proc-1", PNG: data}); err != nil {
		t.Fatal(err)
	}
	got := sess.typed()
	if n := strings.Count(got, "\x1b_G"); n != sess.cmd || n < 2 {
		t.Fatalf("%d commands in %d writes", n, sess.cmd)
	}
}
