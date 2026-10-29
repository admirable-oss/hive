package observer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/protocol"
)

// ClientConfig says how to reach the observer socket and as whom.
type ClientConfig struct {
	// Socket is the observer socket's path; Dial, when set, connects
	// instead (through `ssh <host> hive proxy --observer`, a test pipe).
	Socket string
	Dial   func(ctx context.Context) (net.Conn, error)
	// Token is the observer credential's token.
	Token string
	// Name identifies the program in the daemon's audit log.
	Name string
}

// Client is one authenticated observer connection. It does not reconnect;
// Follow does.
type Client struct {
	mux  *protocol.MuxClient
	auth AuthResult
}

// Connect dials the observer socket and authenticates.
func Connect(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.Token == "" {
		return nil, errors.New("observer: a token is required")
	}
	var (
		conn net.Conn
		err  error
	)
	if cfg.Dial != nil {
		conn, err = cfg.Dial(ctx)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, "unix", cfg.Socket)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to the observer socket: %w", err)
	}
	name := cfg.Name
	if name == "" {
		name = "observer"
	}
	mux, err := protocol.Handshake(ctx, conn, protocol.Hello{Client: name, ClientVersion: buildinfo.Get().Version}, protocol.DefaultMaxMessageSize)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("connect to the observer socket: %w", err)
	}
	c := &Client{mux: mux}
	if err := mux.Call(ctx, methodAuth, AuthRequest{Token: cfg.Token}, &c.auth); err != nil {
		_ = mux.Close()
		return nil, err
	}
	return c, nil
}

// Credential is what this connection acts as.
func (c *Client) Credential() Credential { return c.auth.Credential }

// Position is the event stream's position when the connection
// authenticated.
func (c *Client) Position() (epoch string, seq uint64) { return c.auth.Epoch, c.auth.Seq }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.mux.Done() }

// Close ends the connection.
func (c *Client) Close() error { return c.mux.Close() }

// Call performs any observer method.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.mux.Call(ctx, method, params, result)
}

// Status returns the daemon's health and what this observer sees of it.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	err := c.Call(ctx, methodStatus, nil, &st)
	return st, err
}

// Snapshot returns everything in scope.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	err := c.Call(ctx, methodSnapshot, nil, &s)
	return s, err
}

// Pane returns one pane with its agent and metadata.
func (c *Client) Pane(ctx context.Context, id string) (PaneResult, error) {
	var p PaneResult
	err := c.Call(ctx, methodPane, PaneRequest{Pane: id}, &p)
	return p, err
}

// Screen returns a pane's screen.
func (c *Client) Screen(ctx context.Context, req ScreenRequest) (Screen, error) {
	var s Screen
	err := c.Call(ctx, methodScreen, req, &s)
	return s, err
}

// Output returns a pane's recent output.
func (c *Client) Output(ctx context.Context, req OutputRequest) (OutputResult, error) {
	var o OutputResult
	err := c.Call(ctx, methodOutput, req, &o)
	return o, err
}

// History returns a page of events.
func (c *Client) History(ctx context.Context, req HistoryRequest) (Page, error) {
	var p Page
	err := c.Call(ctx, methodHistory, req, &p)
	return p, err
}

// Insights returns the time and usage of what the credential sees.
func (c *Client) Insights(ctx context.Context, req InsightsRequest) (Insights, error) {
	var in Insights
	err := c.Call(ctx, methodInsights, req, &in)
	return in, err
}

// Plugins lists the installed plugins: their tier, signer, sandbox and
// revocations (credentials that see the whole runtime).
func (c *Client) Plugins(ctx context.Context) ([]Plugin, error) {
	var out []Plugin
	err := c.Call(ctx, methodPlugins, nil, &out)
	return out, err
}

// Schema describes the observer API.
func (c *Client) Schema(ctx context.Context) (protocol.Schema, error) {
	var s protocol.Schema
	err := c.Call(ctx, methodSchema, nil, &s)
	return s, err
}

// Events opens an event stream.
func (c *Client) Events(ctx context.Context, req EventsRequest) (*EventStream, EventsResult, error) {
	var res EventsResult
	p, err := c.mux.OpenPipe(ctx, methodEvents, req, &res)
	if err != nil {
		return nil, res, err
	}
	return &EventStream{pipe: p, sc: lineScanner(p)}, res, nil
}

// Watch opens a pane's screen stream.
func (c *Client) Watch(ctx context.Context, req WatchRequest) (*ScreenStream, WatchResult, error) {
	var res WatchResult
	p, err := c.mux.OpenPipe(ctx, methodWatch, req, &res)
	if err != nil {
		return nil, res, err
	}
	return &ScreenStream{pipe: p, sc: lineScanner(p)}, res, nil
}

func lineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	return sc
}

// EventStream delivers events, one at a time.
type EventStream struct {
	pipe io.Closer
	sc   *bufio.Scanner
}

// Next returns the next event, or io.EOF when the stream ended.
func (s *EventStream) Next() (Event, error) {
	var ev Event
	err := nextLine(s.sc, &ev)
	return ev, err
}

// Close ends the stream.
func (s *EventStream) Close() error { return closePipe(s.pipe) }

// ScreenStream delivers a watched screen's updates.
type ScreenStream struct {
	pipe io.Closer
	sc   *bufio.Scanner
}

// Next returns the next update, or io.EOF when the stream ended.
func (s *ScreenStream) Next() (ScreenUpdate, error) {
	var u ScreenUpdate
	err := nextLine(s.sc, &u)
	return u, err
}

// Close ends the stream.
func (s *ScreenStream) Close() error { return closePipe(s.pipe) }

func nextLine(sc *bufio.Scanner, v any) error {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(sc.Bytes(), v)
}

func closePipe(c io.Closer) error {
	if err := c.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}

// Apply returns lines (a screen's text, one string per row) with u
// applied.
func (u ScreenUpdate) Apply(lines []string) []string {
	if u.Keyframe || len(lines) != u.Height {
		next := make([]string, u.Height)
		if !u.Keyframe {
			copy(next, lines)
		}
		lines = next
	}
	for _, l := range u.Lines {
		if l.Y >= 0 && l.Y < len(lines) {
			lines[l.Y] = l.Text
		}
	}
	return lines
}

// Update is what Follow passes on: a new snapshot (at the start, and after
// every resynchronisation), or one event to apply to the last one.
type Update struct {
	Snapshot *Snapshot
	Event    *Event
}

// Follow reconnects. Backoff between attempts grows to maxBackoff.
const (
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second
)

// Follow keeps an observer's view current until ctx ends or the credential
// stops working. It connects, passes a snapshot to fn, then every event
// after it. When the connection drops it reconnects with growing backoff
// and resumes from the last event it passed on, replayed from the daemon's
// history; when that is not possible (the observer fell behind, history
// moved on, the daemon restarted) it passes a fresh snapshot instead. An
// error from fn stops it.
func Follow(ctx context.Context, cfg ClientConfig, fn func(Update) error) error {
	var (
		epoch   string
		since   *uint64
		backoff = minBackoff
	)
	for {
		err := follow(ctx, cfg, &epoch, &since, fn, func() { backoff = minBackoff })
		if ctx.Err() != nil {
			return nil //nolint:nilerr // following ends with its context
		}
		var pe *protocol.Error
		if errors.As(err, &pe) && pe.Code == protocol.ErrorCodePermissionDenied {
			return err // revoked, expired or invalid: retrying will not help
		}
		var stop stopError
		if errors.As(err, &stop) {
			return stop.err
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// stopError carries an error from Follow's fn, which ends it.
type stopError struct{ err error }

func (e stopError) Error() string { return e.err.Error() }
func (e stopError) Unwrap() error { return e.err }

func follow(ctx context.Context, cfg ClientConfig, epoch *string, since **uint64, fn func(Update) error, connected func()) error {
	c, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	connected()
	resync := func() error {
		snap, err := c.Snapshot(ctx)
		if err != nil {
			return err
		}
		*epoch, *since = snap.Epoch, &snap.Seq
		if err := fn(Update{Snapshot: &snap}); err != nil {
			return stopError{err}
		}
		return nil
	}
	req := EventsRequest{Epoch: *epoch, Since: *since}
	if *since == nil {
		// First connection: subscribe first, then snapshot, so nothing
		// falls between them (events the snapshot already shows are
		// skipped by position).
		req = EventsRequest{}
	}
	stream, _, err := c.Events(ctx, req)
	if err != nil {
		return err
	}
	defer stream.Close()
	if *since == nil {
		if err := resync(); err != nil {
			return err
		}
	}
	for {
		ev, err := stream.Next()
		if err != nil {
			return err
		}
		if ev.Type == Resync {
			if err := resync(); err != nil {
				return err
			}
			continue
		}
		if *since != nil && ev.Seq <= **since {
			continue // already in the snapshot
		}
		seq := ev.Seq
		*since = &seq
		if err := fn(Update{Event: &ev}); err != nil {
			return stopError{err}
		}
	}
}
