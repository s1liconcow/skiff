package deploy_test

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStartRolloutStoresProviderIDBeforeWatch(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createOperationControl(t, store, "payments-api", "prod", "op_rollout")
	rollouts := &fakeASGRolloutClient{start: &aws.InstanceRefresh{ID: "ir-123", Status: "Pending", StartedAt: rolloutTestNow()}}
	deployer := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, rollouts), Clock: rolloutTestNow}

	rollout, err := deployer.StartRollout(ctx, deploy.StartRolloutRequest{
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollout",
		ReleaseID:   "rel_01J",
		TraceID:     "tr_rollout",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
	})
	if err != nil {
		t.Fatalf("start rollout: %v", err)
	}
	if rollout.ProviderID != "ir-123" {
		t.Fatalf("provider id = %q", rollout.ProviderID)
	}
	control := readOperationControl(t, store, "payments-api", "op_rollout")
	if len(control.ProviderOperations) != 1 || control.ProviderOperations[0].ID != "ir-123" || control.ProviderOperations[0].Kind != aws.RolloutKindASGInstanceRefresh {
		t.Fatalf("provider rollout was not stored: %+v", control.ProviderOperations)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: rolloutTestNow})
	if err != nil {
		t.Fatal(err)
	}
	items, err := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: "payments-api", Operation: "op_rollout"}, events.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Type != "rollout.started" {
		t.Fatalf("unexpected rollout events: %+v", items)
	}
}

func TestStartRolloutDefaultsToDesiredRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createOperationControl(t, store, "payments-api", "prod", "op_rollout")
	createRolloutServiceControl(t, store, "payments-api", "prod", "rel_desired", "rel_stable", "op_rollout")
	rollouts := &fakeASGRolloutClient{start: &aws.InstanceRefresh{ID: "ir-123", Status: "Pending", StartedAt: rolloutTestNow()}}
	deployer := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, rollouts), Clock: rolloutTestNow}

	_, err := deployer.StartRollout(ctx, deploy.StartRolloutRequest{
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollout",
		TraceID:     "tr_rollout",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
	})
	if err != nil {
		t.Fatalf("start rollout: %v", err)
	}
	if rollouts.startReq.ReleaseID != "rel_desired" {
		t.Fatalf("start rollout release = %q, want desired release", rollouts.startReq.ReleaseID)
	}
	control := readOperationControl(t, store, "payments-api", "op_rollout")
	if control.Status != schema.OperationRunning {
		t.Fatalf("operation status = %s, want running", control.Status)
	}
}

func TestWatchRolloutResumesStoredProviderIDAndCompletesOperation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createOperationControl(t, store, "payments-api", "prod", "op_rollout")
	createRolloutServiceControl(t, store, "payments-api", "prod", "rel_new", "rel_old", "op_rollout")
	control := readOperationControl(t, store, "payments-api", "op_rollout")
	control.ProviderOperations = []schema.ProviderOperationRef{{
		Provider: aws.Name,
		Kind:     aws.RolloutKindASGInstanceRefresh,
		ID:       "ir-123",
	}}
	writeOperationControlCAS(t, store, control)
	rollouts := &fakeASGRolloutClient{describe: &aws.InstanceRefresh{ID: "ir-123", Status: "Successful", UpdatedAt: rolloutTestNow()}}
	deployer := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, rollouts), Clock: rolloutTestNow}

	status, err := deployer.WatchRollout(ctx, deploy.WatchRolloutRequest{
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollout",
		TraceID:     "tr_rollout",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
	})
	if err != nil {
		t.Fatalf("watch rollout: %v", err)
	}
	if status.Status != "succeeded" || rollouts.describeReq.InstanceRefreshID != "ir-123" {
		t.Fatalf("unexpected watch status/request: status=%+v req=%+v", status, rollouts.describeReq)
	}
	control = readOperationControl(t, store, "payments-api", "op_rollout")
	if control.Status != schema.OperationSucceeded {
		t.Fatalf("operation status = %s, want succeeded", control.Status)
	}
	service := readServiceControl(t, store, "payments-api")
	if service.StableRelease != "rel_new" || service.DesiredRelease != "rel_new" {
		t.Fatalf("service control was not marked stable after rollout: %+v", service)
	}
}

func createOperationControl(t *testing.T, store *memory.Store, service, env, operationID string) {
	t.Helper()
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   operationID,
		Service:       service,
		Env:           env,
		Status:        schema.OperationRunning,
		UpdatedAt:     canonical.Time(rolloutTestNow()),
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func createRolloutServiceControl(t *testing.T, store *memory.Store, service, env, desired, stable, operationID string) {
	t.Helper()
	control := schema.NewServiceControl(service, env, canonical.Time(rolloutTestNow()), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	control.Operation = &schema.ActiveOperation{ID: operationID, Kind: "deploy", State: string(schema.OperationRunning)}
	body, err := canonical.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func readServiceControl(t *testing.T, store *memory.Store, service string) schema.ServiceControl {
	t.Helper()
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var control schema.ServiceControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		t.Fatal(err)
	}
	return control
}

func readOperationControl(t *testing.T, store *memory.Store, service, operationID string) schema.OperationControl {
	t.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		t.Fatal(err)
	}
	return control
}

func writeOperationControlCAS(t *testing.T, store *memory.Store, control schema.OperationControl) {
	t.Helper()
	key, err := paths.OperationControl(control.Service, control.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(context.Background(), key, obj.ETag, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func newRolloutAWSProvider(t *testing.T, rollouts *fakeASGRolloutClient) *aws.Provider {
	t.Helper()
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{Rollouts: rollouts}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeASGRolloutClient struct {
	start       *aws.InstanceRefresh
	startReq    aws.StartInstanceRefreshRequest
	describe    *aws.InstanceRefresh
	describeReq aws.DescribeInstanceRefreshRequest
}

func (c *fakeASGRolloutClient) StartInstanceRefresh(ctx context.Context, req aws.StartInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.startReq = req
	return c.start, nil
}

func (c *fakeASGRolloutClient) DescribeInstanceRefresh(ctx context.Context, req aws.DescribeInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.describeReq = req
	return c.describe, nil
}

func rolloutTestNow() time.Time {
	return time.Date(2026, 5, 16, 23, 5, 0, 0, time.UTC)
}
