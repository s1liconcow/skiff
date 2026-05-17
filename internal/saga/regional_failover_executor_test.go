package saga_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRegionalFailoverExecutesWithFakeProviderAfterApproval(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createReq, err := templates.RegionalFailover(templates.RegionalFailoverRequest{
		SagaID:        "saga_regional_failover",
		OperationID:   "op_regional_failover",
		Stack:         "orders",
		Service:       "orders",
		Database:      "orders-db",
		Env:           "prod",
		FromRegion:    "us-west-2",
		ToRegion:      "us-east-1",
		MaxReplicaLag: "30s",
		FreezeWrites:  true,
		Actor:         schema.Actor{ID: "operator", Type: "user"},
		TraceID:       "tr_regional_failover",
		CreatedAt:     time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RegionalFailover: %v", err)
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	cloud := fakeprovider.New(fakeprovider.WithStateStore(store), fakeprovider.WithClock(func() time.Time {
		return time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC)
	}))
	eventSeq := 0
	executor := sagastate.Executor{
		Store: sagas,
		Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Metrics: cloud, Binary: "skiff"}),
		Owner: "test-regional-failover",
		EventID: func() string {
			eventSeq++
			return fmt.Sprintf("evt_regional_%02d", eventSeq)
		},
	}
	first, err := executor.Execute(ctx, "saga_regional_failover")
	if err != nil {
		t.Fatalf("execute until approval: %v", err)
	}
	if first.Status != schema.SagaRunning || len(first.WaitingSteps) != 1 || first.WaitingSteps[0] != "approve-failover" {
		t.Fatalf("first execution = %+v", first)
	}
	if _, err := approval.Approve(ctx, sagas, approval.DecisionRequest{
		SagaID: "saga_regional_failover",
		StepID: "approve-failover",
		Actor:  schema.Actor{ID: "operator", Type: "user"},
		Reason: "secondary region verified",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	second, err := executor.Execute(ctx, "saga_regional_failover")
	if err != nil {
		t.Fatalf("execute after approval: %v", err)
	}
	if second.Status != schema.SagaSucceeded {
		t.Fatalf("second execution = %+v", second)
	}
	inspect, err := sagas.Inspect(ctx, "saga_regional_failover")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, ref := range inspect.Control.StepResults {
		if ref.StepID == "writes-after-promotion-boundary" && ref.Status == "succeeded" {
			return
		}
	}
	t.Fatalf("irreversible boundary step did not execute: %+v", inspect.Control.StepResults)
}
