package core

import (
	"slices"
	"testing"

	"github.com/admirable-oss/hive/internal/presence"
)

// TestWithPresence: the others are shown where they are, and where they
// type; this app's own person, here or elsewhere, is not someone else.
func TestWithPresence(t *testing.T) {
	ws := Workspace{
		Environments: []Environment{{ID: "web"}, {ID: "api"}},
		Panes:        []Pane{{ID: "p1", EnvironmentID: "web", ProcessID: "x1"}, {ID: "p2", EnvironmentID: "web", ProcessID: "x2"}},
	}
	alice := presence.Person{ID: "m_a", Name: "alice"}
	me := presence.Person{ID: presence.OwnerID, Name: "me"}
	p := presenceList{
		Presence: []presence.Entry{
			{Connection: "1", Person: alice, Spot: presence.Spot{Environment: "web", Pane: "p1"}},
			{Connection: "2", Person: alice, Spot: presence.Spot{Environment: "web", Pane: "p1"}},
			{Connection: "3", Person: me, Spot: presence.Spot{Environment: "api"}},
		},
		Holds: []presence.Hold{
			{Process: "x2", Holder: alice},          // found by its process
			{Process: "x1", Pane: "p1", Holder: me}, // this app's person: not shown
		},
	}
	got := withPresence(ws, p, "")
	if !slices.Equal(got.Environments[0].With, []string{"alice"}) || got.Environments[1].With != nil {
		t.Errorf("environments: %+v", got.Environments)
	}
	if !slices.Equal(got.Panes[0].Watchers, []string{"alice"}) || got.Panes[0].Typing != "" {
		t.Errorf("p1: %+v", got.Panes[0])
	}
	if got.Panes[1].Typing != "alice" || got.Panes[1].Watchers != nil {
		t.Errorf("p2: %+v", got.Panes[1])
	}

	// As alice: the runtime's own user is the other one.
	got = withPresence(ws, p, alice.ID)
	if !slices.Equal(got.Environments[1].With, []string{"me"}) || got.Panes[0].Typing != "me" || got.Panes[1].Typing != "" {
		t.Errorf("as alice: %+v", got)
	}
}
