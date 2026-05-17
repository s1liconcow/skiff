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

func TestTrafficCutoverExecutesWithFakeProviderAfterApproval(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createReq, err := templates.TrafficCutover(templates.TrafficCutoverRequest{
		SagaID:      "saga_traffic_cutover",
		OperationID: "op_traffic_cutover",
		Service:     "payments-api",
		Env:         "prod",
		From:        "kube",
		To:          "skiff",
		Percent:     25,
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		TraceID:     "tr_traffic_cutover",
		CreatedAt:   time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("TrafficCutover: %v", err)
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	cloud := fakeprovider.New(fakeprovider.WithStateStore(store), fakeprovider.WithClock(func() time.Time {
		return time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC)
	}))
	eventSeq := 0
	executor := sagastate.Executor{
		Store: sagas,
		Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Metrics: cloud, Binary: "skiff"}),
		Owner: "test-traffic-cutover",
		EventID: func() string {
			eventSeq++
			return fmt.Sprintf("evt_cutover_%02d", eventSeq)
		},
	}
	first, err := executor.Execute(ctx, "saga_traffic_cutover")
	if err != nil {
		t.Fatalf("execute until approval: %v", err)
	}
	if first.Status != schema.SagaRunning || len(first.WaitingSteps) != 1 || first.WaitingSteps[0] != "approve-cutover" {
		t.Fatalf("first execution = %+v", first)
	}
	if _, err := approval.Approve(ctx, sagas, approval.DecisionRequest{
		SagaID: "saga_traffic_cutover",
		StepID: "approve-cutover",
		Actor:  schema.Actor{ID: "operator", Type: "user"},
		Reason: "shadow service is healthy",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	second, err := executor.Execute(ctx, "saga_traffic_cutover")
	if err != nil {
		t.Fatalf("execute after approval: %v", err)
	}
	if second.Status != schema.SagaSucceeded {
		t.Fatalf("second execution = %+v", second)
	}
	shift, err := sagas.GetStepResult(ctx, "saga_traffic_cutover", "shift-traffic")
	if err != nil {
		t.Fatalf("get shift step: %v", err)
	}
	if shift.Result.Status != "succeeded" || len(shift.Result.ProviderOperations) != 1 || shift.Result.ProviderOperations[0].Kind != "traffic-shift" {
		t.Fatalf("shift step did not record provider operation: %+v", shift.Result)
	}
}
