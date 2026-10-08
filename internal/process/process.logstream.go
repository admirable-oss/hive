package process

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// logPollInterval is how often a following stream checks for new output.
const logPollInterval = 200 * time.Millisecond

// LogStream copies a process log to a writer, optionally following it. It
// is created by Service.OpenLogs, which has already validated the request,
// so a protocol handler can reply before streaming begins.
type LogStream struct {
	path string
	tail int
	// done is closed when the process has exited and its final state is
	// recorded; nil when the process was not running at open time.
	done <-chan struct{}
}

func (s *service) OpenLogs(ctx context.Context, req LogsRequest) (*LogStream, error) {
	req, p, err := s.resolveLogs(ctx, req)
	if err != nil {
		return nil, err
	}
	stdout, stderr := s.store.LogPaths(p)
	ls := &LogStream{path: stdout, tail: req.Tail}
	if req.Stream == StreamStderr {
		ls.path = stderr
	}
	if req.Follow {
		s.mu.Lock()
		if lp, ok := s.live[p.ID]; ok {
			ls.done = lp.done
		}
		s.mu.Unlock()
	}
	return ls, nil
}

// Following reports whether WriteTo will wait for new output.
func (ls *LogStream) Following() bool { return ls.done != nil }

// WriteTo writes the selected tail of the log to w and, when following,
// everything appended afterwards until the process exits or ctx ends.
func (ls *LogStream) WriteTo(ctx context.Context, w io.Writer) error {
	f, err := ls.openWhenReady(ctx)
	if f == nil || err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	start, err := tailOffset(f, info.Size(), ls.tail)
	if err != nil {
		return err
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	pos := start
	drain := func() error {
		n, err := io.Copy(w, f)
		pos += n
		return err
	}
	if err := drain(); err != nil || ls.done == nil {
		return err
	}

	ticker := time.NewTicker(logPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ls.done:
			return drain() // whatever the process wrote last
		case <-ticker.C:
		}
		if info, err := f.Stat(); err == nil && info.Size() < pos {
			// The log was truncated underneath us; start over from the top.
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			pos = 0
		}
		if err := drain(); err != nil {
			return err
		}
	}
}

// openWhenReady opens the log. A process that has not written anything yet
// may have no log file; a following stream waits for it, a plain one returns
// (nil, nil) for an empty log.
func (ls *LogStream) openWhenReady(ctx context.Context) (*os.File, error) {
	for {
		f, err := os.Open(ls.path)
		if !errors.Is(err, fs.ErrNotExist) {
			return f, err
		}
		if ls.done == nil {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-ls.done:
			return nil, nil
		case <-time.After(logPollInterval):
		}
	}
}
