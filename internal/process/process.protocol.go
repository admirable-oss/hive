package process

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/admirable-oss/hive/internal/environment"
	"github.com/admirable-oss/hive/internal/protocol"
)

type idParams struct {
	ID string `json:"id"`
}

type listParams struct {
	EnvironmentID string `json:"environment_id"` // empty lists every environment
}

type logsResult struct {
	Logs string `json:"logs"`
	// Truncated is set when the tail was cut to MaxLogsBytes; use
	// process.logs.stream to read logs of any size.
	Truncated bool `json:"truncated,omitempty"`
}

// DefaultTail is how many lines process.logs returns when none are requested.
const DefaultTail = 50

// MaxLogsBytes caps a process.logs reply well under the protocol's frame
// limit, so a huge tail degrades to a truncated answer instead of an error.
const MaxLogsBytes = 512 << 10

// Register exposes svc on the wire as process.*.
func Register(r *protocol.Router, svc Service) {
	r.MustRegister("process.start", protocol.Method(func(ctx context.Context, req StartRequest) (Process, error) {
		p, err := svc.Start(ctx, req)
		return p, wireError(err)
	}))
	r.MustRegister("process.get", protocol.Method(func(ctx context.Context, p idParams) (Process, error) {
		proc, err := svc.Get(ctx, p.ID)
		return proc, wireError(err)
	}))
	r.MustRegister("process.list", protocol.Method(func(ctx context.Context, p listParams) ([]Process, error) {
		procs, err := svc.List(ctx, p.EnvironmentID)
		return procs, wireError(err)
	}))
	r.MustRegister("process.stop", protocol.Method(func(ctx context.Context, p idParams) (protocol.Empty, error) {
		return protocol.Empty{}, wireError(svc.Stop(ctx, p.ID))
	}))
	r.MustRegister("process.logs", protocol.Method(func(ctx context.Context, req LogsRequest) (logsResult, error) {
		logs, err := svc.Logs(ctx, req)
		if err != nil {
			return logsResult{}, wireError(err)
		}
		res := logsResult{Logs: logs}
		if len(logs) > MaxLogsBytes {
			cut := logs[len(logs)-MaxLogsBytes:]
			if i := strings.IndexByte(cut, '\n'); i >= 0 {
				cut = cut[i+1:] // start on a whole line
			}
			res.Logs, res.Truncated = cut, true
		}
		return res, nil
	}))
	// process.logs.stream replies once the request is validated, then turns
	// the connection into a raw byte stream of the log. The server closes it
	// when the log is fully sent (or, when following, when the process ends);
	// the client closing its side stops a follow early.
	r.MustRegister("process.logs.stream", protocol.HandlerFunc(func(ctx context.Context, req protocol.Request) protocol.Response {
		var params LogsRequest
		if err := protocol.DecodeParams(req, &params); err != nil {
			return protocol.Fail(req, err)
		}
		ls, err := svc.OpenLogs(ctx, params)
		if err != nil {
			return protocol.Fail(req, wireError(err))
		}
		resp := protocol.Reply(req, map[string]bool{"following": ls.Following()})
		resp.Hijack = func(ctx context.Context, conn net.Conn) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			hungUp := make(chan struct{})
			go func() {
				defer close(hungUp)
				// Nothing is expected from the client; a read returning
				// means it hung up.
				_, _ = io.Copy(io.Discard, conn)
				cancel()
			}()
			_ = ls.WriteTo(ctx, conn)
			_ = conn.Close()
			<-hungUp
		}
		return resp
	}))
}

// wireError gives domain errors their protocol codes.
func wireError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, environment.ErrNotFound):
		return protocol.NewError(protocol.ErrorCodeNotFound, err)
	case errors.Is(err, ErrCommandRequired), errors.Is(err, environment.ErrInvalidID),
		errors.Is(err, ErrInvalidStream), errors.Is(err, ErrNoStderr):
		return protocol.NewError(protocol.ErrorCodeInvalidParams, err)
	}
	return err
}
