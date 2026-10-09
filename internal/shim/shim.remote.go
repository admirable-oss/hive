package shim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admirable-oss/hive/internal/jsonfile"
	"github.com/admirable-oss/hive/internal/protocol"
	"github.com/admirable-oss/hive/internal/terminal"
	"github.com/admirable-oss/hive/internal/vt"
)

// callTimeout bounds a request to a shim. Shims answer from memory, so a
// slow answer means the shim is stuck.
const callTimeout = 10 * time.Second

// Remote is the daemon's handle on one shim and its agent. It implements
// terminal.Session (for terminal agents) and the process supervisor's
// handle (PID, Wait, Kill via Close), plus Release and Detach.
type Remote struct {
	id, dir  string
	mux      *protocol.MuxClient
	pid      int
	terminal bool

	mu   sync.Mutex
	size terminal.Size

	waitOnce sync.Once
	waitErr  error
	detached atomic.Bool
	released atomic.Bool
}

var _ terminal.Session = (*Remote)(nil)

func (r *Remote) call(method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return r.mux.Call(ctx, method, params, result)
}

// ID returns the agent's ID.
func (r *Remote) ID() string { return r.id }

// Write sends input to the agent's terminal.
func (r *Remote) Write(b []byte) (int, error) {
	if !r.terminal {
		return 0, ErrNotTerminal
	}
	if err := r.call("shim.input", dataParams{Data: b}, nil); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Resize changes the agent's terminal size.
func (r *Remote) Resize(size terminal.Size) error {
	if !r.terminal || !size.Valid() {
		return nil
	}
	if err := r.call("shim.resize", size, nil); err != nil {
		return err
	}
	r.mu.Lock()
	r.size = size
	r.mu.Unlock()
	return nil
}

// Size returns the terminal size last set or reported.
func (r *Remote) Size() terminal.Size {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Pid returns the agent's PID.
func (r *Remote) Pid() int { return r.pid }

// PID is Pid, for the process supervisor's Handle contract.
func (r *Remote) PID() int { return r.pid }

// Close stops the agent (its whole process group). The shim reports the
// exit through Wait.
func (r *Remote) Close() error { return r.call("shim.stop", stopParams{}, nil) }

// Kill is Close, for the process supervisor's Handle contract.
func (r *Remote) Kill() error { return r.Close() }

// Snapshot returns the agent's current screen.
func (r *Remote) Snapshot(ctx context.Context) (*vt.Screen, error) {
	if !r.terminal {
		return nil, ErrNotTerminal
	}
	var res snapshotResult
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := r.mux.Call(ctx, "shim.snapshot", nil, &res); err != nil {
		return nil, err
	}
	f, err := vt.DecodeFrame(res.Frame)
	if err != nil {
		return nil, err
	}
	s := &vt.Screen{}
	s.Apply(f)
	return s, nil
}

// Read returns part of the agent's terminal text.
func (r *Remote) Read(ctx context.Context, req terminal.ReadRequest) ([]string, error) {
	if !r.terminal {
		return nil, ErrNotTerminal
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var lines []string
	err := r.mux.Call(ctx, "shim.read", req, &lines)
	return lines, err
}

// WaitOutput waits in the shim for a line matching req.Pattern; ctx's
// deadline travels with the request.
func (r *Remote) WaitOutput(ctx context.Context, req terminal.WaitRequest) (string, error) {
	if !r.terminal {
		return "", ErrNotTerminal
	}
	p := waitParams{WaitRequest: req}
	if deadline, ok := ctx.Deadline(); ok {
		p.TimeoutMS = max(time.Until(deadline).Milliseconds(), 1)
	}
	var line string
	err := r.mux.Call(ctx, "shim.wait_output", p, &line)
	if pe, ok := errors.AsType[*protocol.Error](err); ok {
		switch pe.Code {
		case protocol.ErrorCodeTimeout:
			return "", fmt.Errorf("%w: %s", context.DeadlineExceeded, pe.Message)
		case protocol.ErrorCodeUnavailable:
			return "", terminal.ErrEnded
		case protocol.ErrorCodeInvalidParams:
			return "", fmt.Errorf("%w: %s", terminal.ErrInvalidRead, pe.Message)
		}
	}
	return line, err
}

// Frames streams the agent's screen from the shim. Backpressure is end to
// end: while emit is busy this side stops reading, the shim's writes block,
// and it computes the next frame only once the previous one was taken.
func (r *Remote) Frames(ctx context.Context, emit func(*vt.Frame) error) error {
	if !r.terminal {
		return ErrNotTerminal
	}
	p, err := r.mux.OpenPipe(ctx, "shim.frames", nil, nil)
	if err != nil {
		return err
	}
	defer p.Close()
	stop := context.AfterFunc(ctx, func() { _ = p.Close() })
	defer stop()
	fr := vt.NewFrameReader(p)
	for {
		f, err := fr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := emit(f); err != nil {
			return err
		}
	}
}

// Wait blocks until the agent exits. It returns nil for exit status 0 and an
// *Exit otherwise. If the shim disappears without reporting an exit, it
// returns ErrShimGone; after Detach it returns ErrDetached.
func (r *Remote) Wait() error {
	r.waitOnce.Do(func() {
		var st State
		err := r.mux.Call(context.Background(), "shim.wait", nil, &st)
		switch {
		case r.detached.Load():
			r.waitErr = ErrDetached
		case err == nil:
			r.waitErr = exitError(st)
		default:
			// The connection broke. The shim may have recorded the exit
			// before going away.
			if disk, rerr := readState(r.dir); rerr == nil && disk.Status == StatusExited {
				r.waitErr = exitError(disk)
				return
			}
			r.waitErr = fmt.Errorf("%w: %w", ErrShimGone, err)
		}
	})
	return r.waitErr
}

func exitError(st State) error {
	if st.ExitCode == nil || *st.ExitCode == 0 {
		return nil
	}
	return &Exit{Code: *st.ExitCode}
}

// Release tells the shim its agent's result is recorded, so it can exit,
// and removes the shim's directory. Call it after Wait returned.
func (r *Remote) Release() {
	if !r.released.CompareAndSwap(false, true) {
		return
	}
	_ = r.call("shim.release", nil, nil)
	_ = r.mux.Close()
	_ = os.RemoveAll(r.dir)
}

// Detach lets go of the shim without stopping its agent: the daemon is
// shutting down and a later daemon will adopt the shim. Wait returns
// ErrDetached.
func (r *Remote) Detach() {
	r.detached.Store(true)
	_ = r.mux.Close()
}

func readState(dir string) (State, error) {
	var st State
	err := jsonfile.Read(statePath(dir), &st)
	return st, err
}
