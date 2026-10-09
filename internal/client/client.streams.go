package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/vt"
)

var jsonUnmarshal = json.Unmarshal

// Attachment is an open view of an agent's terminal. Read returns what the
// view sends (painted ANSI for an attach), Write types into the agent, and
// Close ends the view. It is an io.ReadWriteCloser.
type Attachment struct {
	View
	pipe      io.ReadWriteCloser
	processID string
	client    *service
}

func (a *Attachment) Read(p []byte) (int, error)  { return a.pipe.Read(p) }
func (a *Attachment) Write(p []byte) (int, error) { return a.pipe.Write(p) }
func (a *Attachment) Close() error                { return a.pipe.Close() }

// Resize reports this view's new size. The agent's terminal follows it while
// this view is the one in control (the last to type).
func (a *Attachment) Resize(ctx context.Context, width, height uint16) error {
	if a.client == nil {
		return nil
	}
	params := terminalParams{ProcessID: a.processID, Width: width, Height: height, ViewID: a.ID}
	if a.ID == "" {
		params.ViewID = "" // protocol 1: resize the terminal directly
	}
	_, err := call[struct{}](ctx, a.client, "terminal.resize", params)
	return err
}

// FrameStream is a view that yields the screen as frames.
type FrameStream struct {
	*Attachment
	reader *vt.FrameReader
}

// Next returns the next frame; io.EOF once the agent's output has ended and
// the view closed.
func (f *FrameStream) Next() (*vt.Frame, error) { return f.reader.Next() }

// EventStream delivers daemon events.
type EventStream struct {
	pipe    io.ReadCloser
	scanner *bufio.Scanner
}

func newEventStream(p io.ReadCloser) *EventStream {
	sc := bufio.NewScanner(p)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return &EventStream{pipe: p, scanner: sc}
}

// Next returns the next event. An event of type event.Lost means events
// were dropped: re-read whatever state the caller tracks.
func (e *EventStream) Next() (event.Event, error) {
	if !e.scanner.Scan() {
		if err := e.scanner.Err(); err != nil {
			return event.Event{}, err
		}
		return event.Event{}, io.EOF
	}
	var ev event.Event
	if err := json.Unmarshal(e.scanner.Bytes(), &ev); err != nil {
		return event.Event{}, fmt.Errorf("decode event: %w", err)
	}
	return ev, nil
}

// Close ends the subscription.
func (e *EventStream) Close() error {
	err := e.pipe.Close()
	if errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

// NewEventStream wraps a pipe carrying JSON events, one per line. Tests and
// alternative transports use it.
func NewEventStream(p io.ReadCloser) *EventStream { return newEventStream(p) }

// NewFrameStream wraps a pipe carrying frames. Resize is a no-op on it.
func NewFrameStream(p io.ReadWriteCloser, view View) *FrameStream {
	return &FrameStream{Attachment: &Attachment{pipe: p, View: view}, reader: vt.NewFrameReader(p)}
}
