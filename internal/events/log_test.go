package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestAppendListAndDuplicateEvent(t *testing.T) {
	store := memory.New()
	log, err := NewLog(Options{
		Store: store,
		Clock: func() time.Time { return time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := NewServiceEvent("payments-api", "service.created", "service control created", time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC), "a")
	second := NewServiceEvent("payments-api", "service.updated", "desired release updated", time.Date(2026, 5, 16, 21, 31, 0, 0, time.UTC), "b")
	if _, err := log.Append(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), first); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate append error = %v, want ErrDuplicate", err)
	}

	events, err := log.List(context.Background(), Scope{Kind: ScopeService, Service: "payments-api"}, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].ID != first.ID || events[1].ID != second.ID {
		t.Fatalf("events not sorted by sortable id: %#v", events)
	}
}

func TestOperationAndSagaEventsUseScopedPrefixes(t *testing.T) {
	store := memory.New()
	log, err := NewLog(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	opEvent := NewOperationEvent("payments-api", "op_01JABC", "operation.started", "deploy started", time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC), "op")
	sagaEvent := NewSagaEvent("saga_01JABC", "approval.required", "approval required", time.Date(2026, 5, 16, 21, 31, 0, 0, time.UTC), "saga")
	if _, err := log.Append(context.Background(), opEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), sagaEvent); err != nil {
		t.Fatal(err)
	}

	opEvents, err := log.List(context.Background(), Scope{Kind: ScopeOperation, Service: "payments-api", Operation: "op_01JABC"}, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(opEvents) != 1 || opEvents[0].Scope.Operation != "op_01JABC" {
		t.Fatalf("unexpected operation events: %#v", opEvents)
	}
	sagaEvents, err := log.List(context.Background(), Scope{Kind: ScopeSaga, Saga: "saga_01JABC"}, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sagaEvents) != 1 || sagaEvents[0].Scope.Saga != "saga_01JABC" {
		t.Fatalf("unexpected saga events: %#v", sagaEvents)
	}
}

func TestAuditRecordIncludesRequiredFields(t *testing.T) {
	store := memory.New()
	log, err := NewLog(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	record := NewAuditRecord(
		schema.Actor{ID: "user:alice", Type: "user"},
		schema.Target{Kind: "service", Name: "payments-api"},
		"deploy.start",
		"alice started deployment",
		"tr_123",
		time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC),
		"audit",
	)
	record.Risk = schema.RiskMedium
	if _, err := log.AppendAudit(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	object, err := store.Get(context.Background(), "audit/2026-05-16/"+record.ID+".json")
	if err != nil {
		t.Fatal(err)
	}
	body := string(object.Body)
	for _, want := range []string{`"actor":{"id":"user:alice","type":"user"}`, `"trace_id":"tr_123"`, `"action":"deploy.start"`, `"target":{"kind":"service","name":"payments-api"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("audit record missing %s:\n%s", want, body)
		}
	}
}

func TestHashChainVerification(t *testing.T) {
	first := NewOperationEvent("payments-api", "op_01JABC", "operation.started", "started", time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC), "first")
	second := NewOperationEvent("payments-api", "op_01JABC", "operation.step", "step done", time.Date(2026, 5, 16, 21, 31, 0, 0, time.UTC), "second")
	if err := Link(nil, &first); err != nil {
		t.Fatal(err)
	}
	if err := Link(&first, &second); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain([]Event{first, second}); err != nil {
		t.Fatalf("VerifyChain() error = %v", err)
	}
	second.Summary = "tampered"
	if err := VerifyChain([]Event{first, second}); err == nil {
		t.Fatal("expected tampered chain to fail verification")
	}
}
