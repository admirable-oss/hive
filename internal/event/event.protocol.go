package event

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/admirable-oss/hive/internal/protocol"
)

type subscribeParams struct {
	// Types are type prefixes ("process.", "environment.created"); empty
	// means every event.
	Types []string `json:"types,omitempty"`
}

type subscribeResult struct {
	// Seq is the last event published before the subscription started.
	Seq uint64 `json:"seq"`
}

// Filter decides what one subscriber may see of an event: the event to
// send (changed, or as it is), or false to leave it out.
type Filter func(Event) (Event, bool)

type filterKey struct{}

// WithFilter makes events.subscribe, called with ctx, pass every event
// through f: what a caller who may not see everything gets.
func WithFilter(ctx context.Context, f Filter) context.Context {
	return context.WithValue(ctx, filterKey{}, f)
}

// Register exposes the bus as events.subscribe: a pipe carrying one JSON
// event per line until the client closes it.
func Register(r *protocol.Router, b *Bus) {
	r.MustRegister("events.subscribe", protocol.PipeMethod(func(ctx context.Context, p subscribeParams) (subscribeResult, protocol.PipeFunc, error) {
		filter, _ := ctx.Value(filterKey{}).(Filter)
		sub := b.Subscribe(DefaultBuffer, p.Types...)
		res := subscribeResult{Seq: b.Seq()}
		return res, func(ctx context.Context, pipe protocol.Pipe) error {
			defer sub.Close()
			ctx, stop := protocol.UntilClosed(ctx, pipe)
			defer stop()
			for {
				ev, err := sub.Next(ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return nil
					}
					return err
				}
				if filter != nil && ev.Type != Lost {
					var ok bool
					if ev, ok = filter(ev); !ok {
						continue
					}
				}
				line, _ := json.Marshal(ev)
				if _, err := pipe.Write(append(line, '\n')); err != nil {
					return nil
				}
			}
		}, nil
	}))
}
