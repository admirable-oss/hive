package observer

import (
	"context"
	"testing"
	"time"
)

// TestPluginsNeedTheWholeRuntime: observe.plugins answers credentials that
// see the whole runtime, and refuses one scoped to part of it.
func TestPluginsNeedTheWholeRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, store := newTestServer(t)
	s.cfg.Sources.Plugins = func(context.Context) []Plugin {
		return []Plugin{{ID: "pr-status", Name: "PR status", Tier: "community", Sandbox: "none", Enabled: true, Revoked: &PluginRevocation{Reason: "leaks the token"}}}
	}
	connect := func(req CreateRequest) *Client {
		t.Helper()
		_, token, err := store.Create(req)
		if err != nil {
			t.Fatal(err)
		}
		c, err := Connect(ctx, ClientConfig{Dial: serve(t, s), Token: token})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	all := connect(CreateRequest{Name: "all", Scope: Scope{All: true}, TTL: MinTTL})
	got, err := all.Plugins(ctx)
	if err != nil || len(got) != 1 || got[0].Tier != "community" || got[0].Revoked == nil {
		t.Fatalf("all sees the plugins: %+v %v", got, err)
	}
	dev := connect(CreateRequest{Name: "dev", Scope: Scope{Environments: []string{"dev"}}, TTL: MinTTL})
	if _, err := dev.Plugins(ctx); code(err) != "permission_denied" {
		t.Fatalf("a scoped credential: %v", err)
	}
}
