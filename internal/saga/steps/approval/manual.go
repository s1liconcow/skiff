package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindManual       = "approval.manual"
	KindChangeWindow = "approval.change_window"

	DecisionApprove = "approved"
	DecisionReject  = "rejected"
)

type Manual struct {
	Binary string
}

type ChangeWindow struct {
	Binary string
}

type DecisionRequest struct {
	SagaID  string       `json:"saga_id"`
	StepID  string       `json:"step_id"`
	Actor   schema.Actor `json:"actor"`
	TraceID string       `json:"trace_id,omitempty"`
	Reason  string       `json:"reason,omitempty"`
	Binary  string       `json:"binary,omitempty"`
}

type DecisionResult struct {
	OK             bool               `json:"ok"`
	SagaID         string             `json:"saga_id"`
	StepID         string             `json:"step"`
	State          string             `json:"state"`
	Decision       string             `json:"decision"`
	Risk           schema.Risk        `json:"risk,omitempty"`
	Facts          []string           `json:"facts,omitempty"`
	ApproveCommand string             `json:"approve_command,omitempty"`
	RejectCommand  string             `json:"reject_command,omitempty"`
	TraceID        string             `json:"trace_id,omitempty"`
	Control        schema.SagaControl `json:"control"`
}

type manualParams struct {
	Summary string      `json:"summary,omitempty"`
	Risk    schema.Risk `json:"risk,omitempty"`
	Facts   []string    `json:"facts,omitempty"`
}

type changeWindowParams struct {
	Summary  string   `json:"summary,omitempty"`
	Window   string   `json:"window,omitempty"`
	Timezone string   `json:"timezone,omitempty"`
	Facts    []string `json:"facts,omitempty"`
}

type approvalState struct {
	State          string        `json:"state"`
	Step           string        `json:"step"`
	Risk           schema.Risk   `json:"risk,omitempty"`
	Facts          []string      `json:"facts,omitempty"`
	Summary        string        `json:"summary,omitempty"`
	ApproveCommand string        `json:"approve_command,omitempty"`
	RejectCommand  string        `json:"reject_command,omitempty"`
	Decision       string        `json:"decision,omitempty"`
	Reason         string        `json:"reason,omitempty"`
	Actor          *schema.Actor `json:"actor,omitempty"`
}

func (s Manual) Kind() string { return KindManual }

func (s Manual) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeParams[manualParams](params)
	return err
}

func (s Manual) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	params, err := decodeParams[manualParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	return &steps.StepPlan{Summary: firstNonEmpty(params.Summary, "wait for manual approval"), Risk: riskDefault(params.Risk), Reversibility: schema.Reversible}, nil
}

func (s Manual) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams[manualParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	state := waitingState(req.SagaID, req.Node.ID, riskDefault(params.Risk), params.Facts, firstNonEmpty(params.Summary, "manual approval required"), firstNonEmpty(s.Binary, "skiff"))
	return &steps.StepResult{Status: steps.StatusWaiting, Result: rawJSON(state), Summary: state.Summary}, nil
}

func (s Manual) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Manual) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "manual approval has no compensation"})}, nil
}

func (s Manual) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s ChangeWindow) Kind() string { return KindChangeWindow }

func (s ChangeWindow) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeParams[changeWindowParams](params)
	return err
}

func (s ChangeWindow) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "wait for configured change window capability", Risk: schema.RiskMedium, Reversibility: schema.Reversible}, nil
}

func (s ChangeWindow) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams[changeWindowParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"state":      "waiting_for_change_window",
		"step":       req.Node.ID,
		"capability": "TODO",
		"summary":    firstNonEmpty(params.Summary, "change window approval capability is not implemented yet"),
		"window":     params.Window,
		"timezone":   params.Timezone,
		"facts":      params.Facts,
	}
	return &steps.StepResult{Status: steps.StatusWaiting, Result: rawJSON(result), Summary: result["summary"].(string)}, nil
}

func (s ChangeWindow) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ChangeWindow) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "change window check has no compensation"})}, nil
}

func (s ChangeWindow) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func Approve(ctx context.Context, store *sagastate.Store, req DecisionRequest) (*DecisionResult, error) {
	return decide(ctx, store, req, DecisionApprove)
}

func Reject(ctx context.Context, store *sagastate.Store, req DecisionRequest) (*DecisionResult, error) {
	return decide(ctx, store, req, DecisionReject)
}

