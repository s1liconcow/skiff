package saga_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCanaryMetricsGateFailureTriggersRollbackCompensation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(canaryExecutorNow()), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = "rel_old"
	control.StableRelease = "rel_old"
	if _, err := state.NewClient(store).CreateServiceControl(ctx, control); err != nil {
		t.Fatalf("create service control: %v", err)
	}
	createReq, err := templates.DeploymentCanary(templates.CanaryRequest{
		SagaID:       "saga_canary_fail",
		OperationID:  "op_canary_fail",
		Service:      "payments-api",
		Env:          "prod",
		ReleaseID:    "rel_new",
		Stages:       []templates.CanaryStage{{Percent: 50}, {Percent: 100}},
		BakeDuration: "0s",
		MetricGates:  []templates.MetricGate{{Metric: "aws.elb.http_5xx_count", Comparator: "<=", Threshold: 0}},
		Actor:        schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:      "tr_canary_fail",
		CreatedAt:    canaryExecutorNow(),
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	sagas := sagastate.NewStore(store, sagastate.WithClock(canaryExecutorNow))
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	cloud := &failingMetricsCanaryProvider{}
	eventSeq := 0
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Metrics: cloud, Binary: "skiff"}),
		Owner: "test-canary",
		EventID: func() string {
			eventSeq++
			return fmt.Sprintf("evt_%02d", eventSeq)
		},
	}).Execute(ctx, "saga_canary_fail")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "metrics-gate-50-1" {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	if len(result.Compensated) != 1 || result.Compensated[0] != "start-50" {
		t.Fatalf("expected start-50 compensation, got %+v", result.Compensated)
	}
	if len(cloud.rollouts) != 2 || cloud.rollouts[0].ReleaseID != "rel_new" || cloud.rollouts[1].ReleaseID != "rel_old" {
		t.Fatalf("expected canary rollout then rollback rollout, got %+v", cloud.rollouts)
	}
	updated, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	if updated.Control.DesiredRelease != "rel_old" || updated.Control.Operation == nil || updated.Control.Operation.Kind != "canary-rollback" {
		t.Fatalf("rollback compensation did not restore desired release: %+v", updated.Control)
	}
}

func canaryExecutorNow() time.Time {
	return time.Date(2026, 5, 17, 3, 45, 0, 0, time.UTC)
}

type failingMetricsCanaryProvider struct {
	rollouts []provider.RolloutRequest
}

func (p *failingMetricsCanaryProvider) Name() string { return "aws" }

func (p *failingMetricsCanaryProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{Provider: "aws", Service: graph.Service, Env: graph.Env}, nil
}

func (p *failingMetricsCanaryProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{Provider: "aws", Service: plan.Service, Env: plan.Env, AppliedAt: time.Now().UTC()}, nil
}

func (p *failingMetricsCanaryProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return &provider.ServiceInspection{Ref: ref, Provider: "aws", FreshAt: time.Now().UTC()}, nil
}

func (p *failingMetricsCanaryProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return &provider.ResourceInspection{Kind: ref.Kind, LogicalID: ref.LogicalID, ProviderID: "tg-123", Status: "healthy"}, nil
}

func (p *failingMetricsCanaryProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return &provider.LogsResult{}, nil
}

func (p *failingMetricsCanaryProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	name := "aws.elb.http_5xx_count"
	if len(req.Names) > 0 {
		name = req.Names[0]
	}
	return &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name:   name,
		Source: "fake",
		Points: []provider.MetricPoint{{Timestamp: time.Now().UTC(), Value: 5}},
	}}}, nil
}

func (p *failingMetricsCanaryProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return &provider.DebugSession{ID: "debug-1", Provider: "aws", StartedAt: time.Now().UTC()}, nil
}

func (p *failingMetricsCanaryProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	p.rollouts = append(p.rollouts, req)
	return &provider.Rollout{ID: req.OperationID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "ir-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}

func (p *failingMetricsCanaryProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return &provider.RolloutStatus{RolloutID: req.RolloutID, Status: "succeeded", ProviderID: req.ProviderID, UpdatedAt: time.Now().UTC()}, nil
}

func (p *failingMetricsCanaryProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: "rollback-" + req.ReleaseID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "rb-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}
