package activity

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSubscribeReceivesPublishedEvent(t *testing.T) {
	b := NewBroker(4)
	ch, cancel := b.Subscribe()
	defer cancel()

	want := Event{
		Ledger:   12,
		TxHash:   "0x1",
		Contract: "C",
		Event:    "GrantCreated",
		Payload:  json.RawMessage(`{"a":1}`),
		SeenAt:   time.Now(),
	}
	b.Publish(want)

	select {
	case got := <-ch:
		if got.Event != want.Event || got.Ledger != want.Ledger {
			t.Fatalf("got %+v, want event %q ledger %d", got, want.Event, want.Ledger)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

func TestCancelStopsDeliveryAndClosesChannel(t *testing.T) {
	b := NewBroker(4)
	ch, cancel := b.Subscribe()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	// Publishing after cancel must not panic and must not deliver.
	b.Publish(Event{Event: "x"})
}

func TestSlowSubscriberDropsOldestNotDeadlock(t *testing.T) {
	b := NewBroker(2)
	ch, cancel := b.Subscribe()
	defer cancel()
	// Fill the buffer without reading.
	b.Publish(Event{Event: "a"})
	b.Publish(Event{Event: "b"})
	// Third publish should evict the oldest, not block.
	done := make(chan struct{})
	go func() {
		b.Publish(Event{Event: "c"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish to full buffer blocked (deadlock)")
	}
	if len(ch) != 2 {
		t.Fatalf("buffer len = %d, want 2", len(ch))
	}
}

func TestMultipleSubscribersEachReceive(t *testing.T) {
	b := NewBroker(4)
	ch1, c1 := b.Subscribe()
	ch2, c2 := b.Subscribe()
	defer c1()
	defer c2()
	b.Publish(Event{Event: "e"})
	select {
	case <-ch1:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber 1 missed event")
	}
	select {
	case <-ch2:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber 2 missed event")
	}
}
