package steps

import (
	"context"
	"encoding/json"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusWaiting   Status = "waiting"
	StatusRunning   Status = "running"
)

type Step interface {
	Kind() string
	ValidateParams(ctx context.Context, params json.RawMessage) error
	Plan(ctx context.Context, req StepRequest) (*StepPlan, error)
	Run(ctx context.Context, req StepRequest) (*StepResult, error)
	Resume(ctx context.Context, req StepRequest) (*StepResult, error)
	Compensate(ctx context.Context, req StepRequest, result schema.StepResult) (*StepResult, error)
	Doctor(ctx context.Context, req StepRequest) ([]Finding, error)
}

type StepRequest struct {
	SagaID          string
	Intent          schema.SagaIntent
	Graph           schema.SagaGraph
	Control         schema.SagaControl
	Node            schema.SagaNode
	TraceID         string
	PreviousResults map[string]schema.StepResult
}

type StepPlan struct {
	Summary       string               `json:"summary,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
}

type StepResult struct {
	Status             Status                        `json:"status"`
	Result             json.RawMessage               `json:"result,omitempty"`
	Failure            *schema.StepFailure           `json:"failure,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	Summary            string                        `json:"summary,omitempty"`
}

type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Summary  string `json:"summary"`
}
