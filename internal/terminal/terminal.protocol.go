package terminal

import (
	"compress/flate"
	"context"
	"errors"
	"io"
	"time"

	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/vt"
)

type viewParams struct {
	ProcessID string `json:"process_id"`
	// Width and Height are the client's size for this view.
	Width  uint16 `json:"width,omitempty"`
	Height uint16 `json:"height,omitempty"`
}

type viewResult struct {
	ViewID string `json:"view_id"`
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}

type inputParams struct {
	ProcessID string `json:"process_id"`
	Data      []byte `json:"data"` // base64 on the wire, so any byte survives
	ViewID    string `json:"view_id,omitempty"`
}

type resizeParams struct {
	ProcessID string `json:"process_id"`
	Width     uint16 `json:"width"`
	Height    uint16 `json:"height"`
	// ViewID makes this a view's resize (applied when that view is in
	// control). Without it the terminal is resized directly.
	ViewID string `json:"view_id,omitempty"`
}

type snapshotParams struct {
	ProcessID string `json:"process_id"`
	// ANSI returns lines with SGR styling instead of plain text.
	ANSI bool `json:"ansi,omitempty"`
	// Scrollback also returns up to this many lines that scrolled off the
	// top of the screen.
	Scrollback int `json:"scrollback,omitempty"`
}

// MaxScrollbackLines bounds one snapshot's scrollback.
const MaxScrollbackLines = 10_000

// SnapshotResult is a screen as text.
type SnapshotResult struct {
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Lines     []string  `json:"lines"`
	Cursor    vt.Cursor `json:"cursor"`
	Title     string    `json:"title,omitempty"`
	AltScreen bool      `json:"alt_screen,omitempty"`
	// Scrollback holds the requested history, oldest first; it precedes
	// Lines.
	Scrollback []string `json:"scrollback,omitempty"`
}

// Register exposes svc on the wire as terminal.*. Sessions are created by the
// process service; the wire views them, types into them and resizes them.
func Register(r *protocol.Router, svc Service) {
	// terminal.attach: a pipe carrying the screen painted as ANSI for a real
	// terminal, and keystrokes back. It takes over the terminal size.
	r.MustRegister("terminal.attach", protocol.PipeMethod(func(_ context.Context, p viewParams) (viewResult, protocol.PipeFunc, error) {
		return openView(svc, p, true, func(_ context.Context, w io.Writer) func(*vt.Frame) error {
			return vt.NewPainter(w).Paint
		})
	}))
	// terminal.frames: a pipe carrying the screen as binary frames (see
	// vt.AppendFrame), and keystrokes back. For clients that draw the screen
	// themselves; it only takes over the size once the client types.
	//
	// A client on a slow link (see protocol.Peer.Report) is sent frames no
	// more often than its round trip and within its byte budget; changes
	// meanwhile merge into the next frame. One that offered frames/deflate
	// gets the stream compressed.
	r.MustRegister("terminal.frames", protocol.PipeMethod(func(ctx context.Context, p viewParams) (viewResult, protocol.PipeFunc, error) {
		opts := vt.FrameOptions{
			Links:     protocol.PeerHas(ctx, vt.FrameLinksCapability),
			Clipboard: protocol.PeerHas(ctx, vt.FrameClipboardCapability),
		}
		deflate := protocol.PeerHas(ctx, vt.FrameDeflateCapability)
		peer := protocol.PeerOf(ctx)
		return openView(svc, p, false, func(ctx context.Context, w io.Writer) func(*vt.Frame) error {
			out := newFrameWriter(w, deflate)
			var buf []byte
			return func(f *vt.Frame) error {
				buf = vt.AppendFrameOpts(buf[:0], f, opts)
				n, err := out.write(buf)
				if err != nil {
					return err
				}
				return pace(ctx, peer, n)
			}
		})
	}))
	// terminal.graphics is an ordered side stream of complete Kitty APC
	// commands. It is kept separate from cell frames so frame coalescing can
	// never discard upload chunks or reorder image operations.
	r.MustRegister("terminal.graphics", protocol.PipeMethod(func(ctx context.Context, p viewParams) (protocol.Empty, protocol.PipeFunc, error) {
		sess, err := lookup(svc, p.ProcessID)
		if err != nil {
			return protocol.Empty{}, nil, err
		}
		graphics, ok := sess.(GraphicsSession)
		if !ok {
			return protocol.Empty{}, nil, protocol.NewError(protocol.ErrorCodeUnsupported, errors.New("graphics stream unavailable"))
		}
		return protocol.Empty{}, func(ctx context.Context, pipe protocol.Pipe) error {
			ctx, stop := protocol.UntilClosed(ctx, pipe)
			defer stop()
			var buf []byte
			return graphics.Graphics(ctx, func(event vt.GraphicsEvent) error {
				buf = vt.AppendGraphicsEvent(buf[:0], event)
				_, err := pipe.Write(buf)
				return err
			})
		}, nil
	}))
	r.MustRegister("terminal.input", protocol.Method(func(_ context.Context, p inputParams) (protocol.Empty, error) {
		sess, err := lookup(svc, p.ProcessID)
		if err != nil {
			return protocol.Empty{}, err
		}
		if p.ViewID != "" {
			if err := svc.Interact(p.ProcessID, p.ViewID); err != nil {
				return protocol.Empty{}, wireError(err)
			}
		}
		_, err = sess.Write(p.Data)
		return protocol.Empty{}, err
	}))
	r.MustRegister("terminal.resize", protocol.Method(func(_ context.Context, p resizeParams) (protocol.Empty, error) {
		size := Size{Width: p.Width, Height: p.Height}
		if !size.Valid() {
			return protocol.Empty{}, protocol.NewError(protocol.ErrorCodeInvalidParams, errors.New("width and height must be positive"))
		}
		if p.ViewID != "" {
			return protocol.Empty{}, wireError(svc.ResizeView(p.ProcessID, p.ViewID, size))
		}
		sess, err := lookup(svc, p.ProcessID)
		if err == nil {
			err = sess.Resize(size)
		}
		return protocol.Empty{}, err
	}))
	r.MustRegister("terminal.snapshot", protocol.Method(func(ctx context.Context, p snapshotParams) (SnapshotResult, error) {
		sess, err := lookup(svc, p.ProcessID)
		if err != nil {
			return SnapshotResult{}, err
		}
		s, err := sess.Snapshot(ctx)
		if err != nil {
			return SnapshotResult{}, wireError(err)
		}
		res := SnapshotResult{Width: s.Cols, Height: s.Rows, Cursor: s.Cursor, Title: s.Title, AltScreen: s.Modes&vt.ModeAltScreen != 0}
		if p.Scrollback > 0 {
			res.Scrollback, err = sess.Read(ctx, ReadRequest{Source: SourceHistory, Lines: min(p.Scrollback, MaxScrollbackLines), ANSI: p.ANSI})
			if err != nil {
				return SnapshotResult{}, wireError(err)
			}
		}
		for y := range s.Rows {
			if p.ANSI {
				res.Lines = append(res.Lines, vt.LineANSI(s.Lines[y]))
			} else {
				res.Lines = append(res.Lines, s.LineText(y))
			}
		}
		return res, nil
	}))
}

