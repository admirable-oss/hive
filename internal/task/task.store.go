package task

import "context"

// Store persists plans. Every plan is written whole: plans are small (their
// screens, logs and summaries are bounded), and a whole write is atomic.
type Store interface {
	// List returns every readable plan. Plans it cannot read are reported
	// in the error, joined, and never stop the others from loading.
	List(ctx context.Context) ([]Plan, error)
	Put(ctx context.Context, p *Plan) error
	Delete(ctx context.Context, id string) error
}
