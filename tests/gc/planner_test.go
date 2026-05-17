package gc_test

import (
	"context"
	"testing"
	"time"

	skiffgc "github.com/s1liconcow/skiff/internal/gc"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestGCPlanIsReadOnlyAndProtectsStatefulResources(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedGCState(t, store)
	now := time.Date(2026, 5, 17, 3, 0, 0, 0, time.UTC)

	plan, err := (skiffgc.Planner{Store: store, Clock: func() time.Time { return now }}).Plan(ctx, skiffgc.PlanRequest{
		Service:   "payments-api",
		Env:       "prod",
		Retention: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.ReadOnly {
		t.Fatalf("plan must be read-only: %+v", plan)
	}
	kinds := map[skiffgc.ActionKind]bool{}
	protected := false
	for _, action := range plan.Actions {
		kinds[action.Kind] = true
		if action.Protected && action.RequiresSnapshot {
			protected = true
		}
	}
	for _, kind := range []skiffgc.ActionKind{skiffgc.ActionExpireRelease, skiffgc.ActionArchiveOperation, skiffgc.ActionProtectStateful} {
		if !kinds[kind] {
			t.Fatalf("missing action kind %s in %+v", kind, plan.Actions)
		}
	}
	if !protected {
		t.Fatalf("stateful resource was not protected: %+v", plan.Actions)
	}
}

func TestGCApplyAuditsCleanupAndSkipsProtectedActions(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedGCState(t, store)
	now := time.Date(2026, 5, 17, 3, 0, 0, 0, time.UTC)
	planner := skiffgc.Planner{Store: store, Clock: func() time.Time { return now }}
	plan, err := planner.Plan(ctx, skiffgc.PlanRequest{Service: "payments-api", Env: "prod", Retention: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	result, err := planner.Apply(ctx, *plan, skiffgc.ApplyRequest{
		Actor:      schema.Actor{ID: "alice", Type: "user"},
		TraceID:    "tr_gc",
		ApprovalID: "approval_01JGC",
		Yes:        true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) == 0 || len(result.Audits) != len(result.Applied) {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Skipped) == 0 {
		t.Fatalf("expected protected stateful action to be skipped")
	}
	objects, err := store.List(ctx, "audit/2026-05-17/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != len(result.Applied) {
		t.Fatalf("audit objects = %d, applied = %d", len(objects), len(result.Applied))
	}
}

func seedGCState(t *testing.T, store objstore.ObjectStore) {
	t.Helper()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_current",
		StableRelease:  "rel_stable",
		UpdatedAt:      "2026-05-16T00:00:00Z",
		UpdatedBy:      schema.Actor{ID: "seed", Type: "test"},
	})
	createJSON(t, store, "services/payments-api/releases/rel_old/release.json", schema.ReleaseManifest{
		SchemaVersion: schema.Version,
		Service:       "payments-api",
		Env:           "prod",
		ReleaseID:     "rel_old",
		CreatedAt:     "2026-05-14T00:00:00Z",
	})
	createJSON(t, store, "services/payments-api/operations/op_done/control.json", schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   "op_done",
		Service:       "payments-api",
		Env:           "prod",
		Status:        schema.OperationSucceeded,
		UpdatedAt:     "2026-05-14T00:00:00Z",
	})
	createJSON(t, store, "resources/by-provider/aws/rds-db-instance/db-123.json", schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "rds-db-instance", Name: "payments-db"},
		Provider:      schema.ResourceProviderRef{Provider: "aws", Kind: "rds-db-instance", ID: "db-123"},
		Service:       "payments-api",
		Env:           "prod",
		ObservedAt:    "2026-05-14T00:00:00Z",
	})
}

func createJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}
