package protocol

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPeerHas(t *testing.T) {
	params, _ := json.Marshal(Hello{Client: "ui", Capabilities: []string{"frames/1", "frames/2"}})
	ctx := withPeer(context.Background(), Message{Params: params})
	if !PeerHas(ctx, "frames/2") || PeerHas(ctx, "frames/3") {
		t.Fatal("PeerHas must report exactly the offered capabilities")
	}
	if PeerHas(context.Background(), "frames/1") {
		t.Fatal("a protocol-1 client (no hello) offers nothing")
	}
	if PeerHas(withPeer(context.Background(), Message{Params: []byte("{bad")}), "frames/1") {
		t.Fatal("an unreadable hello offers nothing")
	}
}

func TestPeerBudget(t *testing.T) {
	params, _ := json.Marshal(Hello{Client: "ui"})
	p := PeerOf(withPeer(context.Background(), Message{Params: params}))
	if p.Spend(1<<20) != 0 {
		t.Fatal("without a budget nothing waits")
	}
	p.Report(40*time.Millisecond, 1000)
	if rtt, budget := p.Link(); rtt != 40*time.Millisecond || budget != 1000 {
		t.Fatalf("link = %v, %d", rtt, budget)
	}
	if w := p.Spend(500); w != 0 {
		t.Fatalf("within the budget: wait %v", w)
	}
	// 1500 bytes against a 1000-byte bucket: half a second short.
	if w := p.Spend(1000); w < 400*time.Millisecond || w > 600*time.Millisecond {
		t.Fatalf("over the budget: wait %v", w)
	}
	if PeerOf(context.Background()) != nil || (*Peer)(nil).Spend(10) != 0 {
		t.Fatal("protocol-1 clients have no peer and no budget")
	}
}
