package plugins

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

func SagaSteps(host *Host) map[string]steps.Step {
	out := map[string]steps.Step{}
	if host == nil || host.Registry == nil {
		return out
	}
	for _, plugin := range host.Registry.Hooks(pluginapi.HookSagaStep) {
		allowed := stringSet(plugin.Manifest.Permissions.SagaStepKinds)
		for _, capability := range plugin.Manifest.Capabilities {
			if capability.Kind != pluginapi.CapabilitySagaStep {
				continue
			}
			for _, kind := range capability.SagaStepKinds {
				if !allowed[kind] {
					continue
				}
				out[kind] = pluginSagaStep{host: host, plugin: plugin, kind: kind}
			}
		}
	}
	return out
}

type pluginSagaStep struct {
	host   *Host
	plugin Plugin
	kind   string
}

func (s pluginSagaStep) Kind() string {
	return s.kind
}

func (s pluginSagaStep) ValidateParams(ctx context.Context, params json.RawMessage) error {
	return ctx.Err()
}

func (s pluginSagaStep) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	var response pluginapi.SagaStepPlanResponse
	if err := s.run(ctx, "plan", req, &response); err != nil {
		return nil, err
	}
	return &steps.StepPlan{
		Summary:       response.Summary,
		Risk:          schema.Risk(response.Risk),
		Reversibility: schema.Reversibility(response.Reversibility),
	}, nil
}

func (s pluginSagaStep) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	var response pluginapi.SagaStepResultResponse
	if err := s.run(ctx, "run", req, &response); err != nil {
		return nil, err
	}
	return sagaStepResult(response), nil
}

func (s pluginSagaStep) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	var response pluginapi.SagaStepResultResponse
	if err := s.run(ctx, "resume", req, &response); err != nil {
		return nil, err
	}
	return sagaStepResult(response), nil
}

func (s pluginSagaStep) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	var response pluginapi.SagaStepResultResponse
	if err := s.run(ctx, "compensate", req, &response); err != nil {
		return nil, err
	}
	return sagaStepResult(response), nil
}

func (s pluginSagaStep) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	var response struct {
		Findings    []steps.Finding        `json:"findings,omitempty"`
		Diagnostics []pluginapi.Diagnostic `json:"diagnostics,omitempty"`
	}
	if err := s.run(ctx, "doctor", req, &response); err != nil {
		return nil, err
	}
	return response.Findings, nil
}

func (s pluginSagaStep) run(ctx context.Context, phase string, req steps.StepRequest, response any) error {
	if s.host == nil || s.host.Runner == nil {
		return fmt.Errorf("plugin host is required")
	}
	previous := make(map[string]json.RawMessage, len(req.PreviousResults))
	for key, result := range req.PreviousResults {
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		previous[key] = body
	}
	return s.host.Runner.RunPluginHook(ctx, s.plugin, pluginapi.HookSagaStep, pluginapi.SagaStepRequest{
		Manifest:        s.plugin.Manifest,
		Phase:           phase,
		Kind:            s.kind,
		SagaID:          req.SagaID,
		StepID:          req.Node.ID,
		Params:          req.Node.Params,
		TraceID:         req.TraceID,
		PreviousResults: previous,
	}, response)
}

func sagaStepResult(response pluginapi.SagaStepResultResponse) *steps.StepResult {
	result := &steps.StepResult{
		Status:  steps.Status(response.Status),
		Result:  append(json.RawMessage(nil), response.Result...),
		Summary: response.Summary,
	}
	if result.Status == "" {
		result.Status = steps.StatusSucceeded
	}
	if response.Failure != nil {
		result.Failure = &schema.StepFailure{
			Code:       response.Failure.Code,
			Summary:    response.Failure.Summary,
			Cause:      response.Failure.Cause,
			Retriable:  response.Failure.Retriable,
			RetryAfter: response.Failure.RetryAfter,
		}
	}
	for _, op := range response.ProviderOperations {
		result.ProviderOperations = append(result.ProviderOperations, schema.ProviderOperationRef{
			Provider:    op.Provider,
			Kind:        op.Kind,
			ID:          op.ID,
			ObservedAt:  op.ObservedAt,
			Description: op.Description,
		})
	}
	return result
}
