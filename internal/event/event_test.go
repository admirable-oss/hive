package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/admirable-oss/hive/internal/event"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func next(t *testing.T, s *event.Subscription) event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ev, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestPublishReachesMatchingSubscribersInOrder(t *testing.T) {
	b := event.NewBus(nil)
	all := b.Subscribe(0)
	procs := b.Subscribe(0, "process.")
	defer all.Close()
	defer procs.Close()

	b.Publish(event.EnvironmentCreated, map[string]string{"id": "dev"})
	b.Publish(event.ProcessStarted, map[string]string{"id": "p1"})
	b.Publish(event.ProcessExited, map[string]string{"id": "p1"})

	for i, want := range []string{event.EnvironmentCreated, event.ProcessStarted, event.ProcessExited} {
		ev := next(t, all)
		if ev.Type != want || ev.Seq != uint64(i+1) {
			t.Fatalf("event %d = %s #%d, want %s #%d", i, ev.Type, ev.Seq, want, i+1)
		}
	}
	if ev := next(t, procs); ev.Type != event.ProcessStarted || ev.Seq != 2 {
		t.Fatalf("filtered subscriber got %s #%d", ev.Type, ev.Seq)
	}
	var data map[string]string
	if ev := next(t, procs); json.Unmarshal(ev.Data, &data) != nil || data["id"] != "p1" {
		t.Fatalf("unexpected data %s", ev.Data)
	}
}

func TestSlowSubscriberIsToldWhatItLost(t *testing.T) {
	b := event.NewBus(nil)
	s := b.Subscribe(2)
	defer s.Close()
	for range 5 {
		b.Publish(event.ProcessStarted, nil) // never blocks
	}
	ev := next(t, s)
	if ev.Type != event.Lost {
		t.Fatalf("first event = %s, want %s", ev.Type, event.Lost)
	}
	var lost map[string]int
	_ = json.Unmarshal(ev.Data, &lost)
	if lost["missed"] != 3 {
		t.Fatalf("missed = %d, want 3", lost["missed"])
	}
	if got := next(t, s); got.Seq != 1 {
		t.Fatalf("after the marker the queued events follow, got #%d", got.Seq)
	}
}

func TestNextEndsWithContextAndClose(t *testing.T) {
	b := event.NewBus(nil)
	s := b.Subscribe(0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Next(ctx); err == nil {
		t.Fatal("expected the context to end Next")
	}
	go s.Close()
	if _, err := s.Next(context.Background()); err == nil {
		t.Fatal("expected Close to end Next")
	}
	s.Close()
	b.Publish(event.ProcessStarted, nil) // a closed subscription receives nothing
}

func TestUnencodableDataIsDropped(t *testing.T) {
	b := event.NewBus(nil)
	b.Publish("bad", make(chan int))
	if b.Seq() != 0 {
		t.Fatal("an event that cannot be encoded must not be published")
	}
}
