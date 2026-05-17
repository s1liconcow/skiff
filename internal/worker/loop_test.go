package worker_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/internal/worker"
)

func TestWorkerIgnoresActiveOperationLease(t *testing.T) {
	store := memory.New()
	createWorkerOperation(t, store, workerOperationOptions{Lease: &schema.Lease{
		Owner:      "other-worker",
		Token:      "lease-active",
		Generation: 1,
		ExpiresAt:  canonical.Time(workerNow().Add(time.Hour)),
	}})
	cloud := &workerFakeProvider{rolloutStatus: &provider.RolloutStatus{Status: "succeeded", ProviderID: "ir-123", UpdatedAt: workerNow()}}

	result, err := (worker.Worker{Store: store, Provider: cloud, Owner: "worker-one", Clock: workerNow}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if result.OperationSkipped != 1 || cloud.watchCount() != 0 {
		t.Fatalf("active lease was not skipped: result=%+v watch=%d", result, cloud.watchCount())
	}
}

func TestWorkerTakesOverExpiredOperationLease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createWorkerOperation(t, store, workerOperationOptions{Lease: &schema.Lease{
		Owner:      "old-worker",
		Token:      "lease-expired",
		Generation: 1,
		ExpiresAt:  canonical.Time(workerNow().Add(-time.Minute)),
	}})
	cloud := &workerFakeProvider{rolloutStatus: &provider.RolloutStatus{Status: "rolling_out", ProviderID: "ir-123", UpdatedAt: workerNow()}}

	result, err := (worker.Worker{
		Store:         store,
		Provider:      cloud,
		Owner:         "worker-new",
		Actor:         schema.Actor{ID: "worker-new", Type: "agent"},
		LeaseDuration: time.Minute,
		Clock:         workerNow,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if result.OperationResumed != 1 || cloud.watchCount() != 1 {
		t.Fatalf("expired lease was not resumed: result=%+v watch=%d", result, cloud.watchCount())
	}
	items, err := eventsForOperation(ctx, store, "payments-api", "op_worker")
	if err != nil {
		t.Fatal(err)
	}
	if !hasWorkerEventType(items, "operation.takeover") {
		t.Fatalf("takeover event missing: %+v", items)
	}
	audits, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audits))
	}
}

func TestMultipleWorkersDoNotDoubleResumeOperation(t *testing.T) {
	store := memory.New()
	createWorkerOperation(t, store, workerOperationOptions{Lease: &schema.Lease{
		Owner:      "old-worker",
		Token:      "lease-expired",
		Generation: 1,
		ExpiresAt:  canonical.Time(workerNow().Add(-time.Minute)),
	}})
	entered := make(chan struct{})
	release := make(chan struct{})
	cloud := &workerFakeProvider{
		rolloutStatus: &provider.RolloutStatus{Status: "rolling_out", ProviderID: "ir-123", UpdatedAt: workerNow()},
		entered:       entered,
		release:       release,
	}
	first := worker.Worker{Store: store, Provider: cloud, Owner: "worker-one", LeaseDuration: time.Minute, Clock: workerNow}
	second := worker.Worker{Store: store, Provider: cloud, Owner: "worker-two", LeaseDuration: time.Minute, Clock: workerNow}

	errs := make(chan error, 2)
	go func() {
		_, err := first.RunOnce(context.Background())
		errs <- err
	}()
	<-entered
	go func() {
		_, err := second.RunOnce(context.Background())
		errs <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-errs; err != nil {
		t.Fatalf("first worker: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("second worker: %v", err)
	}
	if cloud.watchCount() != 1 {
		t.Fatalf("watch count = %d, want 1", cloud.watchCount())
	}
}

func TestWorkerResumesSagasThroughExecutor(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := sagastate.NewStore(store, sagastate.WithClock(workerNow))
	_, err := sagas.Create(ctx, sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_worker",
			Kind:          "test.saga",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_saga",
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
			Summary:       "test saga",
			CreatedAt:     canonical.Time(workerNow()),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_worker",
			Nodes:         []schema.SagaNode{{ID: "step-one", Kind: "test.step"}},
			CreatedAt:     canonical.Time(workerNow()),
		},
		Control: schema.NewSagaControl("saga_worker", schema.SagaPending, canonical.Time(workerNow())),
	})
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}
	step := &workerFakeStep{}
	result, err := (worker.Worker{
		Store:     store,
		Provider:  &workerFakeProvider{},
		SagaSteps: map[string]steps.Step{"test.step": step},
		Owner:     "worker-one",
		Clock:     workerNow,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if result.SagaResumed != 1 || step.count != 1 {
		t.Fatalf("saga was not resumed: result=%+v step=%d", result, step.count)
	}
	control, err := sagas.GetControl(ctx, "saga_worker")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.Status != schema.SagaSucceeded {
		t.Fatalf("saga status = %s, want succeeded", control.Control.Status)
	}
}

type workerOperationOptions struct {
	Lease *schema.Lease
}

func createWorkerOperation(t *testing.T, store objstore.ObjectStore, opts workerOperationOptions) {
	t.Helper()
	intent := schema.NewOperationIntent("op_worker", "payments-api", "prod", "rollback", schema.Target{Kind: "service", Name: "payments-api"}, schema.Actor{ID: "agent-one", Type: "agent"}, "tr_worker", canonical.Time(workerNow()))
	createWorkerJSON(t, store, mustWorkerOperationIntentKey(t, "payments-api", "op_worker"), intent)
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   "op_worker",
		Service:       "payments-api",
		Env:           "prod",
		Status:        schema.OperationRunning,
		Lease:         opts.Lease,
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider: aws.Name,
			Kind:     aws.RolloutKindASGInstanceRefresh,
			ID:       "ir-123",
		}},
		UpdatedAt: canonical.Time(workerNow()),
		TraceID:   "tr_worker",
	}
	createWorkerJSON(t, store, mustWorkerOperationControlKey(t, "payments-api", "op_worker"), control)
}

