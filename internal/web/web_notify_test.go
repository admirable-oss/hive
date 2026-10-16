package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/agent"
	"github.com/admirable-oss/hive/internal/observer"
)

type fired struct{ kind, agent string }

func newTestNotifier(t *testing.T) (*notifier, *[]fired) {
	t.Helper()
	srv, err := New(Config{Dir: t.TempDir(), Listen: "127.0.0.1:0", Push: PushConfig{Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	key, err := loadVAPID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := newNotifier(srv, &pusher{key: key, now: time.Now})
	t.Cleanup(n.close)
	var got []fired
	n.transition = func(_, kind string, a observer.Agent, _ string) { got = append(got, fired{kind, a.ID}) }
	return n, &got
}

func snap(agents ...observer.Agent) observer.Update {
	return observer.Update{Snapshot: &observer.Snapshot{Agents: agents}}
}

func state(a observer.Agent) observer.Update {
	raw, _ := json.Marshal(a)
	return observer.Update{Event: &observer.Event{Type: "agent.state", Data: raw}}
}

func TestNotifierFiresOncePerTransition(t *testing.T) {
	n, got := newTestNotifier(t)
	a := observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 3, CompletionSeq: 1}
	// The first look at a runtime notifies about nothing already true.
	n.update("rt", snap(a))
	n.update("rt", state(a))
	if len(*got) != 0 {
		t.Fatalf("fired on what was already so: %v", *got)
	}
	// Live transitions fire once each.
	n.update("rt", state(observer.Agent{ID: "a", State: agent.StateWorking, StateSeq: 4, CompletionSeq: 1}))
	n.update("rt", state(observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 5, CompletionSeq: 1}))
	n.update("rt", state(observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 5, CompletionSeq: 1})) // repeated
	n.update("rt", state(observer.Agent{ID: "a", State: agent.StateDone, StateSeq: 6, CompletionSeq: 2}))
	// An old state arriving late changes nothing.
	n.update("rt", state(observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 2, CompletionSeq: 1}))
	// A resync shows what happened while disconnected: told once.
	n.update("rt", snap(observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 9, CompletionSeq: 3}))
	n.update("rt", snap(observer.Agent{ID: "a", State: agent.StateBlocked, StateSeq: 9, CompletionSeq: 3}))
	var kinds []string
	for _, f := range *got {
		kinds = append(kinds, f.kind)
	}
	if strings.Join(kinds, ",") != "blocked,done,blocked,done" {
		t.Fatalf("fired %v", kinds)
	}
	// A new agent that blocks is told about.
	n.update("rt", state(observer.Agent{ID: "b", State: agent.StateBlocked, StateSeq: 1}))
	if last := (*got)[len(*got)-1]; last != (fired{KindBlocked, "b"}) {
		t.Fatalf("new agent: %v", last)
	}
}

func TestDeviceRateIsBounded(t *testing.T) {
	n, _ := newTestNotifier(t)
	now := time.Now()
	allowed := 0
	for range perMinute + 5 {
		if n.allow("dev", now) {
			allowed++
		}
	}
	if allowed != perMinute || n.buckets["dev"].pending != 5 {
		t.Fatalf("allowed %d, pending %d", allowed, n.buckets["dev"].pending)
	}
	if !n.allow("dev", now.Add(61*time.Second)) {
		t.Fatal("still limited after a minute")
	}
}

func TestPrivateNotificationsNameNothing(t *testing.T) {
	a := observer.Agent{ID: "p1", Name: "secret-project-agent", Kind: "claude", EnvironmentID: "acme-merger", PaneID: "p9"}
	for _, kind := range []string{KindBlocked, KindDone, KindFailed} {
		m := message("laptop", kind, a, "a permission dialog", DetailPrivate, time.Now())
		for _, leak := range []string{"secret", "acme", "laptop", "claude", "p9", "permission"} {
			if strings.Contains(string(m.Payload), leak) {
				t.Errorf("%s private payload names %q: %s", kind, leak, m.Payload)
			}
		}
		named := message("laptop", kind, a, "", DetailNames, time.Now())
		if !strings.Contains(string(named.Payload), "secret-project-agent") || !strings.Contains(string(named.Payload), "/r/laptop/p/p9") {
			t.Errorf("%s named payload: %s", kind, named.Payload)
		}
		if len(m.Topic) > 32 || m.Topic == "" {
			t.Errorf("topic %q", m.Topic)
		}
	}
	p := DefaultPrefs()
	until := time.Now().Add(time.Hour)
	p.Mutes = []Mute{{Runtime: "laptop", Environment: "acme-merger"}, {Runtime: "laptop", Agent: "x", Until: &until}}
	if !p.muted("laptop", "acme-merger", "p1", time.Now()) || !p.muted("laptop", "other", "x", time.Now()) || p.muted("laptop", "other", "x", until.Add(time.Second)) || p.muted("desk", "acme-merger", "p1", time.Now()) {
		t.Fatal("mutes")
	}
	if safeNext("//evil.example") != "" || safeNext("https://evil.example") != "" || safeNext("/login?next=x") != "" || safeNext("/r/x/p/y?from=notification") == "" {
		t.Fatal("next paths")
	}
}
