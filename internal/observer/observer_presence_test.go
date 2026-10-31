package observer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/event"
	"github.com/admirable-oss/hive/internal/presence"
)

type fakePresence struct {
	entries []presence.Entry
	holds   []presence.Hold
}

func (f fakePresence) List() []presence.Entry { return f.entries }
func (f fakePresence) Holds() []presence.Hold { return f.holds }

// TestPresenceIsScoped: observe.presence names people only where the
// credential sees them, without their connections, and the holds of the
// panes it sees; someone nowhere in particular only to one that sees all.
func TestPresenceIsScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	alice := presence.Person{ID: "m1", Name: "alice"}
	s, store := newTestServer(t)
	s.cfg.Sources.Presence = fakePresence{
		entries: []presence.Entry{
			{Connection: "c1", Person: alice, Client: "hive-desktop", Spot: presence.Spot{Environment: "dev", Pane: "p1"}, Since: at.Add(time.Minute)},
			{Connection: "c2", Person: alice, Spot: presence.Spot{Environment: "dev", Pane: "p1"}, Since: at},
			{Connection: "c3", Person: presence.Person{ID: "m2", Name: "bob"}, Spot: presence.Spot{Environment: "ops"}, Since: at},
			{Connection: "c4", Person: presence.Person{ID: presence.OwnerID, Name: "me"}, Since: at},
		},
		holds: []presence.Hold{
			{Process: "proc1", Pane: "p1", Environment: "dev", Holder: alice, Since: at, Until: at.Add(time.Minute)},
			{Process: "proc2", Pane: "p2", Environment: "ops", Holder: presence.Person{ID: "m2", Name: "bob"}, Since: at},
		},
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

	dev, err := connect(CreateRequest{Name: "dev", Scope: Scope{Environments: []string{"dev"}}, TTL: MinTTL}).Presence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.People) != 1 || dev.People[0] != (Present{Name: "alice", Environment: "dev", Pane: "p1", Since: at}) {
		t.Fatalf("dev's people: %+v", dev.People)
	}
	if len(dev.Floors) != 1 || dev.Floors[0].Holder != "alice" || dev.Floors[0].Pane != "p1" {
		t.Fatalf("dev's floors: %+v", dev.Floors)
	}
	raw, _ := json.Marshal(dev)
	for _, hidden := range []string{"m1", "c1", "hive-desktop", "proc1"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatalf("dev's presence has %q: %s", hidden, raw)
		}
	}

	all, err := connect(CreateRequest{Name: "all", Scope: Scope{All: true}, TTL: MinTTL}).Presence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.People) != 3 || len(all.Floors) != 2 {
		t.Fatalf("everyone's presence: %+v", all)
	}
	if me := all.People[2]; me.Name != "me" || !me.Owner {
		t.Fatalf("the owner: %+v", me)
	}

	// Without a source, nobody is shown.
	s, store = newTestServer(t)
	none, err := connect(CreateRequest{Name: "none", Scope: Scope{All: true}, TTL: MinTTL}).Presence(ctx)
	if err != nil || len(none.People) != 0 || len(none.Floors) != 0 {
		t.Fatalf("no presence: %+v %v", none, err)
	}
}

// TestPresenceEvents: a pane's floor is seen where the pane is, free once
// no one holds it; everyone's presence only by observers that see all.
func TestPresenceEvents(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	held, _ := json.Marshal(presence.Hold{Process: "proc1", Pane: "p1", Environment: "dev", Holder: presence.Person{ID: "m1", Name: "alice"}, Since: at})
	ev, ok := Translate(event.Event{Seq: 1, Type: EventPaneFloor, Data: held}, fakeLocator{})
	if !ok || !ev.Visible(Scope{Environments: []string{"dev"}}) || ev.Visible(Scope{Environments: []string{"ops"}}) {
		t.Fatalf("a held floor: %+v %v", ev, ok)
	}
	if strings.Contains(string(ev.Data), "m1") || !strings.Contains(string(ev.Data), `"holder":"alice"`) {
		t.Fatalf("a held floor's data: %s", ev.Data)
	}
	free, _ := json.Marshal(map[string]any{"process_id": "proc1", "holder": nil, "pane": "p1", "environment": "dev"})
	ev, ok = Translate(event.Event{Seq: 2, Type: EventPaneFloor, Data: free}, fakeLocator{})
	if !ok || strings.Contains(string(ev.Data), "holder") || !ev.Visible(Scope{Panes: []string{"p1"}, Environments: []string{"dev"}}) {
		t.Fatalf("a free floor: %s %v", ev.Data, ok)
	}

	changed, _ := json.Marshal(map[string]any{"presence": []presence.Entry{{Connection: "c1", Person: presence.Person{ID: "m1", Name: "alice"}, Spot: presence.Spot{Environment: "dev"}, Since: at}}})
	ev, ok = Translate(event.Event{Seq: 3, Type: EventPresenceChanged, Data: changed}, fakeLocator{})
	if !ok || ev.Visible(Scope{Environments: []string{"dev"}}) || !ev.Visible(Scope{All: true}) {
		t.Fatalf("presence.changed: %+v %v", ev, ok)
	}
	if strings.Contains(string(ev.Data), "c1") || !strings.Contains(string(ev.Data), `"name":"alice"`) {
		t.Fatalf("presence.changed's data: %s", ev.Data)
	}
}
