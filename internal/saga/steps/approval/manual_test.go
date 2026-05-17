package approval_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestManualApprovalWaitsThenApproveAllowsSagaToComplete(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := seedApprovalSaga(t, store, "saga_approval")
	executor := sagastate.Executor{
		Store:   sagas,
		Steps:   map[string]steps.Step{approval.KindManual: approval.Manual{Binary: "skiff"}},
		Owner:   "test-executor",
		EventID: sequentialEventID(),
	}
	first, err := executor.Execute(ctx, "saga_approval")
	if err != nil {
		t.Fatalf("execute waiting saga: %v", err)
	}
	if first.Status != schema.SagaRunning || len(first.WaitingSteps) != 1 || first.WaitingSteps[0] != "approval-before-cutover" {
		t.Fatalf("unexpected waiting execution: %+v", first)
	}
	control, err := sagas.GetControl(ctx, "saga_approval")
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Control.StepResults) != 1 || control.Control.StepResults[0].Status != "waiting" {
		t.Fatalf("approval did not persist waiting result: %+v", control.Control.StepResults)
	}
	var waiting struct {
		State          string   `json:"state"`
		ApproveCommand string   `json:"approve_command"`
		RejectCommand  string   `json:"reject_command"`
		Facts          []string `json:"facts"`
	}
	if err := json.Unmarshal(control.Control.StepResults[0].Result, &waiting); err != nil {
		t.Fatalf("decode waiting result: %v", err)
	}
	if waiting.State != "waiting_for_approval" || waiting.ApproveCommand == "" || waiting.RejectCommand == "" || len(waiting.Facts) != 1 {
		t.Fatalf("unexpected waiting payload: %+v", waiting)
	}

	decision, err := approval.Approve(ctx, sagas, approval.DecisionRequest{
		SagaID: "saga_approval",
		StepID: "approval-before-cutover",
		Actor:  schema.Actor{ID: "operator", Type: "user"},
		Reason: "shadow service healthy",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !decision.OK || decision.Decision != approval.DecisionApprove || decision.Control.StepResults[0].Status != "succeeded" {
		t.Fatalf("unexpected approval result: %+v", decision)
	}
	second, err := executor.Execute(ctx, "saga_approval")
	if err != nil {
		t.Fatalf("execute approved saga: %v", err)
	}
	if second.Status != schema.SagaSucceeded {
		t.Fatalf("status = %s, want succeeded: %+v", second.Status, second)
	}
}

func TestManualApprovalRejectMarksSagaFailed(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := seedApprovalSaga(t, store, "saga_reject")
	executor := sagastate.Executor{
		Store:   sagas,
		Steps:   map[string]steps.Step{approval.KindManual: approval.Manual{Binary: "skiff"}},
		Owner:   "test-executor",
		EventID: sequentialEventID(),
	}
	if _, err := executor.Execute(ctx, "saga_reject"); err != nil {
		t.Fatalf("execute waiting saga: %v", err)
	}
	decision, err := approval.Reject(ctx, sagas, approval.DecisionRequest{
		SagaID: "saga_reject",
		StepID: "approval-before-cutover",
		Actor:  schema.Actor{ID: "operator", Type: "user"},
		Reason: "change window closed",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if decision.Control.Status != schema.SagaFailed || decision.Control.StepResults[0].Status != "failed" || decision.Control.StepResults[0].Failure == nil {
		t.Fatalf("unexpected rejection result: %+v", decision)
	}
}

func TestChangeWindowStepReturnsExplicitTodoWaitingState(t *testing.T) {
	step := approval.ChangeWindow{}
	body, _ := json.Marshal(map[string]any{"window": "Sat 10:00-12:00 UTC"})
	result, err := step.Run(context.Background(), approvalStepRequest("change-window", approval.KindChangeWindow, body))
	if err != nil {
		t.Fatalf("change window run: %v", err)
	}
	if result.Status != "waiting" {
		t.Fatalf("status = %s, want waiting", result.Status)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatalf("decode change window result: %v", err)
	}
	if payload["capability"] != "TODO" {
		t.Fatalf("change window payload missing TODO capability: %+v", payload)
	}
}

func seedApprovalSaga(t *testing.T, store *memory.Store, sagaID string) *sagastate.Store {
	t.Helper()
	sagas := sagastate.NewStore(store, sagastate.WithClock(approvalTestNow))
	params, _ := json.Marshal(map[string]any{
		"summary": "approve before cutover",
		"risk":    "high",
		"facts":   []string{"shadow service healthy"},
	})
	_, err := sagas.Create(context.Background(), sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        sagaID,
			Kind:          "test.approval",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_approval",
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
			Summary:       "approval test",
			CreatedAt:     canonical.Time(approvalTestNow()),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        sagaID,
			Nodes: []schema.SagaNode{{
				ID:            "approval-before-cutover",
				Kind:          approval.KindManual,
				Params:        params,
				Risk:          schema.RiskHigh,
				Reversibility: schema.Reversible,
			}},
			CreatedAt: canonical.Time(approvalTestNow()),
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        sagaID,
			Status:        schema.SagaPending,
			UpdatedAt:     canonical.Time(approvalTestNow()),
			TraceID:       "tr_approval",
		},
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}
	return sagas
}

func approvalStepRequest(id, kind string, params json.RawMessage) steps.StepRequest {
	return steps.StepRequest{
		SagaID:  "saga_change_window",
		TraceID: "tr_change_window",
		Intent: schema.SagaIntent{
			SagaID:  "saga_change_window",
			Target:  schema.Target{Kind: "service", Name: "payments-api"},
			Actor:   schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID: "tr_change_window",
		},
		Node: schema.SagaNode{ID: id, Kind: kind, Params: params},
	}
}

func sequentialEventID() func() string {
	next := 0
	return func() string {
		next++
		return "evt_approval_" + string(rune('a'+next))
	}
}

func approvalTestNow() time.Time {
	return time.Date(2026, 5, 17, 1, 0, 0, 0, time.UTC)
}
