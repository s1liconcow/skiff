package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	defaultLeaseDuration = 30 * time.Second
	defaultRetryBackoff  = time.Millisecond
)

type Executor struct {
	Store         *Store
	Steps         map[string]steps.Step
	Owner         string
	LeaseDuration time.Duration
	Sleep         func(context.Context, time.Duration) error
	EventID       func() string
}

type ExecutionResult struct {
	SagaID       string            `json:"saga_id"`
	Status       schema.SagaStatus `json:"status"`
	Completed    []string          `json:"completed,omitempty"`
	FailedStep   string            `json:"failed_step,omitempty"`
	Compensated  []string          `json:"compensated,omitempty"`
	WaitingSteps []string          `json:"waiting_steps,omitempty"`
}

func (e *Executor) Execute(ctx context.Context, sagaID string) (*ExecutionResult, error) {
	if e == nil || e.Store == nil {
		return nil, errors.New("saga executor store is required")
	}
	if e.Owner == "" {
		e.Owner = "saga-executor"
	}
	if e.LeaseDuration <= 0 {
		e.LeaseDuration = defaultLeaseDuration
	}
	if e.Sleep == nil {
		e.Sleep = sleepContext
	}
	handle, control, err := e.Store.AcquireLease(ctx, sagaID, LeaseOptions{Owner: e.Owner, Duration: e.LeaseDuration})
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = e.Store.ReleaseLease(context.Background(), *handle)
	}()

	intent, err := e.Store.GetIntent(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	graph, err := e.Store.GetGraph(ctx, sagaID)
	if err != nil {
		return nil, err
	}
	if control.Control.Status == schema.SagaSucceeded || control.Control.Status == schema.SagaCanceled {
		return executionSummary(control.Control, nil, nil, ""), nil
	}
	if control.Control.Status == schema.SagaPending {
		nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
			control.Status = schema.SagaRunning
			return nil
		})
		if err != nil {
			return nil, err
		}
		handle, control = nextHandle, nextControl
	}

	completed := completedResults(control.Control)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ready, err := ReadyNodes(graph.Graph, completed)
		if err != nil {
			return nil, err
		}
		if len(ready) == 0 {
			if len(completed) == len(graph.Graph.Nodes) {
				nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
					control.Status = schema.SagaSucceeded
					control.CurrentSteps = nil
					return nil
				})
				if err != nil {
					return nil, err
				}
				handle, control = nextHandle, nextControl
			}
			return executionSummary(control.Control, completed, nil, ""), nil
		}
		for _, node := range ready {
			nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
				control.Status = schema.SagaRunning
				control.CurrentSteps = []string{node.ID}
				return nil
			})
			if err != nil {
				return nil, err
			}
			handle, control = nextHandle, nextControl

			stepResult, err := e.runNodeWithRetry(ctx, intent.Intent, graph.Graph, control.Control, node, completed)
			if err != nil {
				stepResult = failedStepResult(node, err)
			}
			persisted, err := e.persistStepResult(ctx, sagaID, node, stepResult)
			if err != nil {
				return nil, err
			}
			if _, err := e.appendStepEvent(ctx, sagaID, node, persisted); err != nil {
				return nil, err
			}
			switch persisted.Status {
			case string(steps.StatusSucceeded):
				completed[node.ID] = persisted
				nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
					control.StepResults = upsertStepResultRef(control.StepResults, stepResultRef(persisted))
					control.CurrentSteps = nil
					return nil
				})
				if err != nil {
					return nil, err
				}
				handle, control = nextHandle, nextControl
			case string(steps.StatusWaiting):
				nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
					control.StepResults = upsertStepResultRef(control.StepResults, stepResultRef(persisted))
					control.CurrentSteps = []string{node.ID}
					return nil
				})
				if err != nil {
					return nil, err
				}
				handle, control = nextHandle, nextControl
				_ = handle
				return executionSummary(control.Control, completed, nil, ""), nil
			default:
				compHandle, _, compensated, compErr := e.compensate(ctx, *handle, intent.Intent, graph.Graph, control.Control, completed)
				if compErr != nil {
					return nil, compErr
				}
				handle = compHandle
				nextHandle, nextControl, err := e.updateControl(ctx, *handle, func(control *schema.SagaControl) error {
					control.Status = schema.SagaFailed
					control.StepResults = upsertStepResultRef(control.StepResults, stepResultRef(persisted))
					control.CurrentSteps = nil
					return nil
				})
				if err != nil {
					return nil, err
				}
				handle = nextHandle
				return executionSummary(nextControl.Control, completed, compensated, node.ID), nil
			}
		}
	}
}

