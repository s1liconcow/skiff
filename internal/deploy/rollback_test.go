package deploy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRollbackPreviousStableUpdatesDesiredStartsRolloutAndAudits(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedRollbackService(t, store, "rel_bad", "rel_good")
	seedReleaseManifest(t, store, "payments-api", "prod", "rel_good")
	rollouts := &fakeASGRolloutClient{
		start:    &aws.InstanceRefresh{ID: "ir-rollback", Status: "Pending", StartedAt: rolloutTestNow()},
		describe: &aws.InstanceRefresh{ID: "ir-rollback", Status: "Successful", UpdatedAt: rolloutTestNow()},
	}

	result, err := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, rollouts), Clock: rolloutTestNow}.Rollback(ctx, deploy.RollbackRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_rollback",
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollback",
		SagaID:      "saga_rollback",
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !result.OK || result.FromRelease != "rel_bad" || result.ToRelease != "rel_good" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Rollout == nil || result.Rollout.ProviderID != "ir-rollback" || result.RolloutStatus == nil || result.RolloutStatus.Status != "succeeded" {
		t.Fatalf("unexpected rollout result: rollout=%+v status=%+v", result.Rollout, result.RolloutStatus)
	}
	if len(result.ReleaseHistory) != 1 || result.ReleaseHistory[0] != "rel_good" {
		t.Fatalf("release history = %+v", result.ReleaseHistory)
	}

	control, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.DesiredRelease != "rel_good" || control.Control.StableRelease != "rel_good" || control.Control.Lease != nil {
		t.Fatalf("service control not completed/released: %+v", control.Control)
	}
	if control.Control.Operation == nil || control.Control.Operation.Kind != "rollback" || control.Control.Operation.State != string(schema.OperationSucceeded) {
		t.Fatalf("service operation = %+v", control.Control.Operation)
	}
	intentKey, err := paths.OperationIntent("payments-api", "op_rollback")
	if err != nil {
		t.Fatal(err)
	}
	intent := readObject[schema.OperationIntent](t, store, intentKey)
	if intent.Kind != "rollback" || intent.Risk != schema.RiskMedium || intent.Reversibility != schema.Reversible {
		t.Fatalf("unexpected operation intent: %+v", intent)
	}
	opControl := readOperationControl(t, store, "payments-api", "op_rollback")
	if opControl.Status != schema.OperationSucceeded {
		t.Fatalf("operation status = %s, want succeeded", opControl.Status)
	}
	if len(opControl.ProviderOperations) != 1 || opControl.ProviderOperations[0].ID != "ir-rollback" {
		t.Fatalf("provider operation not persisted: %+v", opControl.ProviderOperations)
	}
	inspect, err := sagastate.NewStore(store).Inspect(ctx, "saga_rollback")
	if err != nil {
		t.Fatalf("inspect saga: %v", err)
	}
	if inspect.Kind != templates.DeploymentRollbackKind || inspect.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected saga state: %+v", inspect)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: rolloutTestNow})
	if err != nil {
		t.Fatal(err)
	}
	operationEvents, err := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: "payments-api", Operation: "op_rollback"}, events.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(operationEvents) < 4 {
		t.Fatalf("operation events = %d, want at least 4", len(operationEvents))
	}
	auditObjects, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(auditObjects) != 1 {
		t.Fatalf("audit object count = %d, want 1", len(auditObjects))
	}
}

func TestRollbackRequiresStableReleaseForPreviousStable(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedRollbackService(t, store, "rel_bad", "")

	result, err := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, &fakeASGRolloutClient{}), Clock: rolloutTestNow}.Rollback(ctx, deploy.RollbackRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_rollback_missing_stable",
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_missing_stable",
		SagaID:      "saga_missing_stable",
	})
	if err == nil || result == nil || !strings.Contains(err.Error(), "stable_release") {
		t.Fatalf("result=%+v err=%v, want stable_release error", result, err)
	}
	intentKey, err := paths.OperationIntent("payments-api", "op_missing_stable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, intentKey); err == nil {
		t.Fatalf("operation intent was created before resolving stable release")
	}
}

