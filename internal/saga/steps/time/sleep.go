package time

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdtime "time"

	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const KindSleep = "time.sleep"

type Sleep struct {
	Clock func() stdtime.Time
}

type sleepParams struct {
	Service      string `json:"service,omitempty"`
	Env          string `json:"env,omitempty"`
	StagePercent int    `json:"stage_percent,omitempty"`
	Duration     string `json:"duration"`
}

type sleepResult struct {
	OK           bool   `json:"ok"`
	State        string `json:"state,omitempty"`
	Service      string `json:"service,omitempty"`
	Env          string `json:"env,omitempty"`
	StagePercent int    `json:"stage_percent,omitempty"`
	Duration     string `json:"duration,omitempty"`
	ResumeAfter  string `json:"resume_after,omitempty"`
	NextAction   string `json:"next_action,omitempty"`
	Command      string `json:"command,omitempty"`
}

func (s Sleep) Kind() string { return KindSleep }

func (s Sleep) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	_, err = parseDuration(decoded.Duration)
	return err
}

func (s Sleep) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "wait for the canary bake period before evaluating gates", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s Sleep) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	duration, err := parseDuration(params.Duration)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return s.succeeded(params, "canary bake skipped"), nil
	}
	now := s.now()
	if waiting, ok := waitingResult(req.Control, req.Node.ID); ok {
		resumeAfter, err := stdtime.Parse(stdtime.RFC3339Nano, waiting.ResumeAfter)
		if err != nil {
			return nil, fmt.Errorf("waiting resume_after is invalid: %w", err)
		}
		if !now.Before(resumeAfter) {
			return s.succeeded(params, "canary bake completed"), nil
		}
		return s.waiting(params, req.SagaID, req.Node.ID, duration, resumeAfter), nil
	}
	return s.waiting(params, req.SagaID, req.Node.ID, duration, now.Add(duration)), nil
}

func (s Sleep) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Sleep) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "sleep step has no compensation"})}, nil
}

func (s Sleep) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s Sleep) waiting(params sleepParams, sagaID, stepID string, duration stdtime.Duration, resumeAfter stdtime.Time) *steps.StepResult {
	result := sleepResult{
		OK:           true,
		State:        "baking",
		Service:      params.Service,
		Env:          params.Env,
		StagePercent: params.StagePercent,
		Duration:     duration.String(),
		ResumeAfter:  canonical.Time(resumeAfter),
		NextAction:   "resume_after_bake",
		Command:      fmt.Sprintf("skiff ops resume %s --step %s --format json", sagaID, stepID),
	}
	return &steps.StepResult{Status: steps.StatusWaiting, Result: rawJSON(result), Summary: fmt.Sprintf("waiting %s for canary bake", duration)}
}

func (s Sleep) succeeded(params sleepParams, summary string) *steps.StepResult {
	result := sleepResult{
		OK:           true,
		State:        "bake_complete",
		Service:      params.Service,
		Env:          params.Env,
		StagePercent: params.StagePercent,
		Duration:     params.Duration,
		NextAction:   "run_gates",
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(result), Summary: summary}
}

func (s Sleep) now() stdtime.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return stdtime.Now().UTC()
}

func waitingResult(control schema.SagaControl, stepID string) (sleepResult, bool) {
	for _, ref := range control.StepResults {
		if ref.StepID != stepID || ref.Status != string(steps.StatusWaiting) {
			continue
		}
		var out sleepResult
		if err := json.Unmarshal(ref.Result, &out); err != nil {
			return sleepResult{}, false
		}
		return out, true
	}
	return sleepResult{}, false
}

func decodeParams(body json.RawMessage) (sleepParams, error) {
	var out sleepParams
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

func parseDuration(value string) (stdtime.Duration, error) {
	if value == "" {
		return 0, errors.New("sleep duration is required")
	}
	duration, err := stdtime.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("sleep duration is invalid: %w", err)
	}
	return duration, nil
}

func rawJSON(value any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}
