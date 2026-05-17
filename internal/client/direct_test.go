package client

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDirectStatusReadsMemoryStore(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_02",
		StableRelease:  "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T21:00:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	})

	direct, err := NewDirect(config.Config{
		Mode:        config.ModeDirect,
		Env:         "prod",
		Provider:    "aws",
		Region:      "us-west-2",
		StateBucket: "memory://test",
	}, DirectOptions{
		Store: store,
		Clock: func() time.Time {
			return time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new direct client: %v", err)
	}
	status, err := direct.Status(context.Background(), StatusOptions{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Mode != config.ModeDirect || status.Freshness.Source != "direct_object_store" || !status.Freshness.Ready {
		t.Fatalf("unexpected status metadata: %+v", status)
	}
	if len(status.Services) != 1 || status.Services[0].Service != "payments-api" || status.Services[0].DesiredRelease != "rel_02" {
		t.Fatalf("unexpected services: %+v", status.Services)
	}
}

func TestDirectDoctorReadsObjectState(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_02",
		StableRelease:  "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T21:00:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	})

	direct, err := NewDirect(config.Config{
		Mode:        config.ModeDirect,
		Env:         "prod",
		Provider:    "aws",
		Region:      "us-west-2",
		StateBucket: "memory://test",
	}, DirectOptions{
		Store: store,
		Clock: func() time.Time {
			return time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new direct client: %v", err)
	}
	result, err := direct.Doctor(context.Background(), DoctorOptions{Service: "payments-api", TraceID: "tr_doctor"})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if result.TraceID != "tr_doctor" || result.Service != "payments-api" || result.Source != "direct" {
		t.Fatalf("unexpected doctor metadata: %+v", result)
	}
	if len(result.Findings) == 0 {
		t.Fatalf("expected status-derived findings: %+v", result)
	}
}

func TestDirectWatchEventsPollsObjectState(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "sagas/saga_01JABC/events/01JEVT.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JEVT",
		Time:          "2026-05-16T21:00:00Z",
		Subject:       schema.Target{Kind: "saga", Name: "saga_01JABC"},
		Type:          "approval.required",
		Summary:       "approval required",
	})
	direct, err := NewDirect(config.Config{
		Mode:        config.ModeDirect,
		StateBucket: "memory://test",
	}, DirectOptions{Store: store})
	if err != nil {
		t.Fatalf("new direct client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := direct.WatchEvents(ctx, EventWatchOptions{
		EventOptions: EventOptions{Scope: "saga", Saga: "saga_01JABC"},
		PollInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("watch events: %v", err)
	}
	got := <-ch
	if got.Event.ID != "01JEVT" || got.Event.Type != "approval.required" || got.LastEventID != "01JEVT" {
		t.Fatalf("unexpected watched event: %+v", got)
	}
}

func createJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
}
