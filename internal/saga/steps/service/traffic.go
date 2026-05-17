package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const KindTrafficShift = "service.traffic.shift"

type TrafficShift struct {
	Shifter provider.TrafficOperations
}

type trafficShiftParams struct {
	Service     string `json:"service"`
	Env         string `json:"env"`
	From        string `json:"from"`
	To          string `json:"to"`
	Percent     int    `json:"percent"`
	OperationID string `json:"operation_id,omitempty"`
}

func (s TrafficShift) Kind() string { return KindTrafficShift }

func (s TrafficShift) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeTrafficShiftParams(params)
	if err != nil {
		return err
	}
	return validateTrafficShiftParams(decoded)
}

func (s TrafficShift) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	params, err := decodeTrafficShiftParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	return &steps.StepPlan{
		Summary:       fmt.Sprintf("shift %d%% traffic from %s to %s", params.Percent, params.From, params.To),
		Risk:          schema.RiskMedium,
		Reversibility: schema.Compensatable,
	}, nil
}

func (s TrafficShift) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	if s.Shifter == nil {
		return trafficFailed("TRAFFIC_SHIFT_UNSUPPORTED", "provider does not support weighted traffic shift"), nil
	}
	params, err := decodeTrafficShiftParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := validateTrafficShiftParams(params); err != nil {
		return nil, err
	}
	result, err := s.Shifter.ShiftTraffic(ctx, provider.TrafficShiftRequest{
		Service:     params.Service,
		Env:         params.Env,
		From:        params.From,
		To:          params.To,
		Percent:     params.Percent,
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	return &steps.StepResult{
		Status:  steps.StatusSucceeded,
		Summary: fmt.Sprintf("shifted %d%% traffic to %s", params.Percent, params.To),
		Result:  rawJSON(result),
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider:    result.Provider,
			Kind:        "traffic-shift",
			ID:          result.ProviderID,
			ObservedAt:  canonicalTime(firstNonZero(result.UpdatedAt, time.Now().UTC())),
			Description: fmt.Sprintf("weighted traffic shift %d%% to %s", params.Percent, params.To),
		}},
	}, nil
}

func (s TrafficShift) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s TrafficShift) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := decodeTrafficShiftParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Shifter == nil {
		return trafficFailed("TRAFFIC_SHIFT_COMPENSATION_UNSUPPORTED", "provider does not support traffic compensation"), nil
	}
	compensated, err := s.Shifter.ShiftTraffic(ctx, provider.TrafficShiftRequest{
		Service:     params.Service,
		Env:         params.Env,
		From:        params.To,
		To:          params.From,
		Percent:     100,
		OperationID: params.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "traffic shift compensated", Result: rawJSON(compensated)}, nil
}

func (s TrafficShift) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func decodeTrafficShiftParams(body json.RawMessage) (trafficShiftParams, error) {
	var out trafficShiftParams
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	return out, nil
}

func validateTrafficShiftParams(params trafficShiftParams) error {
	switch {
	case params.Service == "":
		return errors.New("traffic shift service is required")
	case params.Env == "":
		return errors.New("traffic shift env is required")
	case params.From == "":
		return errors.New("traffic shift source is required")
	case params.To == "":
		return errors.New("traffic shift target is required")
	case params.Percent < 0 || params.Percent > 100:
		return errors.New("traffic shift percent must be between 0 and 100")
	default:
		return nil
	}
}

func trafficFailed(code, summary string) *steps.StepResult {
	return &steps.StepResult{
		Status:  steps.StatusFailed,
		Summary: summary,
		Failure: &schema.StepFailure{
			Code:    code,
			Summary: summary,
		},
	}
}

func canonicalTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func firstNonZero(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
