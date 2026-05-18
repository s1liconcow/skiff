package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCanarySagaHumanOutputIncludesGeneratedIdentifiers(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeCanarySagaResult("skiff", "deploy --canary", "human", "tr_canary", canarySagaResult{
		SagaID:      "saga_generated",
		OperationID: "op_generated",
		ReleaseID:   "rel_generated",
		Status:      schema.SagaSucceeded,
		Stage:       100,
		Gate:        "request_count",
		NextAction:  "complete",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"canary saga saga_generated status=succeeded",
		"release: rel_generated",
		"operation: op_generated",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSagaInspectJSONReadsDirectObjectState(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	sagas := sagastate.NewStore(store, sagastate.WithClock(func() time.Time {
		return time.Date(2026, 5, 16, 23, 55, 0, 0, time.UTC)
	}))
	if _, err := sagas.Create(context.Background(), sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Kind:          "rollback",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_saga",
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
			Summary:       "rollback payments-api",
			CreatedAt:     "2026-05-16T23:55:00Z",
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Nodes: []schema.SagaNode{{
				ID:            "point-release",
				Kind:          "service.point_release",
				Risk:          schema.RiskMedium,
				Reversibility: schema.Reversible,
			}},
			CreatedAt: "2026-05-16T23:55:00Z",
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Status:        schema.SagaRunning,
			CurrentSteps:  []string{"point-release"},
			UpdatedAt:     "2026-05-16T23:55:00Z",
			TraceID:       "tr_saga",
		},
	}); err != nil {
		t.Fatalf("create saga: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "inspect", "saga_01JABC",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_cli_saga",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got sagaInspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga inspect output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cli_saga" || got.Result.SagaID != "saga_01JABC" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.Status != schema.SagaRunning || got.Result.Risk != schema.RiskMedium || got.Result.Reversibility != schema.Reversible {
		t.Fatalf("unexpected saga summary: %+v", got.Result)
	}
	if len(got.Result.CurrentSteps) != 1 || got.Result.CurrentSteps[0] != "point-release" {
		t.Fatalf("current steps missing: %+v", got.Result.CurrentSteps)
	}
}

func TestSagaSkeletonCommandsReturnJSON(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "cancel", "saga_01JABC",
		"--format", "json",
		"--trace-id", "tr_saga_skeleton",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got sagaCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga skeleton output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_saga_skeleton" || got.Command != "cancel" || got.Saga != "saga_01JABC" || got.Implemented {
		t.Fatalf("unexpected skeleton output: %+v", got)
	}
}

func TestSagaStartStatefulOrderedUpdateDirectJSON(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	client := state.NewClient(store)
	if _, err := client.CreateStatefulGroupControl(context.Background(), schema.StatefulGroupControl{
		Group: "orders-stream",
		Env:   "prod",
		Members: []schema.StatefulMemberSummary{
			{Member: 0, Generation: 1, InstanceID: "i-0", VolumeID: "vol-0", Phase: state.StatefulMemberReady},
			{Member: 1, Generation: 1, InstanceID: "i-1", VolumeID: "vol-1", Phase: state.StatefulMemberReady},
		},
		Replicas:  2,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := client.CreateStatefulMemberControl(context.Background(), schema.StatefulMemberControl{
			Group:      "orders-stream",
			Env:        "prod",
			Member:     i,
			InstanceID: "i-" + strconv.Itoa(i),
			VolumeID:   "vol-" + strconv.Itoa(i),
			Generation: 1,
			Phase:      state.StatefulMemberReady,
			UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
		}); err != nil {
			t.Fatalf("create member %d: %v", i, err)
		}
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "start", "stateful.ordered_update",
		"--group", "orders-stream",
		"--release-id", "rel_new",
		"--members", "0,1",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "fake",
		"--region", "local",
		"--format", "json",
		"--trace-id", "tr_stateful_ordered_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got statefulOrderedSagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stateful ordered output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_stateful_ordered_cli" || got.Result.Status != schema.SagaSucceeded || got.Result.Group != "orders-stream" || got.Result.ReleaseID != "rel_new" {
		t.Fatalf("unexpected stateful ordered output: %+v", got)
	}
	member, err := client.GetStatefulMemberControl(context.Background(), "orders-stream", 1)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.ReleaseID != "rel_new" || member.Control.Generation != 2 {
		t.Fatalf("member was not updated by saga start: %+v", member.Control)
	}
}

func TestSagaWatchDirectModeStreamsSagaEvents(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	body, err := canonical.Marshal(schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JSAGA",
		Time:          "2026-05-16T20:03:00Z",
		TraceID:       "tr_saga_event",
		Subject:       schema.Target{Kind: "saga", Name: "saga_01JABC"},
		Type:          "approval.required",
		Summary:       "approval required",
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if _, err := store.Create(context.Background(), "sagas/saga_01JABC/events/01JSAGA.json", body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	oldContext := eventsWatchContext
	oldInterval := eventsWatchPollInterval
	eventsWatchContext = func() context.Context { return ctx }
	eventsWatchPollInterval = time.Hour
	t.Cleanup(func() {
		eventsWatchContext = oldContext
		eventsWatchPollInterval = oldInterval
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "watch", "saga_01JABC",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_saga_watch",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got eventWatchOutput
	if err := json.Unmarshal(bytes.Split(stdout.Bytes(), []byte("\n"))[0], &got); err != nil {
		t.Fatalf("saga watch output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_saga_watch" || got.Event == nil || got.Event.ID != "01JSAGA" {
		t.Fatalf("unexpected saga watch output: %+v", got)
	}
}

func TestSagaApproveJSONMutatesWaitingApproval(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	sagas := sagastate.NewStore(store, sagastate.WithClock(func() time.Time {
		return time.Date(2026, 5, 17, 1, 15, 0, 0, time.UTC)
	}))
	if _, err := sagas.Create(context.Background(), sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_approval_cli",
			Kind:          "test.approval",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_approval_cli",
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
			Summary:       "approval cli",
			CreatedAt:     "2026-05-17T01:15:00Z",
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_approval_cli",
			Nodes: []schema.SagaNode{{
				ID:            "approval-before-cutover",
				Kind:          approval.KindManual,
				Risk:          schema.RiskHigh,
				Reversibility: schema.Reversible,
			}},
			CreatedAt: "2026-05-17T01:15:00Z",
		},
		Control: schema.SagaControl{
			SchemaVersion: "skiff.state/v1",
			SagaID:        "saga_approval_cli",
			Status:        schema.SagaRunning,
			CurrentSteps:  []string{"approval-before-cutover"},
			StepResults: []schema.StepResultRef{{
				StepID: "approval-before-cutover",
				Kind:   approval.KindManual,
				Status: "waiting",
				Result: json.RawMessage(`{"state":"waiting_for_approval","step":"approval-before-cutover","risk":"high","facts":["shadow service healthy"],"approve_command":"skiff saga approve saga_approval_cli --step approval-before-cutover --format json","reject_command":"skiff saga reject saga_approval_cli --step approval-before-cutover --format json"}`),
			}},
			UpdatedAt: "2026-05-17T01:15:00Z",
			TraceID:   "tr_approval_cli",
		},
	}); err != nil {
		t.Fatalf("create saga: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "approve", "saga_approval_cli",
		"--step", "approval-before-cutover",
		"--reason", "shadow service healthy",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_cli_approval",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got sagaApprovalOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga approve output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Result.Decision != approval.DecisionApprove || got.Result.Control.StepResults[0].Status != "succeeded" {
		t.Fatalf("unexpected approval output: %+v", got)
	}
}

func TestSagaStartCanaryDeployJSONCreatesAndRuns(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(time.Date(2026, 5, 17, 3, 20, 0, 0, time.UTC)), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = "rel_old"
	control.StableRelease = "rel_old"
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatalf("create service control: %v", err)
	}
	fake := &fakeCanaryCLIProvider{}
	oldProvider := newSagaProvider
	newSagaProvider = func(config.Config, objstore.ObjectStore) (provider.Provider, error) {
		return fake, nil
	}
	defer func() { newSagaProvider = oldProvider }()

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"saga", "start", "canary-deploy",
		"--service", "payments-api",
		"--release-id", "rel_new",
		"--stages", "100",
		"--bake", "0s",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_canary_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got canarySagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("saga start output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_canary_cli" || got.Result.Status != schema.SagaSucceeded || got.Result.Stage != 100 || got.Result.NextAction != "complete" {
		t.Fatalf("unexpected canary output: %+v", got)
	}
	if len(fake.rollouts) != 1 || fake.rollouts[0].ReleaseID != "rel_new" {
		t.Fatalf("rollout not started for canary release: %+v", fake.rollouts)
	}
	updated, err := state.NewClient(store).GetServiceControl(context.Background(), "payments-api")
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	if updated.Control.DesiredRelease != "rel_new" || updated.Control.StableRelease != "rel_new" {
		t.Fatalf("canary did not mark release stable: %+v", updated.Control)
	}
}

type fakeCanaryCLIProvider struct {
	rollouts []provider.RolloutRequest
}

func (p *fakeCanaryCLIProvider) Name() string { return "aws" }

func (p *fakeCanaryCLIProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{Provider: "aws", Service: graph.Service, Env: graph.Env}, nil
}

func (p *fakeCanaryCLIProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{Provider: "aws", Service: plan.Service, Env: plan.Env, AppliedAt: time.Now().UTC()}, nil
}

func (p *fakeCanaryCLIProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return &provider.ServiceInspection{Ref: ref, Provider: "aws", FreshAt: time.Now().UTC()}, nil
}

func (p *fakeCanaryCLIProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return &provider.ResourceInspection{Kind: ref.Kind, LogicalID: ref.LogicalID, ProviderID: "tg-123", Status: "healthy"}, nil
}

func (p *fakeCanaryCLIProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return &provider.LogsResult{}, nil
}

func (p *fakeCanaryCLIProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	name := "aws.elb.http_5xx_count"
	if len(req.Names) > 0 {
		name = req.Names[0]
	}
	return &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name:   name,
		Source: "fake",
		Points: []provider.MetricPoint{{Timestamp: time.Now().UTC(), Value: 0}},
	}}}, nil
}

func (p *fakeCanaryCLIProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return &provider.DebugSession{ID: "debug-1", Provider: "aws", StartedAt: time.Now().UTC()}, nil
}

func (p *fakeCanaryCLIProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	p.rollouts = append(p.rollouts, req)
	return &provider.Rollout{ID: req.OperationID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "ir-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}

func (p *fakeCanaryCLIProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return &provider.RolloutStatus{RolloutID: req.RolloutID, Status: "succeeded", ProviderID: req.ProviderID, UpdatedAt: time.Now().UTC()}, nil
}

func (p *fakeCanaryCLIProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: "rollback-" + req.ReleaseID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "rb-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}
