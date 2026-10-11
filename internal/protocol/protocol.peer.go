package protocol

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

type peerKey struct{}

// Peer is a protocol-2 client as the server knows it: its hello, and what
// it reports about its link (see Report), which paces what it is sent.
type Peer struct {
	Hello

	mu     sync.Mutex
	rtt    time.Duration
	budget int // bytes per second; 0 is unlimited
	tokens float64
	last   time.Time
}

// withPeer records a protocol-2 client's hello in the context its
// requests are handled with.
func withPeer(ctx context.Context, hello Message) context.Context {
	var h Hello
	if json.Unmarshal(hello.Params, &h) != nil {
		return ctx
	}
	return context.WithValue(ctx, peerKey{}, &Peer{Hello: h})
}

// LinkReport is what a client says about its link (method client.report):
// its round-trip time, and the bytes per second it wants to be sent at
// most (0: no limit).
type LinkReport struct {
	RTTMicros int64 `json:"rtt_us"`
	Budget    int   `json:"budget,omitempty"`
}

// PeerOf returns the client whose request ctx belongs to; nil for a
// protocol-1 client.
func PeerOf(ctx context.Context) *Peer {
	p, _ := ctx.Value(peerKey{}).(*Peer)
	return p
}

// PeerHas reports whether the client whose request ctx belongs to offered
// capability in its hello. Protocol-1 clients offer none.
func PeerHas(ctx context.Context, capability string) bool {
	p := PeerOf(ctx)
	return p != nil && slices.Contains(p.Capabilities, capability)
}

// Report records the client's round-trip time and how many bytes per
// second it wants to be sent at most (0: no limit).
func (p *Peer) Report(rtt time.Duration, budget int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rtt = max(rtt, 0)
	if budget != p.budget {
		p.budget, p.tokens, p.last = max(budget, 0), float64(max(budget, 0)), time.Time{}
	}
}

// Link returns what the client last reported.
func (p *Peer) Link() (rtt time.Duration, budget int) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rtt, p.budget
}

// Spend takes n bytes sent to the client from its budget (shared by
// everything sent to it, refilled at budget bytes per second, up to one
// second's worth) and returns how long the sender should wait before
// sending again so the budget holds. Without a budget it returns 0.
func (p *Peer) Spend(n int) time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.budget <= 0 {
		return 0
	}
	now := time.Now()
	rate := float64(p.budget)
	if !p.last.IsZero() {
		p.tokens = min(p.tokens+now.Sub(p.last).Seconds()*rate, rate)
	}
	p.last = now
	p.tokens -= float64(n)
	if p.tokens >= 0 {
		return 0
	}
	return time.Duration(-p.tokens / rate * float64(time.Second))
}
