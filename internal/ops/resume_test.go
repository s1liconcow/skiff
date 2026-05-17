package ops_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestResumeOperationContinuesStoredASGRollout(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createOperationIntent(t, store, "payments-api", "prod", "op_resume", "rollback", nil)
	createOperationControl(t, store, "payments-api", "prod", "op_resume", schema.OperationRunning, []schema.ProviderOperationRef{{
		Provider: aws.Name,
		Kind:     aws.RolloutKindASGInstanceRefresh,
		ID:       "ir-123",
	}})
	createServiceControl(t, store, "payments-api", "prod", "rel_new", "rel_old", "op_resume")
	cloud := &fakeProvider{rolloutStatus: &provider.RolloutStatus{
		RolloutID:  "op_resume",
		Status:     "succeeded",
		ProviderID: "ir-123",
		UpdatedAt:  testNow(),
	}}

	result, err := (ops.Resumer{Store: store, Provider: cloud, Clock: testNow}).Resume(ctx, ops.ResumeRequest{
		Service:     "payments-api",
		OperationID: "op_resume",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_resume",
		Owner:       "agent-one",
	})
	if err != nil {
		t.Fatalf("resume operation: %v", err)
	}
	if !result.Resumed || result.Status != schema.OperationSucceeded {
		t.Fatalf("unexpected result: %+v", result)
	}
	if cloud.watchReq.ProviderID != "ir-123" {
		t.Fatalf("provider id = %q, want ir-123", cloud.watchReq.ProviderID)
	}
	control := readOperationControl(t, store, "payments-api", "op_resume")
	if control.Status != schema.OperationSucceeded || control.Lease != nil {
		t.Fatalf("operation control was not completed and released: %+v", control)
	}
	service := readServiceControl(t, store, "payments-api")
	if service.StableRelease != "rel_new" || service.DesiredRelease != "rel_new" {
		t.Fatalf("service control was not marked stable: %+v", service)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: testNow})
	if err != nil {
		t.Fatal(err)
	}
	items, err := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: "payments-api", Operation: "op_resume"}, events.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEventType(items, "operation.resume.started") || !hasEventType(items, "rollout.succeeded") || !hasEventType(items, "operation.resume.completed") {
		t.Fatalf("resume events missing: %+v", items)
	}
}

func createOperationIntent(t *testing.T, store objstore.ObjectStore, service, env, operationID, kind string, params any) {
	t.Helper()
	intent := schema.NewOperationIntent(operationID, service, env, kind, schema.Target{Kind: "service", Name: service}, schema.Actor{ID: "agent-one", Type: "agent"}, "tr_resume", canonical.Time(testNow()))
	intent.Risk = schema.RiskMedium
	intent.Reversibility = schema.Compensatable
	intent.Summary = kind + " " + service
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		intent.Params = body
	}
	createJSON(t, store, mustOperationIntentKey(t, service, operationID), intent)
}

func createOperationControl(t *testing.T, store objstore.ObjectStore, service, env, operationID string, status schema.OperationStatus, refs []schema.ProviderOperationRef) {
	t.Helper()
	control := schema.OperationControl{
		SchemaVersion:      schema.Version,
		OperationID:        operationID,
		Service:            service,
		Env:                env,
		Status:             status,
		ProviderOperations: refs,
		UpdatedAt:          canonical.Time(testNow()),
		TraceID:            "tr_resume",
	}
	createJSON(t, store, mustOperationControlKey(t, service, operationID), control)
}

func createServiceControl(t *testing.T, store objstore.ObjectStore, service, env, desired, stable, operationID string) {
	t.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(testNow()), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	control.Operation = &schema.ActiveOperation{ID: operationID, Kind: "rollback", State: string(schema.OperationRunning)}
	createJSON(t, store, mustServiceControlKey(t, service), control)
}

func readOperationControl(t *testing.T, store objstore.ObjectStore, service, operationID string) schema.OperationControl {
	t.Helper()
	obj, err := store.Get(context.Background(), mustOperationControlKey(t, service, operationID))
	if err != nil {
		t.Fatal(err)
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		t.Fatal(err)
	}
	return control
}

func readServiceControl(t *testing.T, store objstore.ObjectStore, service string) schema.ServiceControl {
	t.Helper()
	obj, err := store.Get(context.Background(), mustServiceControlKey(t, service))
	if err != nil {
		t.Fatal(err)
	}
	var control schema.ServiceControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		t.Fatal(err)
	}
	return control
}

func createJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
}

func mustOperationIntentKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustOperationControlKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustServiceControlKey(t *testing.T, service string) string {
	t.Helper()
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func hasEventType(items []events.Event, eventType string) bool {
	for _, item := range items {
		if item.Type == eventType {
			return true
		}
	}
	return false
}

type fakeProvider struct {
	rolloutStatus *provider.RolloutStatus
	watchReq      provider.WatchRolloutRequest
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{}, nil
}

func (p *fakeProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{}, nil
}

func (p *fakeProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-service")
}

func (p *fakeProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-resource")
}

func (p *fakeProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return nil, provider.Unsupported("fake", "logs")
}

func (p *fakeProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return nil, provider.Unsupported("fake", "metrics")
}

func (p *fakeProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return nil, provider.Unsupported("fake", "debug")
}

func (p *fakeProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "start-rollout")
}

func (p *fakeProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	p.watchReq = req
	return p.rolloutStatus, nil
}

func (p *fakeProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "rollback")
}

func testNow() time.Time {
	return time.Date(2026, 5, 17, 1, 45, 0, 0, time.UTC)
}
