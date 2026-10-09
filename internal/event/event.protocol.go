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

// Register exposes the bus as events.subscribe: a pipe carrying one JSON
// event per line until the client closes it.
func Register(r *protocol.Router, b *Bus) {
	r.MustRegister("events.subscribe", protocol.PipeMethod(func(_ context.Context, p subscribeParams) (subscribeResult, protocol.PipeFunc, error) {
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
				line, _ := json.Marshal(ev)
				if _, err := pipe.Write(append(line, '\n')); err != nil {
					return nil
				}
			}
		}, nil
	}))
}