func decide(ctx context.Context, store *sagastate.Store, req DecisionRequest, decision string) (*DecisionResult, error) {
	if store == nil {
		return nil, errors.New("saga store is required")
	}
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.StepID = strings.TrimSpace(req.StepID)
	if req.SagaID == "" || req.StepID == "" {
		return nil, errors.New("saga and step are required")
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff-cli", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		current, err := store.GetControl(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
		waiting, ok := findStep(current.Control.StepResults, req.StepID)
		if !ok || waiting.Status != string(steps.StatusWaiting) {
			return nil, fmt.Errorf("saga %s step %s is not waiting for approval", req.SagaID, req.StepID)
		}
		now := time.Now().UTC()
		waitingState := decodeApprovalState(waiting.Result)
		resultState := approvalState{
			State:          decision,
			Step:           req.StepID,
			Risk:           waitingState.Risk,
			Facts:          append([]string(nil), waitingState.Facts...),
			Summary:        firstNonEmpty(req.Reason, waitingState.Summary),
			ApproveCommand: waitingState.ApproveCommand,
			RejectCommand:  waitingState.RejectCommand,
			Decision:       decision,
			Reason:         req.Reason,
			Actor:          &req.Actor,
		}
		next := current.Control
		next.TraceID = firstNonEmpty(req.TraceID, next.TraceID)
		next.CurrentSteps = removeStep(next.CurrentSteps, req.StepID)
		if decision == DecisionApprove {
			next.Status = schema.SagaRunning
			next.StepResults = upsertStepResult(next.StepResults, schema.StepResultRef{
				StepID:      req.StepID,
				Kind:        firstNonEmpty(waiting.Kind, KindManual),
				Status:      string(steps.StatusSucceeded),
				Result:      rawJSON(resultState),
				CompletedAt: canonical.Time(now),
			})
		} else {
			next.Status = schema.SagaFailed
			next.CurrentSteps = nil
			next.StepResults = upsertStepResult(next.StepResults, schema.StepResultRef{
				StepID: req.StepID,
				Kind:   firstNonEmpty(waiting.Kind, KindManual),
				Status: string(steps.StatusFailed),
				Result: rawJSON(resultState),
				Failure: &schema.StepFailure{
					Code:    "APPROVAL_REJECTED",
					Summary: firstNonEmpty(req.Reason, "manual approval rejected"),
				},
				CompletedAt: canonical.Time(now),
			})
		}
		updated, err := store.UpdateControlCAS(ctx, current, next)
		if err != nil {
			lastErr = err
			continue
		}
		_ = appendDecisionEvent(ctx, store, req, decision, resultState, now)
		return &DecisionResult{
			OK:             true,
			SagaID:         req.SagaID,
			StepID:         req.StepID,
			State:          resultState.State,
			Decision:       decision,
			Risk:           resultState.Risk,
			Facts:          resultState.Facts,
			ApproveCommand: resultState.ApproveCommand,
			RejectCommand:  resultState.RejectCommand,
			TraceID:        req.TraceID,
			Control:        updated.Control,
		}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("approval decision CAS did not complete")
	}
	return nil, lastErr
}

func appendDecisionEvent(ctx context.Context, store *sagastate.Store, req DecisionRequest, decision string, state approvalState, now time.Time) error {
	severity := "info"
	if decision == DecisionReject {
		severity = "error"
	}
	_, err := store.AppendEvent(ctx, req.SagaID, schema.Event{
		SchemaVersion: schema.Version,
		ID:            "evt_" + events.NewID(now, req.SagaID+req.StepID+decision),
		Time:          canonical.Time(now),
		TraceID:       req.TraceID,
		Subject:       schema.Target{Kind: "saga", Name: req.SagaID},
		Type:          "saga.approval." + decision,
		Severity:      severity,
		Actor:         &req.Actor,
		Summary:       firstNonEmpty(req.Reason, state.Summary, "manual approval "+decision),
		Facts: []schema.Fact{
			{Type: "step", Message: req.StepID},
			{Type: "decision", Message: decision},
		},
		Data: rawJSON(state),
	})
	return err
}

func waitingState(sagaID, stepID string, risk schema.Risk, facts []string, summary, binary string) approvalState {
	return approvalState{
		State:          "waiting_for_approval",
		Step:           stepID,
		Risk:           risk,
		Facts:          append([]string(nil), facts...),
		Summary:        summary,
		ApproveCommand: fmt.Sprintf("%s ops approve %s --step %s --format json", binary, sagaID, stepID),
		RejectCommand:  fmt.Sprintf("%s ops reject %s --step %s --format json", binary, sagaID, stepID),
	}
}

func decodeApprovalState(body json.RawMessage) approvalState {
	var state approvalState
	_ = json.Unmarshal(body, &state)
	return state
}

func decodeParams[T any](body json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(body)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func findStep(refs []schema.StepResultRef, stepID string) (schema.StepResultRef, bool) {
	for _, ref := range refs {
		if ref.StepID == stepID {
			return ref, true
		}
	}
	return schema.StepResultRef{}, false
}

func upsertStepResult(refs []schema.StepResultRef, next schema.StepResultRef) []schema.StepResultRef {
	out := refs[:0]
	for _, ref := range refs {
		if ref.StepID != next.StepID {
			out = append(out, ref)
		}
	}
	return append(out, next)
}

func removeStep(steps []string, stepID string) []string {
	out := steps[:0]
	for _, step := range steps {
		if step != stepID {
			out = append(out, step)
		}
	}
	return out
}

func riskDefault(risk schema.Risk) schema.Risk {
	if risk == "" {
		return schema.RiskMedium
	}
	return risk
}

func rawJSON(value any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