func TestRollbackRequiresTargetReleaseObject(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedRollbackService(t, store, "rel_bad", "rel_good")

	result, err := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, &fakeASGRolloutClient{}), Clock: rolloutTestNow}.Rollback(ctx, deploy.RollbackRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_rollback_missing_release",
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_missing_release",
		SagaID:      "saga_missing_release",
	})
	if err == nil || result == nil || !strings.Contains(err.Error(), "immutable release history") {
		t.Fatalf("result=%+v err=%v, want missing release history error", result, err)
	}
	intentKey, err := paths.OperationIntent("payments-api", "op_missing_release")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, intentKey); err == nil {
		t.Fatalf("operation intent was created before verifying target release")
	}
}

func TestRollbackLeaseHeldDoesNotUpdateDesiredRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedRollbackService(t, store, "rel_bad", "rel_good")
	seedReleaseManifest(t, store, "payments-api", "prod", "rel_good")
	client := state.NewClient(store, state.WithClock(testClock{now: rolloutTestNow()}))
	if _, _, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "other-agent", Duration: time.Minute, Actor: schema.Actor{ID: "other-agent", Type: "agent"}}); err != nil {
		t.Fatal(err)
	}

	result, err := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, &fakeASGRolloutClient{}), Clock: rolloutTestNow}.Rollback(ctx, deploy.RollbackRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_rollback_lease",
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollback_lease",
		SagaID:      "saga_rollback_lease",
	})
	if err == nil || result == nil || result.OK || !strings.Contains(err.Error(), "LEASE_HELD") {
		t.Fatalf("result=%+v err=%v, want lease held", result, err)
	}
	control, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.DesiredRelease != "rel_bad" {
		t.Fatalf("desired release changed despite held lease: %+v", control.Control)
	}
}

func TestRollbackFailedRolloutMarksOperationFailed(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedRollbackService(t, store, "rel_bad", "rel_good")
	seedReleaseManifest(t, store, "payments-api", "prod", "rel_good")
	rollouts := &fakeASGRolloutClient{
		start:    &aws.InstanceRefresh{ID: "ir-failed", Status: "Pending", StartedAt: rolloutTestNow()},
		describe: &aws.InstanceRefresh{ID: "ir-failed", Status: "Failed", UpdatedAt: rolloutTestNow()},
	}

	result, err := deploy.Deployer{Store: store, Provider: newRolloutAWSProvider(t, rollouts), Clock: rolloutTestNow}.Rollback(ctx, deploy.RollbackRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_rollback_failed",
		Service:     "payments-api",
		Env:         "prod",
		OperationID: "op_rollback_failed",
		SagaID:      "saga_rollback_failed",
	})
	if err == nil || result == nil || result.OK || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("result=%+v err=%v, want failed rollout", result, err)
	}
	control, err := state.NewClient(store).GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.DesiredRelease != "rel_good" || control.Control.Operation == nil || control.Control.Operation.State != string(schema.OperationFailed) || control.Control.Lease != nil {
		t.Fatalf("service control not marked failed/released: %+v", control.Control)
	}
	opControl := readOperationControl(t, store, "payments-api", "op_rollback_failed")
	if opControl.Status != schema.OperationFailed {
		t.Fatalf("operation status = %s, want failed", opControl.Status)
	}
	inspect, err := sagastate.NewStore(store).Inspect(ctx, "saga_rollback_failed")
	if err != nil {
		t.Fatalf("inspect saga: %v", err)
	}
	if inspect.Status != schema.SagaFailed {
		t.Fatalf("saga status = %s, want failed", inspect.Status)
	}
}

func seedRollbackService(t *testing.T, store *memory.Store, desired, stable string) {
	t.Helper()
	control := schema.NewServiceControl("payments-api", "prod", canonical.Time(rolloutTestNow()), schema.Actor{ID: "seed", Type: "agent"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	if _, err := state.NewClient(store, state.WithClock(testClock{now: rolloutTestNow()})).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatal(err)
	}
}

func seedReleaseManifest(t *testing.T, store *memory.Store, service, env, releaseID string) {
	t.Helper()
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            service,
		Env:                env,
		ReleaseID:          releaseID,
		RuntimeManifestKey: runtimeKey,
		CreatedAt:          canonical.Time(rolloutTestNow()),
	}
	body, err := canonical.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}
