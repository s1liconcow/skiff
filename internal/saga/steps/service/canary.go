package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindCanaryStage      = "service.canary.stage"
	KindCanaryMarkStable = "service.canary.mark_stable"
	KindCanaryRollback   = "service.canary.rollback"
)

type RolloutProvider interface {
	Name() string
	StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error)
}

type Stage struct {
	Store         objstore.ObjectStore
	Provider      RolloutProvider
	LeaseDuration time.Duration
	Clock         func() time.Time
}

type MarkStable struct {
	Store         objstore.ObjectStore
	Provider      RolloutProvider
	LeaseDuration time.Duration
	Clock         func() time.Time
}

type canaryParams struct {
	Service              string `json:"service"`
	Env                  string `json:"env"`
	OperationID          string `json:"operation_id,omitempty"`
	ReleaseID            string `json:"release_id"`
	StagePercent         int    `json:"stage_percent"`
	RollbackPolicy       string `json:"rollback_policy,omitempty"`
	MinHealthyPercentage int    `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int    `json:"instance_warmup,omitempty"`
	Mechanism            string `json:"mechanism,omitempty"`
}

func (s Stage) Kind() string { return KindCanaryStage }

func (s Stage) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return validateStageParams(decoded)
}

func (s Stage) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "write desired release, then start the provider rollout for one canary stage", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s Stage) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := validateStageParams(params); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("canary stage object store is required")
	}
	if s.Provider == nil {
		return nil, errors.New("canary stage provider is required")
	}
	if err := s.writeDesiredRelease(ctx, req, params); err != nil {
		return nil, err
	}
	_ = s.appendAudit(ctx, req, params, "canary_stage", fmt.Sprintf("started canary stage %d%% for %s", params.StagePercent, params.ReleaseID))
	rollout, err := s.Provider.StartRollout(ctx, provider.RolloutRequest{
		Service:              params.Service,
		Env:                  params.Env,
		ReleaseID:            params.ReleaseID,
		OperationID:          params.OperationID,
		MinHealthyPercentage: minHealthy(params),
		InstanceWarmup:       params.InstanceWarmup,
	})
	if err != nil {
		return nil, err
	}
	if rollout == nil {
		return nil, errors.New("canary stage provider returned no rollout")
	}
	ref := providerOperationRef(*rollout, s.now(), "canary stage rollout")
	result := map[string]any{
		"ok":                     true,
		"stage_percent":          params.StagePercent,
		"release_id":             params.ReleaseID,
		"mechanism":              firstNonEmpty(params.Mechanism, "aws-asg-instance-refresh-min-healthy"),
		"min_healthy_percentage": minHealthy(params),
		"provider":               rollout.Provider,
		"provider_id":            rollout.ProviderID,
		"rollout_id":             rollout.ID,
		"next_action":            "bake",
	}
	return &steps.StepResult{
		Status:             steps.StatusSucceeded,
		Result:             rawJSON(result),
		ProviderOperations: []schema.ProviderOperationRef{ref},
		Summary:            fmt.Sprintf("canary stage %d%% started", params.StagePercent),
	}, nil
}

func (s Stage) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Stage) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := validateStageParams(params); err != nil {
		return nil, err
	}
	target, err := s.rollbackTarget(ctx, params)
	if err != nil {
		return failed("CANARY_ROLLBACK_TARGET_MISSING", err.Error(), params), nil
	}
	if err := s.writeRollbackDesired(ctx, req, params, target); err != nil {
		return nil, err
	}
	_ = s.appendAudit(ctx, req, params, "canary_rollback", fmt.Sprintf("reverted desired release to %s after canary stage %d%%", target, params.StagePercent))
	var refs []schema.ProviderOperationRef
	var providerID string
	var rolloutID string
	if s.Provider != nil {
		rollout, err := s.Provider.StartRollout(ctx, provider.RolloutRequest{
			Service:              params.Service,
			Env:                  params.Env,
			ReleaseID:            target,
			OperationID:          firstNonEmpty(params.OperationID+"-rollback", params.OperationID),
			MinHealthyPercentage: 90,
			InstanceWarmup:       params.InstanceWarmup,
		})
		if err != nil {
			return nil, err
		}
		if rollout != nil {
			ref := providerOperationRef(*rollout, s.now(), "canary rollback rollout")
			refs = append(refs, ref)
			providerID = rollout.ProviderID
			rolloutID = rollout.ID
		}
	}
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":               true,
			"stage_percent":    params.StagePercent,
			"rollback_release": target,
			"provider_id":      providerID,
			"rollout_id":       rolloutID,
			"next_action":      "inspect_rollback",
			"command":          fmt.Sprintf("skiff rollback %s --to %s --format json", params.Service, target),
		}),
		ProviderOperations: refs,
		Summary:            fmt.Sprintf("canary stage %d%% compensated by rollback to %s", params.StagePercent, target),
	}, nil
}

func (s Stage) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s Stage) writeDesiredRelease(ctx context.Context, req steps.StepRequest, params canaryParams) error {
	client := state.NewClient(s.Store)
	handle, _, err := client.AcquireLease(ctx, params.Service, state.LeaseOptions{
		Owner:    "saga:" + req.SagaID,
		Duration: s.leaseDuration(),
		Actor:    req.Intent.Actor,
		TraceID:  req.TraceID,
	})
	if err != nil {
		return err
	}
	defer func() { _, _ = client.ReleaseLease(context.Background(), *handle) }()
	nextHandle, _, err := client.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = params.ReleaseID
		control.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "canary-deploy", State: string(schema.OperationRunning), Step: fmt.Sprintf("stage-%d", params.StagePercent)}
		control.UpdatedBy = req.Intent.Actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return err
	}
	handle = nextHandle
	return nil
}

func (s Stage) writeRollbackDesired(ctx context.Context, req steps.StepRequest, params canaryParams, target string) error {
	if s.Store == nil {
		return errors.New("canary rollback object store is required")
	}
	client := state.NewClient(s.Store)
	handle, _, err := client.AcquireLease(ctx, params.Service, state.LeaseOptions{
		Owner:    "saga:" + req.SagaID + ":compensate",
		Duration: s.leaseDuration(),
		Actor:    req.Intent.Actor,
		TraceID:  req.TraceID,
	})
	if err != nil {
		return err
	}
	defer func() { _, _ = client.ReleaseLease(context.Background(), *handle) }()
	nextHandle, _, err := client.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = target
		control.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "canary-rollback", State: string(schema.OperationRunning), Step: fmt.Sprintf("compensate-stage-%d", params.StagePercent)}
		control.UpdatedBy = req.Intent.Actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return err
	}
	handle = nextHandle
	return nil
}

func (s Stage) rollbackTarget(ctx context.Context, params canaryParams) (string, error) {
	if params.RollbackPolicy != "" && params.RollbackPolicy != "previous-stable" {
		return params.RollbackPolicy, nil
	}
	if s.Store == nil {
		return "", errors.New("object store is required to resolve previous-stable rollback target")
	}
	doc, err := state.NewClient(s.Store).GetServiceControl(ctx, params.Service)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Control.StableRelease) == "" {
		return "", errors.New("service control has no stable release for previous-stable rollback")
	}
	return doc.Control.StableRelease, nil
}

func (s Stage) appendAudit(ctx context.Context, req steps.StepRequest, params canaryParams, action, summary string) error {
	if s.Store == nil {
		return nil
	}
	log, err := events.NewLog(events.Options{Store: s.Store, Clock: s.now})
	if err != nil {
		return err
	}
	record := events.NewAuditRecord(req.Intent.Actor, schema.Target{Kind: "service", Name: params.Service}, action, summary, req.TraceID, s.now(), req.SagaID+action+fmt.Sprint(params.StagePercent))
	record.Risk = schema.RiskMedium
	record.Data = rawJSON(map[string]any{"operation_id": params.OperationID, "saga_id": req.SagaID, "release_id": params.ReleaseID, "stage_percent": params.StagePercent})
	_, err = log.AppendAudit(ctx, record)
	return err
}

func (s Stage) leaseDuration() time.Duration {
	if s.LeaseDuration <= 0 {
		return 5 * time.Minute
	}
	return s.LeaseDuration
}

func (s Stage) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s MarkStable) Kind() string { return KindCanaryMarkStable }

func (s MarkStable) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams(params)
	if err != nil {
		return err
	}
	return validateStageParams(decoded)
}

func (s MarkStable) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "mark the canary release stable in service control after all gates pass", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s MarkStable) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if err := validateStageParams(params); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("canary mark-stable object store is required")
	}
	client := state.NewClient(s.Store)
	handle, _, err := client.AcquireLease(ctx, params.Service, state.LeaseOptions{
		Owner:    "saga:" + req.SagaID + ":mark-stable",
		Duration: s.leaseDuration(),
		Actor:    req.Intent.Actor,
		TraceID:  req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = client.ReleaseLease(context.Background(), *handle) }()
	nextHandle, doc, err := client.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = params.ReleaseID
		control.StableRelease = params.ReleaseID
		control.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "canary-deploy", State: string(schema.OperationSucceeded), Step: "mark-stable"}
		control.UpdatedBy = req.Intent.Actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return nil, err
	}
	handle = nextHandle
	_ = s.appendAudit(ctx, req, params, "canary_mark_stable", "marked canary release stable")
	return &steps.StepResult{
		Status: steps.StatusSucceeded,
		Result: rawJSON(map[string]any{
			"ok":             true,
			"release_id":     params.ReleaseID,
			"stable_release": doc.Control.StableRelease,
			"next_action":    "complete",
		}),
		Summary: "canary release marked stable",
	}, nil
}

func (s MarkStable) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s MarkStable) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	stage := Stage{Store: s.Store, Provider: s.Provider, LeaseDuration: s.LeaseDuration, Clock: s.Clock}
	return stage.Compensate(ctx, req, result)
}

func (s MarkStable) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s MarkStable) appendAudit(ctx context.Context, req steps.StepRequest, params canaryParams, action, summary string) error {
	stage := Stage{Store: s.Store, Provider: s.Provider, LeaseDuration: s.LeaseDuration, Clock: s.Clock}
	return stage.appendAudit(ctx, req, params, action, summary)
}

func (s MarkStable) leaseDuration() time.Duration {
	if s.LeaseDuration <= 0 {
		return 5 * time.Minute
	}
	return s.LeaseDuration
}

func decodeParams(body json.RawMessage) (canaryParams, error) {
	var out canaryParams
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

func validateStageParams(params canaryParams) error {
	switch {
	case strings.TrimSpace(params.Service) == "":
		return errors.New("canary service is required")
	case strings.TrimSpace(params.Env) == "":
		return errors.New("canary env is required")
	case strings.TrimSpace(params.ReleaseID) == "":
		return errors.New("canary release ID is required")
	case params.StagePercent <= 0 || params.StagePercent > 100:
		return errors.New("canary stage percent must be between 1 and 100")
	}
	return nil
}

func minHealthy(params canaryParams) int {
	if params.MinHealthyPercentage > 0 {
		return params.MinHealthyPercentage
	}
	min := 100 - params.StagePercent
	if min < 1 {
		return 1
	}
	return min
}

func providerOperationRef(rollout provider.Rollout, observed time.Time, description string) schema.ProviderOperationRef {
	id := firstNonEmpty(rollout.ProviderID, rollout.ID)
	return schema.ProviderOperationRef{
		Provider:    rollout.Provider,
		Kind:        "asg-instance-refresh",
		ID:          id,
		ObservedAt:  canonical.Time(observed),
		Description: description,
	}
}

func failed(code, summary string, params canaryParams) *steps.StepResult {
	return &steps.StepResult{
		Status: steps.StatusFailed,
		Result: rawJSON(map[string]any{
			"ok":            false,
			"code":          code,
			"summary":       summary,
			"service":       params.Service,
			"release_id":    params.ReleaseID,
			"stage_percent": params.StagePercent,
		}),
		Failure: &schema.StepFailure{Code: code, Summary: summary},
		Summary: summary,
	}
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
