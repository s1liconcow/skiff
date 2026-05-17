package time

import (
	"context"
	"encoding/json"
	"testing"
	stdtime "time"

	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSleepWaitsUntilResumeAfter(t *testing.T) {
	now := stdtime.Date(2026, 5, 17, 3, 0, 0, 0, stdtime.UTC)
	step := Sleep{Clock: func() stdtime.Time { return now }}
	req := sleepRequest(rawJSON(map[string]any{
		"service":       "payments-api",
		"env":           "prod",
		"stage_percent": 10,
		"duration":      "30s",
	}))
	result, err := step.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != steps.StatusWaiting {
		t.Fatalf("status = %s, want waiting", result.Status)
	}
	var body sleepResult
	if err := json.Unmarshal(result.Result, &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if body.NextAction != "resume_after_bake" || body.ResumeAfter != "2026-05-17T03:00:30Z" {
		t.Fatalf("unexpected waiting result: %+v", body)
	}

	now = now.Add(31 * stdtime.Second)
	req.Control.StepResults = []schema.StepResultRef{{
		StepID: "bake-10",
		Kind:   KindSleep,
		Status: string(steps.StatusWaiting),
		Result: result.Result,
	}}
	result, err = step.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if result.Status != steps.StatusSucceeded {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
}

func TestSleepZeroDurationSucceedsImmediately(t *testing.T) {
	step := Sleep{}
	result, err := step.Run(context.Background(), sleepRequest(rawJSON(map[string]any{"duration": "0s", "stage_percent": 100})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != steps.StatusSucceeded {
		t.Fatalf("status = %s, want succeeded", result.Status)
	}
}

func sleepRequest(params json.RawMessage) steps.StepRequest {
	return steps.StepRequest{
		SagaID: "saga_canary",
		Node: schema.SagaNode{
			ID:     "bake-10",
			Kind:   KindSleep,
			Params: params,
		},
	}
}
