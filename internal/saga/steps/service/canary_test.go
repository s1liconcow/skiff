package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCanaryStageWritesDesiredBeforeStartingRollout(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedCanaryService(t, store, "rel_old", "rel_old")
	rollouts := &fakeCanaryRollouts{}
	step := Stage{Store: store, Provider: rollouts, Clock: canaryTestNow}
	result, err := step.Run(ctx, canaryStepRequest(rawJSON(map[string]any{
		"service":       "payments-api",
		"env":           "prod",
		"operation_id":  "op_canary",
		"release_id":    "rel_new",
		"stage_percent": 25,
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != steps.StatusSucceeded || len(result.ProviderOperations) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(rollouts.calls) != 1 {
		t.Fatalf("rollout calls = %d, want 1", len(rollouts.calls))
	}
	call := rollouts.calls[0]
	if call.ReleaseID != "rel_new" || call.MinHealthyPercentage != 75 {
		t.Fatalf("unexpected rollout call: %+v", call)
	}
	control, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	if control.Control.DesiredRelease != "rel_new" || control.Control.Operation == nil || control.Control.Operation.Step != "stage-25" {
		t.Fatalf("desired release was not written before rollout: %+v", control.Control)
	}
	var body struct {
		StagePercent int    `json:"stage_percent"`
		NextAction   string `json:"next_action"`
	}
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body.StagePercent != 25 || body.NextAction != "bake" {
		t.Fatalf("unexpected result body: %+v", body)
	}
}

func TestCanaryStageCompensationRevertsDesiredToStableRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedCanaryService(t, store, "rel_new", "rel_stable")
	rollouts := &fakeCanaryRollouts{}
	step := Stage{Store: store, Provider: rollouts, Clock: canaryTestNow}
	req := canaryStepRequest(rawJSON(map[string]any{
		"service":       "payments-api",
		"env":           "prod",
		"operation_id":  "op_canary",
		"release_id":    "rel_new",
		"stage_percent": 50,
	}))
	result, err := step.Compensate(ctx, req, schema.StepResult{Status: string(steps.StatusSucceeded)})
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	if result.Status != steps.StatusSucceeded {
		t.Fatalf("unexpected result: %+v", result)
	}
	control, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	if control.Control.DesiredRelease != "rel_stable" || control.Control.Operation == nil || control.Control.Operation.Kind != "canary-rollback" {
		t.Fatalf("desired release was not reverted: %+v", control.Control)
	}
	if len(rollouts.calls) != 1 || rollouts.calls[0].ReleaseID != "rel_stable" {
		t.Fatalf("rollback rollout not started with stable release: %+v", rollouts.calls)
	}
}

func canaryStepRequest(params json.RawMessage) steps.StepRequest {
	return steps.StepRequest{
		SagaID:  "saga_canary",
		TraceID: "tr_canary",
		Intent: schema.SagaIntent{
			SagaID:  "saga_canary",
			Kind:    "deployment.canary",
			Actor:   schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID: "tr_canary",
			Target:  schema.Target{Kind: "service", Name: "payments-api"},
		},
		Node: schema.SagaNode{
			ID:     "start-25",
			Kind:   KindCanaryStage,
			Params: params,
		},
	}
}

func seedCanaryService(t *testing.T, store *memory.Store, desired, stable string) {
	t.Helper()
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(canaryTestNow()), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatalf("create service control: %v", err)
	}
}

func canaryTestNow() time.Time {
	return time.Date(2026, 5, 17, 2, 30, 0, 0, time.UTC)
}

type fakeCanaryRollouts struct {
	calls []provider.RolloutRequest
}

func (f *fakeCanaryRollouts) Name() string { return "aws" }

func (f *fakeCanaryRollouts) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	f.calls = append(f.calls, req)
	return &provider.Rollout{
		ID:         req.OperationID,
		Provider:   "aws",
		Service:    req.Service,
		Env:        req.Env,
		ProviderID: "ir-" + req.ReleaseID,
		StartedAt:  canaryTestNow(),
	}, nil
}
