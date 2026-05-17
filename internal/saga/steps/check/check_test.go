package check_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/saga/steps/check"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestPreflightChecksObjectStateProviderIdentityAndServiceControl(t *testing.T) {
	store := memory.New()
	seedCheckServiceControl(t, store)
	step := check.Preflight{Store: store, Provider: fakeProviderIdentity{name: "aws"}}
	result, err := step.Run(context.Background(), stepRequest("preflight", check.KindPreflight, map[string]any{"service": "payments-api", "env": "prod"}))
	if err != nil {
		t.Fatalf("preflight run: %v", err)
	}
	if result.Status != steps.StatusSucceeded {
		t.Fatalf("status = %s, want succeeded: %+v", result.Status, result.Failure)
	}

	missing := check.Preflight{Store: store, Provider: fakeProviderIdentity{name: "aws"}}
	result, err = missing.Run(context.Background(), stepRequest("preflight", check.KindPreflight, map[string]any{"service": "missing-api"}))
	if err != nil {
		t.Fatalf("missing preflight run: %v", err)
	}
	if result.Status != steps.StatusFailed || result.Failure == nil || result.Failure.Code != "PREFLIGHT_SERVICE_CONTROL_MISSING" {
		t.Fatalf("unexpected missing service result: %+v", result)
	}
}

func TestServiceAndTargetHealthChecksFailOnUnhealthyProviderStatus(t *testing.T) {
	provider := &fakeInspector{
		serviceInspection: &provider.ServiceInspection{
			Provider: "aws",
			Resources: []provider.ResourceInspection{{
				Kind:       "autoscaling-group",
				ProviderID: "asg-123",
				Status:     "unhealthy",
			}},
		},
		resourceInspection: &provider.ResourceInspection{
			Kind:       "target-group",
			ProviderID: "tg-123",
			Status:     "healthy",
		},
	}
	serviceResult, err := check.ServiceHealthy{Provider: provider}.Run(context.Background(), stepRequest("service-health", check.KindServiceHealth, map[string]any{"service": "payments-api"}))
	if err != nil {
		t.Fatalf("service health run: %v", err)
	}
	if serviceResult.Status != steps.StatusFailed || serviceResult.Failure == nil || serviceResult.Failure.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unexpected service health result: %+v", serviceResult)
	}
	targetResult, err := check.TargetHealth{Provider: provider}.Run(context.Background(), stepRequest("target-health", check.KindTargetHealth, map[string]any{"service": "payments-api"}))
	if err != nil {
		t.Fatalf("target health run: %v", err)
	}
	if targetResult.Status != steps.StatusSucceeded {
		t.Fatalf("target health status = %s, want succeeded: %+v", targetResult.Status, targetResult)
	}
}

func TestMetricsGateComparesLatestObservedValue(t *testing.T) {
	client := fakeMetricsClient{result: &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name: "errors",
		Points: []provider.MetricPoint{
			{Timestamp: checkTestNow().Add(-time.Minute), Value: 2},
			{Timestamp: checkTestNow(), Value: 3},
		},
	}}}}
	params := map[string]any{"service": "payments-api", "metric": "errors", "comparator": "<=", "threshold": 5}
	result, err := check.MetricsGate{Client: client}.Run(context.Background(), stepRequest("metrics", check.KindMetricsGate, params))
	if err != nil {
		t.Fatalf("metrics gate run: %v", err)
	}
	if result.Status != steps.StatusSucceeded {
		t.Fatalf("metrics gate status = %s, want succeeded: %+v", result.Status, result)
	}
	params["threshold"] = 1
	result, err = (check.MetricsGate{Client: client}).Run(context.Background(), stepRequest("metrics", check.KindMetricsGate, params))
	if err != nil {
		t.Fatalf("metrics gate failed run: %v", err)
	}
	if result.Status != steps.StatusFailed || result.Failure == nil || result.Failure.Code != "METRIC_GATE_FAILED" {
		t.Fatalf("unexpected failed metrics result: %+v", result)
	}
}

type fakeProviderIdentity struct {
	name string
}

func (p fakeProviderIdentity) Name() string {
	return p.name
}

type fakeInspector struct {
	serviceInspection  *provider.ServiceInspection
	resourceInspection *provider.ResourceInspection
}

func (p *fakeInspector) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return p.serviceInspection, nil
}

func (p *fakeInspector) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return p.resourceInspection, nil
}

type fakeMetricsClient struct {
	result *provider.MetricsResult
}

func (c fakeMetricsClient) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return c.result, nil
}

func stepRequest(id, kind string, params map[string]any) steps.StepRequest {
	body, _ := json.Marshal(params)
	return steps.StepRequest{
		SagaID:  "saga_checks",
		TraceID: "tr_checks",
		Intent: schema.SagaIntent{
			SagaID:  "saga_checks",
			Target:  schema.Target{Kind: "service", Name: "payments-api"},
			Actor:   schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID: "tr_checks",
		},
		Node: schema.SagaNode{ID: id, Kind: kind, Params: body},
	}
}

func seedCheckServiceControl(t *testing.T, store *memory.Store) {
	t.Helper()
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(checkTestNow()), schema.Actor{ID: "seed", Type: "agent"})
	control.DesiredRelease = "rel_02"
	control.StableRelease = "rel_01"
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatal(err)
	}
}

func checkTestNow() time.Time {
	return time.Date(2026, 5, 17, 0, 45, 0, 0, time.UTC)
}
