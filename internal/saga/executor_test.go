package saga

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestExecutorCompletesMultiStepGraph(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	req := sampleCreateRequest()
	req.Graph.Nodes = append(req.Graph.Nodes, schema.SagaNode{
		ID:            "metrics",
		Kind:          "check.metrics",
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	})
	req.Graph.Nodes[1].Requires = []string{"metrics", "preflight"}
	req.Graph.Edges = []schema.SagaEdge{{From: "preflight", To: "shift-traffic"}, {From: "metrics", To: "shift-traffic"}}
	if _, err := sagas.Create(ctx, req); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	events := sequenceIDs()
	executor := &Executor{
		Store: sagas,
		Steps: map[string]steps.Step{
			"check.preflight":   &fakeStep{kind: "check.preflight"},
			"check.metrics":     &fakeStep{kind: "check.metrics"},
			"aws.shift_traffic": &fakeStep{kind: "aws.shift_traffic"},
		},
		Owner:   "test-executor",
		Sleep:   noSleep,
		EventID: events.next,
	}
	result, err := executor.Execute(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != schema.SagaSucceeded {
		t.Fatalf("status = %s, want succeeded: %+v", result.Status, result)
	}
	control, err := sagas.GetControl(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("get control: %v", err)
	}
	if control.Control.Status != schema.SagaSucceeded || len(control.Control.StepResults) != 3 {
		t.Fatalf("unexpected final control: %+v", control.Control)
	}
	if _, err := sagas.GetStepResult(ctx, "saga_01JABC", "shift-traffic"); err != nil {
		t.Fatalf("missing step result: %v", err)
	}
}

func TestExecutorResumesFromCompletedControlResults(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	if _, err := sagas.Create(ctx, sampleCreateRequest()); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	current, err := sagas.GetControl(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("get control: %v", err)
	}
	preflight := schema.StepResult{
		SchemaVersion: schema.Version,
		SagaID:        "saga_01JABC",
		StepID:        "preflight",
		Kind:          "check.preflight",
		Status:        string(steps.StatusSucceeded),
		CompletedAt:   "2026-05-16T23:51:00Z",
	}
	if _, err := sagas.CreateStepResult(ctx, preflight); err != nil {
		t.Fatalf("create preflight result: %v", err)
	}
	next := current.Control
	next.Status = schema.SagaRunning
	next.StepResults = []schema.StepResultRef{stepResultRef(preflight)}
	if _, err := sagas.UpdateControlCAS(ctx, current, next); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	preflightStep := &fakeStep{kind: "check.preflight"}
	shiftStep := &fakeStep{kind: "aws.shift_traffic"}
	executor := &Executor{
		Store: sagas,
		Steps: map[string]steps.Step{
			"check.preflight":   preflightStep,
			"aws.shift_traffic": shiftStep,
		},
		Owner:   "resume-executor",
		Sleep:   noSleep,
		EventID: sequenceIDs().next,
	}
	result, err := executor.Execute(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != schema.SagaSucceeded {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
	if preflightStep.runCalls != 0 {
		t.Fatalf("preflight reran during resume")
	}
	if shiftStep.runCalls != 1 {
		t.Fatalf("shift step calls = %d, want 1", shiftStep.runCalls)
	}
}

func TestExecutorRetriesAndCompensatesInReverseTopologicalOrder(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	req := sampleCreateRequest()
	req.Graph.Nodes[0].Compensate = &schema.CompensationSpec{Kind: "check.preflight.compensate"}
	req.Graph.Nodes = append(req.Graph.Nodes, schema.SagaNode{
		ID:            "cutover",
		Kind:          "aws.cutover",
		Requires:      []string{"shift-traffic"},
		Retry:         &schema.RetryPolicy{MaxAttempts: 2},
		Risk:          schema.RiskHigh,
		Reversibility: schema.Compensatable,
	})
	req.Graph.Edges = append(req.Graph.Edges, schema.SagaEdge{From: "shift-traffic", To: "cutover"})
	if _, err := sagas.Create(ctx, req); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	var compensated []string
	cutover := &fakeStep{kind: "aws.cutover", failRuns: 2}
	shift := &fakeStep{kind: "aws.shift_traffic", compensate: func(req steps.StepRequest, result schema.StepResult) {
		compensated = append(compensated, req.Node.ID)
	}}
	preflight := &fakeStep{kind: "check.preflight", compensate: func(req steps.StepRequest, result schema.StepResult) {
		compensated = append(compensated, req.Node.ID)
	}}
	executor := &Executor{
		Store: sagas,
		Steps: map[string]steps.Step{
			"check.preflight":   preflight,
			"aws.shift_traffic": shift,
			"aws.cutover":       cutover,
		},
		Owner:   "compensate-executor",
		Sleep:   noSleep,
		EventID: sequenceIDs().next,
	}
	result, err := executor.Execute(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "cutover" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if cutover.runCalls != 2 {
		t.Fatalf("cutover calls = %d, want retry count 2", cutover.runCalls)
	}
	if !reflect.DeepEqual(compensated, []string{"shift-traffic", "preflight"}) {
		t.Fatalf("compensation order = %+v", compensated)
	}
}

func TestSagaLeaseRejectsStaleExecutor(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	if _, err := sagas.Create(ctx, sampleCreateRequest()); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	handle, current, err := sagas.AcquireLease(ctx, "saga_01JABC", LeaseOptions{Owner: "executor-a", Duration: time.Minute})
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	next := current.Control
	next.CurrentSteps = []string{"preflight"}
	if _, err := sagas.UpdateControlCAS(ctx, current, next); err != nil {
		t.Fatalf("external control update: %v", err)
	}
	_, _, err = sagas.UpdateControlWithLeaseCAS(ctx, *handle, func(control *schema.SagaControl) error {
		control.Status = schema.SagaRunning
		return nil
	})
	if !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stale lease update error = %v, want ErrPreconditionFailed", err)
	}
}

type fakeStep struct {
	kind       string
	runCalls   int
	failRuns   int
	compensate func(steps.StepRequest, schema.StepResult)
}

func (s *fakeStep) Kind() string { return s.kind }

func (s *fakeStep) ValidateParams(ctx context.Context, params json.RawMessage) error { return nil }

func (s *fakeStep) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{}, nil
}

func (s *fakeStep) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	s.runCalls++
	if s.runCalls <= s.failRuns {
		return nil, errors.New("planned failure")
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: json.RawMessage(`{"ok":true}`)}, nil
}

func (s *fakeStep) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s *fakeStep) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	if s.compensate != nil {
		s.compensate(req, result)
	}
	return &steps.StepResult{Status: steps.StatusSucceeded}, nil
}

func (s *fakeStep) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

type eventSequence struct {
	nextID int
}

func sequenceIDs() *eventSequence {
	return &eventSequence{}
}

func (s *eventSequence) next() string {
	s.nextID++
	return "evt_" + string(rune('A'+s.nextID))
}

func noSleep(ctx context.Context, delay time.Duration) error {
	return ctx.Err()
}
