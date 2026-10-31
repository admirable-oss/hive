package presence

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

var (
	me    = Person{ID: OwnerID, Name: "me"}
	alice = Person{ID: "m_0000000001", Name: "alice"}
	bob   = Person{ID: "m_0000000002", Name: "bob"}
)

type changes struct {
	mu    sync.Mutex
	kinds []string
}

func (c *changes) record(kind string, _ *Hold, _ string) {
	c.mu.Lock()
	c.kinds = append(c.kinds, kind)
	c.mu.Unlock()
}

func (c *changes) count(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, k := range c.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

func TestPresence(t *testing.T) {
	var ch changes
	r := New(0, ch.record)
	a1, a2, o := new(int), new(int), new(int)
	if !r.Set(a1, alice, "hive-tui", Spot{Environment: "dev", Pane: "p1"}) || r.Set(a1, alice, "hive-tui", Spot{Environment: "dev", Pane: "p1"}) {
		t.Fatal("a connection is new once")
	}
	r.Set(a2, alice, "hive", Spot{Environment: "dev"})
	r.Set(o, me, "hive-tui", Spot{Environment: "ops"})
	list := r.List()
	if len(list) != 3 || !list[0].Person.Owner() || list[1].Connection == list[2].Connection {
		t.Fatalf("list = %+v", list)
	}
	if ch.count(ChangedPresence) != 3 {
		t.Fatalf("an unchanged spot announced a change: %v", ch.kinds)
	}
	r.Leave(a1)
	r.Leave(a1)
	if len(r.List()) != 2 || ch.count(ChangedPresence) != 4 {
		t.Fatalf("after leaving: %+v, %v", r.List(), ch.kinds)
	}
}

func TestOnePersonTypesAtATime(t *testing.T) {
	var ch changes
	r := New(time.Hour, ch.record)
	if err := r.Use("proc", "p1", "dev", alice); err != nil {
		t.Fatal(err)
	}
	// Alice's own clients never conflict with each other.
	if err := r.Use("proc", "p1", "dev", alice); err != nil {
		t.Fatal(err)
	}
	var held *HeldError
	if err := r.Use("proc", "p1", "dev", me); !errors.As(err, &held) || held.Holder != alice || !errors.Is(err, ErrHeld) {
		t.Fatalf("the owner typing over alice: %v", err)
	}
	// Another terminal is free.
	if err := r.Use("other", "p2", "dev", bob); err != nil {
		t.Fatal(err)
	}
	if r.Release("proc", bob) {
		t.Fatal("bob released what alice holds")
	}
	prev, err := r.Claim("proc", "p1", "dev", me, true)
	if err != nil || prev == nil || *prev != alice {
		t.Fatalf("claim with force: %v %v", prev, err)
	}
	if h, _ := r.Holder("proc"); h != me {
		t.Fatalf("holder = %+v", h)
	}
	if !r.Release("proc", me) {
		t.Fatal("the holder could not release")
	}
	if err := r.Use("proc", "p1", "dev", alice); err != nil {
		t.Fatalf("a released terminal: %v", err)
	}
	r.ReleaseIf(func(id string) bool { return id == alice.ID })
	if _, ok := r.Holder("proc"); ok {
		t.Fatal("alice left but still holds the terminal")
	}
	if h := r.Holds(); len(h) != 1 || h[0].Holder != bob {
		t.Fatalf("holds = %+v", h)
	}
}

func TestHolderLapsesWhenSilent(t *testing.T) {
	var ch changes
	r := New(30*time.Millisecond, ch.record)
	if err := r.Use("proc", "p1", "dev", alice); err != nil {
		t.Fatal(err)
	}
	// Typing keeps it.
	for range 4 {
		time.Sleep(15 * time.Millisecond)
		if err := r.Use("proc", "p1", "dev", alice); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Use("proc", "p1", "dev", bob); err == nil {
		t.Fatal("bob took a terminal alice is typing into")
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.Use("proc", "p1", "dev", bob) != nil {
		if time.Now().After(deadline) {
			t.Fatal("a silent holder never let go")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ch.count(ChangedFloor) < 3 { // alice took, lapsed, bob took
		t.Fatalf("changes = %v", ch.kinds)
	}
}

// TestInputNeverInterleaves: people racing to type into one terminal never
// both get through while the first holds it.
func TestInputNeverInterleaves(t *testing.T) {
	r := New(time.Hour, nil)
	people := []Person{me, alice, bob}
	var (
		mu      sync.Mutex
		writers = map[string]bool{}
		wg      sync.WaitGroup
	)
	for i := range 30 {
		wg.Go(func() {
			who := people[i%len(people)]
			for range 50 {
				if r.Use("proc", "p1", "dev", who) == nil {
					mu.Lock()
					writers[who.ID] = true
					mu.Unlock()
				}
				if rand.IntN(10) == 0 {
					r.Release("proc", who)
				}
			}
		})
	}
	wg.Wait()
	// Writers changed only through releases; whoever holds it now is the
	// last who got through, and the registry never had two holders.
	if h := r.Holds(); len(h) > 1 {
		t.Fatalf("holds = %+v", h)
	}
	if len(writers) == 0 {
		t.Fatal("nobody typed")
	}
}

func FuzzRegistry(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Fuzz(func(t *testing.T, ops []byte) {
		r := New(time.Hour, nil)
		people := []Person{me, alice, bob}
		conns := []*int{new(int), new(int), new(int)}
		for _, op := range ops {
			who := people[int(op>>4)%len(people)]
			proc := []string{"a", "b"}[int(op>>6)%2]
			switch op % 6 {
			case 0:
				_ = r.Use(proc, "p", "dev", who)
			case 1:
				_, _ = r.Claim(proc, "p", "dev", who, op&8 != 0)
			case 2:
				r.Release(proc, who)
			case 3:
				r.ReleaseIf(func(id string) bool { return id == who.ID })
			case 4:
				r.Set(conns[int(op>>3)%3], who, "c", Spot{Pane: proc})
			case 5:
				r.Leave(conns[int(op>>3)%3])
			}
			seen := map[string]bool{}
			for _, h := range r.Holds() {
				if seen[h.Process] {
					t.Fatalf("two holders of %s", h.Process)
				}
				seen[h.Process] = true
				// Whoever holds it is the only one whose input passes.
				for _, p := range people {
					if err := r.take(h.Process, "p", "dev", p, false); (err == nil) != (p == h.Holder) {
						t.Fatalf("%s's input with %s holding: %v", p.Name, h.Holder.Name, err)
					}
				}
			}
			if len(r.List()) > len(conns) {
				t.Fatalf("more entries than connections: %+v", r.List())
			}
		}
	})
}
