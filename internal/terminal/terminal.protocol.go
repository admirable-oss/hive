package terminal

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/admirable-oss/hive/internal/protocol"
)

type sessionParams struct {
	ProcessID string `json:"process_id"`
}

type inputParams struct {
	ProcessID string `json:"process_id"`
	Data      []byte `json:"data"` // base64 on the wire, so any byte survives
}

type resizeParams struct {
	ProcessID string `json:"process_id"`
	Width     uint16 `json:"width"`
	Height    uint16 `json:"height"`
}

// Register exposes svc on the wire as terminal.*. Sessions are created by the
// process service; the wire only attaches to, types into and resizes them.
func Register(r *protocol.Router, svc Service) {
	r.MustRegister("terminal.attach", protocol.HandlerFunc(func(_ context.Context, req protocol.Request) protocol.Response {
		var p sessionParams
		if err := protocol.DecodeParams(req, &p); err != nil {
			return protocol.Fail(req, err)
		}
		sess, err := lookup(svc, p.ProcessID)
		if err != nil {
			return protocol.Fail(req, err)
		}
		resp := protocol.Reply(req, map[string]string{"status": "attached"})
		resp.Hijack = stream(sess)
		return resp
	}))
	r.MustRegister("terminal.input", protocol.Method(func(_ context.Context, p inputParams) (protocol.Empty, error) {
		sess, err := lookup(svc, p.ProcessID)
		if err == nil {
			_, err = sess.Write(p.Data)
		}
		return protocol.Empty{}, err
	}))
	r.MustRegister("terminal.resize", protocol.Method(func(_ context.Context, p resizeParams) (protocol.Empty, error) {
		sess, err := lookup(svc, p.ProcessID)
		if err == nil {
			err = sess.Resize(Size{Width: p.Width, Height: p.Height})
		}
		return protocol.Empty{}, err
	}))
}

func lookup(svc Service, processID string) (Session, error) {
	sess, err := svc.Get(processID)
	if errors.Is(err, ErrSessionNotFound) {
		return nil, protocol.NewError(protocol.ErrorCodeNotFound, err)
	}
	return sess, err
}

// stream turns an attached connection into a raw terminal: history first, then
// live output to the client and client bytes to the PTY, until either side ends.
func stream(sess Session) protocol.HijackFunc {
	return func(ctx context.Context, conn net.Conn) {
		out, history, detach := sess.Subscribe()
		defer detach()

		if _, err := conn.Write(history); err != nil {
			return
		}

		done := make(chan struct{}, 2)
		go func() {
			for chunk := range out {
				if _, err := conn.Write(chunk); err != nil {
					break
				}
			}
			done <- struct{}{}
		}()
		go func() {
			_, _ = io.Copy(sess, conn)
			done <- struct{}{}
		}()

		select {
		case <-done:
		case <-ctx.Done():
		}
		_ = conn.Close() // unblocks whichever copy is still running
	}
}