func eventsForOperation(ctx context.Context, store objstore.ObjectStore, service, operationID string) ([]schema.Event, error) {
	prefix, err := paths.OperationEventsPrefix(service, operationID)
	if err != nil {
		return nil, err
	}
	metas, err := store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	events := make([]schema.Event, 0, len(metas))
	for _, meta := range metas {
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, err
		}
		var event schema.Event
		_ = json.Unmarshal(obj.Body, &event)
		events = append(events, event)
	}
	return events, nil
}

func hasWorkerEventType(items []schema.Event, eventType string) bool {
	for _, item := range items {
		if item.Type == eventType {
			return true
		}
	}
	return false
}

func createWorkerJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
}

func mustWorkerOperationIntentKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustWorkerOperationControlKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type workerFakeProvider struct {
	mu            sync.Mutex
	count         int
	rolloutStatus *provider.RolloutStatus
	entered       chan struct{}
	release       chan struct{}
	once          sync.Once
}

func (p *workerFakeProvider) watchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func (p *workerFakeProvider) Name() string { return "fake" }

func (p *workerFakeProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{}, nil
}

func (p *workerFakeProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{}, nil
}

func (p *workerFakeProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-service")
}

func (p *workerFakeProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-resource")
}

func (p *workerFakeProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return nil, provider.Unsupported("fake", "logs")
}

func (p *workerFakeProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return nil, provider.Unsupported("fake", "metrics")
}

func (p *workerFakeProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return nil, provider.Unsupported("fake", "debug")
}

func (p *workerFakeProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "start-rollout")
}

func (p *workerFakeProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	p.mu.Lock()
	p.count++
	p.mu.Unlock()
	if p.entered != nil {
		p.once.Do(func() { close(p.entered) })
	}
	if p.release != nil {
		<-p.release
	}
	if p.rolloutStatus == nil {
		return &provider.RolloutStatus{Status: "rolling_out", ProviderID: req.ProviderID, UpdatedAt: workerNow()}, nil
	}
	return p.rolloutStatus, nil
}

func (p *workerFakeProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "rollback")
}

type workerFakeStep struct {
	count int
}

func (s *workerFakeStep) Kind() string { return "test.step" }

func (s *workerFakeStep) ValidateParams(ctx context.Context, params json.RawMessage) error {
	return nil
}

func (s *workerFakeStep) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "test step", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s *workerFakeStep) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	s.count++
	return &steps.StepResult{Status: steps.StatusSucceeded}, nil
}

func (s *workerFakeStep) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s *workerFakeStep) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded}, nil
}

func (s *workerFakeStep) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func workerNow() time.Time {
	return time.Date(2026, 5, 17, 2, 0, 0, 0, time.UTC)
}
