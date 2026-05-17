package events

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestBusPublishesMatchingEvents(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe(context.Background(), Filter{Kind: ScopeService, Service: "payments-api"}, SubscribeOptions{Buffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	ignored := schema.Event{ID: "01JIGNORED", Subject: schema.Target{Kind: "service", Name: "orders-api"}, Type: "deploy.started"}
	if err := bus.Publish(context.Background(), ignored); err != nil {
		t.Fatal(err)
	}
	event := schema.Event{ID: "01JMATCH", Subject: schema.Target{Kind: "service", Name: "payments-api"}, Type: "deploy.started"}
	if err := bus.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sub.C:
		if got.Event.ID != "01JMATCH" || got.ResyncRequired {
			t.Fatalf("unexpected delivery: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for bus delivery")
	}
}

func TestBusSlowSubscriberReceivesResyncRequired(t *testing.T) {
	bus := NewBus()
	sub, err := bus.Subscribe(context.Background(), Filter{}, SubscribeOptions{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := bus.Publish(context.Background(), schema.Event{ID: "01JFIRST", Subject: schema.Target{Kind: "service", Name: "payments-api"}}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), schema.Event{ID: "01JSECOND", Subject: schema.Target{Kind: "service", Name: "payments-api"}}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sub.C:
		if !got.ResyncRequired || got.LastEventID != "01JSECOND" {
			t.Fatalf("unexpected slow-subscriber delivery: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resync delivery")
	}
}
