package watchhub

import (
	"strconv"
	"testing"
)

func TestReplayWithinBuffer(t *testing.T) {
	b := New(Config{BufferSize: 10, MaxConnections: 10})
	for i := 0; i < 5; i++ {
		b.Publish(Event{
			Type: EventAdded, Project: "p", Environment: "e", Service: "s",
			Payload: EndpointPayload{ID: "ep-" + strconv.Itoa(i)},
		})
	}
	res := b.Replay("p", "e", "s", 2)
	if res.Miss {
		t.Fatal("expected hit within buffer")
	}
	if len(res.Events) != 3 {
		t.Fatalf("events = %d want 3", len(res.Events))
	}
	if res.Events[0].ResourceVersion != "3" || res.Events[2].ResourceVersion != "5" {
		t.Fatalf("order = %+v", res.Events)
	}
}

func TestReplayOlderThanBufferTriggersMiss(t *testing.T) {
	b := New(Config{BufferSize: 3, MaxConnections: 10})
	for i := 0; i < 5; i++ {
		b.Publish(Event{
			Type: EventAdded, Project: "p", Environment: "e", Service: "s",
			Payload: EndpointPayload{ID: "ep-" + strconv.Itoa(i)},
		})
	}
	// Buffer retains RV 3,4,5. since=1 → since+1=2 < oldest=3 → miss.
	res := b.Replay("p", "e", "s", 1)
	if !res.Miss {
		t.Fatalf("expected miss, got events=%+v", res.Events)
	}
}

func TestSubscribeReceivesPublish(t *testing.T) {
	b := New(Config{BufferSize: 10, MaxConnections: 10})
	ch := b.Subscribe("p", "e", "s")
	defer b.Unsubscribe("p", "e", "s", ch)
	b.Publish(Event{Type: EventAdded, Project: "p", Environment: "e", Service: "s", Payload: EndpointPayload{ID: "x"}})
	ev := <-ch
	if ev.Type != EventAdded || ev.Payload.ID != "x" || ev.ResourceVersion != "1" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestConnectionCap(t *testing.T) {
	b := New(Config{BufferSize: 10, MaxConnections: 2})
	if !b.TryAcquireConnection() || !b.TryAcquireConnection() {
		t.Fatal("expected two acquires")
	}
	if b.TryAcquireConnection() {
		t.Fatal("expected cap")
	}
	b.ReleaseConnection()
	if !b.TryAcquireConnection() {
		t.Fatal("expected acquire after release")
	}
}
