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

// lagNotice is written to a client that was cut off for falling behind, so
// the user knows why the session ended and that the agent is still running.
const lagNotice = "\r\n[hive] detached: this terminal could not keep up with the agent's output. The agent is still running; attach again to continue.\r\n"

// stream turns an attached connection into a raw terminal: history first, then
// live output to the client and client bytes to the PTY, until either side ends.
func stream(sess Session) protocol.HijackFunc {
	return func(ctx context.Context, conn net.Conn) {
		sub := sess.Subscribe()
		defer sub.Close()

		if _, err := conn.Write(sub.History); err != nil {
			return
		}

		done := make(chan struct{}, 2)
		go func() {
			defer func() { done <- struct{}{} }()
			for chunk := range sub.C {
				if _, err := conn.Write(chunk); err != nil {
					return
				}
			}
			if sub.Lagged() {
				_, _ = conn.Write([]byte(lagNotice))
			}
		}()
		go func() {
			_, _ = io.Copy(sess, conn)
			done <- struct{}{}
		}()

		pending := 2
		select {
		case <-done:
			pending--
		case <-ctx.Done():
		}
		_ = conn.Close() // unblocks the input copy
		sub.Close()      // ends the output loop
		for ; pending > 0; pending-- {
			<-done
		}
	}
}
