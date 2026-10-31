package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
)

// TestModelFollowsFloors: who holds a pane's input comes with the
// snapshot and follows pane.floor; a free pane shows no one typing.
func TestModelFollowsFloors(t *testing.T) {
	m := NewModel()
	m.Apply(observer.Update{Snapshot: &observer.Snapshot{
		Seq:    1,
		Panes:  []observer.Pane{{ID: "p1", EnvironmentID: "dev"}, {ID: "p2", EnvironmentID: "dev"}},
		Floors: []observer.Floor{{Pane: "p1", Environment: "dev", Holder: "alice"}},
	}}, time.Now())
	if f := m.View().Floors["p1"]; f.Holder != "alice" {
		t.Fatalf("from the snapshot: %+v", m.View().Floors)
	}
	m.Apply(planEvent(2, observer.EventPaneFloor, observer.Floor{Pane: "p2", Environment: "dev", Holder: "bob"}), time.Now())
	m.Apply(planEvent(3, observer.EventPaneFloor, observer.Floor{Pane: "p1", Environment: "dev"}), time.Now())
	v := m.View()
	if _, held := v.Floors["p1"]; held || v.Floors["p2"].Holder != "bob" {
		t.Fatalf("after the events: %+v", v.Floors)
	}

	r := &shown{rt: Runtime{Name: "laptop"}, v: v}
	var b bytes.Buffer
	if err := paneItem(&page{}, r, "p2").Render(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "bob is typing") {
		t.Fatalf("the pane's row: %s", b.String())
	}
	b.Reset()
	if err := paneItem(&page{}, r, "p1").Render(&b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "typing") {
		t.Fatalf("a free pane's row: %s", b.String())
	}
}