func (e *Executor) runNodeWithRetry(ctx context.Context, intent schema.SagaIntent, graph schema.SagaGraph, control schema.SagaControl, node schema.SagaNode, completed map[string]schema.StepResult) (*steps.StepResult, error) {
	step := e.Steps[node.Kind]
	if step == nil {
		return nil, fmt.Errorf("no saga step registered for kind %q", node.Kind)
	}
	if err := step.ValidateParams(ctx, node.Params); err != nil {
		return nil, err
	}
	req := steps.StepRequest{
		SagaID:          intent.SagaID,
		Intent:          intent,
		Graph:           graph,
		Control:         control,
		Node:            node,
		TraceID:         intent.TraceID,
		PreviousResults: cloneResults(completed),
	}
	attempts := nodeAttempts(node)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := step.Run(ctx, req)
		if err == nil {
			if result == nil {
				return nil, errors.New("saga step returned nil result")
			}
			if result.Status == "" {
				result.Status = steps.StatusSucceeded
			}
			return result, nil
		}
		lastErr = err
		if attempt < attempts {
			if err := e.Sleep(ctx, retryBackoff(node, attempt)); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (e *Executor) compensate(ctx context.Context, handle LeaseHandle, intent schema.SagaIntent, graph schema.SagaGraph, control schema.SagaControl, completed map[string]schema.StepResult) (*LeaseHandle, *ControlDocument, []string, error) {
	order, err := ReverseTopologicalCompleted(graph, completed)
	if err != nil {
		return nil, nil, nil, err
	}
	compensated := make([]string, 0)
	var latest *ControlDocument
	for _, node := range order {
		if node.Compensate == nil || node.Reversibility == schema.Irreversible {
			continue
		}
		step := e.Steps[node.Kind]
		if step == nil {
			continue
		}
		result := completed[node.ID]
		req := steps.StepRequest{
			SagaID:          intent.SagaID,
			Intent:          intent,
			Graph:           graph,
			Control:         control,
			Node:            node,
			TraceID:         intent.TraceID,
			PreviousResults: cloneResults(completed),
		}
		stepResult, err := step.Compensate(ctx, req, result)
		if err != nil {
			stepResult = &steps.StepResult{
				Status: steps.StatusFailed,
				Failure: &schema.StepFailure{
					Code:    "COMPENSATION_FAILED",
					Summary: err.Error(),
				},
			}
		}
		compStepID := node.ID + "-compensate"
		persisted, err := e.persistStepResult(ctx, intent.SagaID, schema.SagaNode{ID: compStepID, Kind: node.Compensate.Kind}, stepResult)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := e.appendStepEvent(ctx, intent.SagaID, schema.SagaNode{ID: compStepID, Kind: node.Compensate.Kind}, persisted); err != nil {
			return nil, nil, nil, err
		}
		nextHandle, doc, err := e.updateControl(ctx, handle, func(control *schema.SagaControl) error {
			control.Status = schema.SagaCompensating
			control.StepResults = upsertStepResultRef(control.StepResults, stepResultRef(persisted))
			control.CurrentSteps = []string{compStepID}
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
		handle = *nextHandle
		latest = doc
		if persisted.Status == string(steps.StatusSucceeded) {
			compensated = append(compensated, node.ID)
		}
	}
	return &handle, latest, compensated, nil
}

func (e *Executor) persistStepResult(ctx context.Context, sagaID string, node schema.SagaNode, result *steps.StepResult) (schema.StepResult, error) {
	if result == nil {
		result = &steps.StepResult{Status: steps.StatusSucceeded}
	}
	status := string(result.Status)
	if status == "" {
		status = string(steps.StatusSucceeded)
	}
	now := canonical.Time(e.Store.clock().UTC())
	persisted := schema.StepResult{
		SchemaVersion:      schema.Version,
		SagaID:             sagaID,
		StepID:             node.ID,
		Kind:               node.Kind,
		Status:             status,
		Result:             cloneRaw(result.Result),
		Failure:            result.Failure,
		ProviderOperations: append([]schema.ProviderOperationRef(nil), result.ProviderOperations...),
		CompletedAt:        now,
	}
	_, err := e.Store.CreateStepResult(ctx, persisted)
	if err != nil {
		if errors.Is(err, objstore.ErrAlreadyExists) {
			existing, getErr := e.Store.GetStepResult(ctx, sagaID, node.ID)
			if getErr != nil {
				return schema.StepResult{}, getErr
			}
			return existing.Result, nil
		}
		return schema.StepResult{}, err
	}
	return persisted, nil
}

func (e *Executor) appendStepEvent(ctx context.Context, sagaID string, node schema.SagaNode, result schema.StepResult) (*EventDocument, error) {
	eventID := ""
	if e.EventID != nil {
		eventID = e.EventID()
	}
	if eventID == "" {
		eventID = fmt.Sprintf("evt_%d", e.Store.clock().UTC().UnixNano())
	}
	event := schema.Event{
		SchemaVersion: schema.Version,
		ID:            eventID,
		Time:          canonical.Time(e.Store.clock().UTC()),
		TraceID:       "",
		Subject:       schema.Target{Kind: "saga", Name: sagaID},
		Type:          "saga.step." + result.Status,
		Severity:      eventSeverity(result.Status),
		Summary:       fmt.Sprintf("%s %s", node.ID, result.Status),
		Facts: []schema.Fact{
			{Type: "step", Message: node.ID},
			{Type: "kind", Message: node.Kind},
			{Type: "status", Message: result.Status},
		},
	}
	return e.Store.AppendEvent(ctx, sagaID, event)
}

func (e *Executor) updateControl(ctx context.Context, handle LeaseHandle, mutate func(*schema.SagaControl) error) (*LeaseHandle, *ControlDocument, error) {
	nextHandle, doc, err := e.Store.UpdateControlWithLeaseCAS(ctx, handle, mutate)
	if err != nil {
		if errors.Is(err, state.ErrPreconditionFailed) || errors.Is(err, state.ErrLeaseLost) {
			return nil, nil, err
		}
		return nil, nil, err
	}
	return nextHandle, doc, nil
}

func completedResults(control schema.SagaControl) map[string]schema.StepResult {
	out := make(map[string]schema.StepResult)
	for _, ref := range control.StepResults {
		if ref.Status == string(steps.StatusSucceeded) {
			out[ref.StepID] = schema.StepResult{
				SchemaVersion: schema.Version,
				SagaID:        control.SagaID,
				StepID:        ref.StepID,
				Kind:          ref.Kind,
				Status:        ref.Status,
				Result:        cloneRaw(ref.Result),
				Failure:       ref.Failure,
				CompletedAt:   ref.CompletedAt,
			}
		}
	}
	return out
}

func upsertStepResultRef(refs []schema.StepResultRef, next schema.StepResultRef) []schema.StepResultRef {
	out := refs[:0]
	for _, ref := range refs {
		if ref.StepID != next.StepID {
			out = append(out, ref)
		}
	}
	out = append(out, next)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StepID < out[j].StepID })
	return out
}

func stepResultRef(result schema.StepResult) schema.StepResultRef {
	return schema.StepResultRef{
		StepID:      result.StepID,
		Kind:        result.Kind,
		Status:      result.Status,
		ResultRef:   fmt.Sprintf("sagas/%s/artifacts/results/%s.json", result.SagaID, result.StepID),
		Result:      cloneRaw(result.Result),
		Failure:     result.Failure,
		CompletedAt: result.CompletedAt,
	}
}

func failedStepResult(node schema.SagaNode, err error) *steps.StepResult {
	return &steps.StepResult{
		Status: steps.StatusFailed,
		Failure: &schema.StepFailure{
			Code:    "STEP_FAILED",
			Summary: err.Error(),
		},
		Summary: fmt.Sprintf("%s failed", node.ID),
	}
}

func executionSummary(control schema.SagaControl, completed map[string]schema.StepResult, compensated []string, failed string) *ExecutionResult {
	done := make([]string, 0, len(completed))
	for id := range completed {
		done = append(done, id)
	}
	sort.Strings(done)
	return &ExecutionResult{
		SagaID:       control.SagaID,
		Status:       control.Status,
		Completed:    done,
		FailedStep:   failed,
		Compensated:  compensated,
		WaitingSteps: append([]string(nil), control.CurrentSteps...),
	}
}

func cloneResults(in map[string]schema.StepResult) map[string]schema.StepResult {
	out := make(map[string]schema.StepResult, len(in))
	for key, value := range in {
		value.Result = cloneRaw(value.Result)
		out[key] = value
	}
	return out
}

func cloneRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func nodeAttempts(node schema.SagaNode) int {
	if node.Retry == nil || node.Retry.MaxAttempts <= 0 {
		return 1
	}
	return node.Retry.MaxAttempts
}

func retryBackoff(node schema.SagaNode, attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	delay := defaultRetryBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func eventSeverity(status string) string {
	switch status {
	case string(steps.StatusFailed):
		return "error"
	case string(steps.StatusWaiting):
		return "warn"
	default:
		return "info"
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