// openView joins a view and returns the pipe that serves it: frames out
// through the encoder made by newEmit, keystrokes in.
func openView(svc Service, p viewParams, interactive bool, newEmit func(context.Context, io.Writer) func(*vt.Frame) error) (viewResult, protocol.PipeFunc, error) {
	sess, err := lookup(svc, p.ProcessID)
	if err != nil {
		return viewResult{}, nil, err
	}
	size := Size{Width: p.Width, Height: p.Height}
	if !size.Valid() {
		size = sess.Size()
	}
	viewID, err := svc.Join(p.ProcessID, size, interactive)
	if err != nil {
		return viewResult{}, nil, wireError(err)
	}
	cur := sess.Size()
	res := viewResult{ViewID: viewID, Width: cur.Width, Height: cur.Height}
	return res, func(ctx context.Context, pipe protocol.Pipe) error {
		defer svc.Leave(p.ProcessID, viewID)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		inputDone := make(chan struct{})
		go func() {
			defer close(inputDone)
			defer cancel() // the client closed its end: stop sending frames
			buf := make([]byte, 4096)
			for {
				n, err := pipe.Read(buf)
				if n > 0 {
					_ = svc.Interact(p.ProcessID, viewID)
					if _, werr := sess.Write(buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		err := sess.Frames(ctx, newEmit(ctx, pipe))
		cancel()
		if c, ok := pipe.(io.Closer); ok {
			_ = c.Close() // ends the input reader
		}
		<-inputDone
		return err
	}, nil
}

func lookup(svc Service, processID string) (Session, error) {
	sess, err := svc.Get(processID)
	return sess, wireError(err)
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrViewNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	}
	return err
}

// MaxFrameWait bounds how long a client on a slow link waits between
// frames because of its round trip.
const MaxFrameWait = 250 * time.Millisecond

// frameWriter writes frames to a client, deflated when it asked, and says
// how many bytes each took on the wire.
type frameWriter struct {
	w   *countingWriter
	zip *flate.Writer
}

func newFrameWriter(w io.Writer, deflate bool) *frameWriter {
	fw := &frameWriter{w: &countingWriter{w: w}}
	if deflate {
		fw.zip, _ = flate.NewWriter(fw.w, flate.BestSpeed) // the level is valid
	}
	return fw
}

func (fw *frameWriter) write(frame []byte) (int, error) {
	before := fw.w.n
	if fw.zip == nil {
		_, err := fw.w.Write(frame)
		return fw.w.n - before, err
	}
	if _, err := fw.zip.Write(frame); err != nil {
		return 0, err
	}
	// A sync flush sends the whole frame now; the compressor keeps its
	// history, so later frames compress against earlier ones.
	err := fw.zip.Flush()
	return fw.w.n - before, err
}

type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// pace holds the next frame back for a client on a slow link, after one
// of n bytes was sent: for its round trip (between FrameInterval and
// MaxFrameWait), and until its budget allows more. Changes meanwhile merge
// into the next frame. A local client (no report) is not held back.
func pace(ctx context.Context, peer *protocol.Peer, n int) error {
	wait := peer.Spend(n)
	if rtt, _ := peer.Link(); rtt > 0 {
		wait = max(wait, min(max(rtt, FrameInterval), MaxFrameWait))
	}
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
