package protocol

import (
	"context"
	"encoding/json"
	"slices"
)

type peerKey struct{}

// withPeer records a protocol-2 client's hello in the context its
// requests are handled with.
func withPeer(ctx context.Context, hello Message) context.Context {
	var h Hello
	if json.Unmarshal(hello.Params, &h) != nil {
		return ctx
	}
	return context.WithValue(ctx, peerKey{}, h)
}

// PeerHas reports whether the client whose request ctx belongs to offered
// capability in its hello. Protocol-1 clients offer none.
func PeerHas(ctx context.Context, capability string) bool {
	h, ok := ctx.Value(peerKey{}).(Hello)
	return ok && slices.Contains(h.Capabilities, capability)
}
