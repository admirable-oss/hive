package terminal

import (
	"context"
	"errors"
	"io"

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
		return openView(svc, p, true, func(w io.Writer) func(*vt.Frame) error {
			return vt.NewPainter(w).Paint
		})
	}))
	// terminal.frames: a pipe carrying the screen as binary frames (see
	// vt.AppendFrame), and keystrokes back. For clients that draw the screen
	// themselves; it only takes over the size once the client types.
	r.MustRegister("terminal.frames", protocol.PipeMethod(func(_ context.Context, p viewParams) (viewResult, protocol.PipeFunc, error) {
		return openView(svc, p, false, func(w io.Writer) func(*vt.Frame) error {
			var buf []byte
			return func(f *vt.Frame) error {
				buf = vt.AppendFrame(buf[:0], f)
				_, err := w.Write(buf)
				return err
			}
		})
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
func openView(svc Service, p viewParams, interactive bool, newEmit func(io.Writer) func(*vt.Frame) error) (viewResult, protocol.PipeFunc, error) {
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
		err := sess.Frames(ctx, newEmit(pipe))
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
