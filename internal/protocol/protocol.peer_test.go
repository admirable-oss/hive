package protocol

import (
	"context"
	"encoding/json"
	"testing"
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
