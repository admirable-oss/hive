package terminal

import (
	"context"
	"errors"
	"sync"

	"github.com/admirable-oss/hive/internal/vt"
)

// MaxGraphicsJournalBytes bounds the ordered graphics history retained for
// late viewers. If a session exceeds it, graphics streams fail explicitly.
const MaxGraphicsJournalBytes = 32 << 20

var ErrGraphicsJournalOverflow = errors.New("terminal: graphics history exceeded its byte limit")

// GraphicsReader is implemented by sessions that can replay their ordered
// Kitty APC output independently from coalesced cell frames.
type GraphicsReader interface {
	Graphics(context.Context, func(vt.GraphicsEvent) error) error
}

type graphicsJournal struct {
	mu       sync.Mutex
	commands []vt.GraphicsEvent
	bytes    int
	changed  chan struct{}
	closed   bool
	overflow bool
}

func newGraphicsJournal() *graphicsJournal {
	return &graphicsJournal{changed: make(chan struct{})}
}

func (j *graphicsJournal) publish(event vt.GraphicsEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.overflow {
		return
	}
	if len(event.Command) == 0 || len(event.Command) > vt.MaxGraphicsEventBytes || len(event.Command)+8 > MaxGraphicsJournalBytes-j.bytes {
		j.overflow = true
		j.signalLocked()
		return
	}
	event.Command = append([]byte(nil), event.Command...)
	j.commands = append(j.commands, event)
	j.bytes += len(event.Command) + 8
	j.signalLocked()
}

func (j *graphicsJournal) close() {
	j.mu.Lock()
	if !j.closed {
		j.closed = true
		j.signalLocked()
	}
	j.mu.Unlock()
}

func (j *graphicsJournal) signalLocked() {
	close(j.changed)
	j.changed = make(chan struct{})
}

func (j *graphicsJournal) stream(ctx context.Context, emit func(vt.GraphicsEvent) error) error {
	index := 0
	for {
		j.mu.Lock()
		if index < len(j.commands) {
			event := j.commands[index]
			event.Command = append([]byte(nil), event.Command...)
			index++
			j.mu.Unlock()
			if err := emit(event); err != nil {
				return err
			}
			continue
		}
		if j.overflow {
			j.mu.Unlock()
			return ErrGraphicsJournalOverflow
		}
		if j.closed {
			j.mu.Unlock()
			return nil
		}
		changed := j.changed
		j.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
	}
}
