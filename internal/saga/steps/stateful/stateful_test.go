package stateful_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/saga/steps/check"
	statefulsteps "github.com/s1liconcow/skiff/internal/saga/steps/stateful"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
	stateruntime "github.com/s1liconcow/skiff/internal/stateful"
)

func TestOrderedUpdateExecutorAdvancesMembersSequentially(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{
		memberSummary(0, state.StatefulMemberReady),
		memberSummary(1, state.StatefulMemberReady),
		memberSummary(2, state.StatefulMemberReady),
	})
	createReq := orderedUpdateCreateRequest(t, []int{0, 1, 2})
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	recipe := &recordingRecipe{name: "nats-jetstream"}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: stepMap(statefulsteps.New(store, recipe)),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_update")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	client := state.NewClient(store)
	for _, member := range []int{0, 1, 2} {
		doc, err := client.GetStatefulMemberControl(ctx, "orders-stream", member)
		if err != nil {
			t.Fatalf("get member %d: %v", member, err)
		}
		if doc.Control.ReleaseID != "rel_new" || doc.Control.Generation != 2 || doc.Control.Phase != state.StatefulMemberReady || doc.Control.Lease != nil {
			t.Fatalf("member %d was not updated safely: %+v", member, doc.Control)
		}
	}
	group, err := client.GetStatefulGroupControl(ctx, "orders-stream")
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if group.Control.Lease != nil || group.Control.Operation == nil || group.Control.Operation.State != string(schema.OperationSucceeded) {
		t.Fatalf("group was not completed/released: %+v", group.Control)
	}
	wantHooks := []string{
		"0:stop", "0:start", "0:recover", "0:health", "0:detect_role",
		"1:stop", "1:start", "1:recover", "1:health", "1:detect_role",
		"2:stop", "2:start", "2:recover", "2:health", "2:detect_role",
	}
	if !reflect.DeepEqual(recipe.calls, wantHooks) {
		t.Fatalf("recipe calls = %v, want %v", recipe.calls, wantHooks)
	}
}

func TestOrderedUpdateHonorsMaxUnavailableAndReleasesGroupLease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{
		memberSummary(0, state.StatefulMemberReady),
		memberSummary(1, state.StatefulMemberUpdating),
		memberSummary(2, state.StatefulMemberReady),
	})
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, orderedUpdateCreateRequest(t, []int{0, 2})); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: stepMap(statefulsteps.New(store, nil)),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_update")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "update-member-0" {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	group, err := state.NewClient(store).GetStatefulGroupControl(ctx, "orders-stream")
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if group.Control.Lease != nil || group.Control.Operation == nil || group.Control.Operation.State != string(schema.OperationFailed) {
		t.Fatalf("failed ordered update should release group lease and mark operation failed: %+v", group.Control)
	}
}

func TestOrderedUpdateMarksMemberFailedOnRecipeFailure(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{memberSummary(0, state.StatefulMemberReady)})
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, orderedUpdateCreateRequest(t, []int{0})); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: stepMap(statefulsteps.New(store, &recordingRecipe{name: "nats-jetstream", fail: "health"})),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_update")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "update-member-0" {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	member, err := state.NewClient(store).GetStatefulMemberControl(ctx, "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.Phase != state.StatefulMemberFailed || member.Control.Lease != nil {
		t.Fatalf("member should be failed and lease released after recipe failure: %+v", member.Control)
	}
}

func TestReplaceMemberExecutorPersistsProviderProgress(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{memberSummary(0, state.StatefulMemberReady)})
	createReq := replaceMemberCreateRequest(t)
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	fake := &fakeStatefulOps{replacementID: "i-new"}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: stepMap(statefulsteps.NewWithProvider(store, fake, nil)),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_replace")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	if !reflect.DeepEqual(fake.calls, []string{"fence", "detach", "launch", "attach", "dns"}) {
		t.Fatalf("provider call order = %v", fake.calls)
	}
	client := state.NewClient(store)
	member, err := client.GetStatefulMemberControl(ctx, "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.InstanceID != "i-new" || member.Control.Generation != 2 || member.Control.Phase != state.StatefulMemberReady || member.Control.Lease != nil {
		t.Fatalf("member was not replaced safely: %+v", member.Control)
	}
	if len(member.Control.ProviderOperations) != 5 {
		t.Fatalf("provider ops = %d, want 5", len(member.Control.ProviderOperations))
	}
	group, err := client.GetStatefulGroupControl(ctx, "orders-stream")
	if err != nil {
		t.Fatalf("get group: %v", err)
	}
	if group.Control.Members[0].InstanceID != "i-new" || group.Control.Members[0].Generation != 2 {
		t.Fatalf("group summary was not updated: %+v", group.Control.Members)
	}
}

func TestReplaceMemberExecutorClassifiesProviderFailure(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{memberSummary(0, state.StatefulMemberReady)})
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, replaceMemberCreateRequest(t)); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	fake := &fakeStatefulOps{replacementID: "i-new", failAttach: true}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: stepMap(statefulsteps.NewWithProvider(store, fake, nil)),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_replace")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "replace-member" {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	inspect, err := sagas.Inspect(ctx, "saga_stateful_replace")
	if err != nil {
		t.Fatalf("inspect saga: %v", err)
	}
	if len(inspect.Control.StepResults) != 1 || inspect.Control.StepResults[0].Failure == nil || inspect.Control.StepResults[0].Failure.Code != "STATEFUL_PROVIDER_ERROR" {
		t.Fatalf("unexpected failure ref: %+v", inspect.Control.StepResults)
	}
	member, err := state.NewClient(store).GetStatefulMemberControl(ctx, "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.Replacement == nil || member.Control.Replacement.NewInstanceID != "i-new" || member.Control.Replacement.AttachedAt != "" || member.Control.InstanceID != "i-old" {
		t.Fatalf("failed replacement progress is not resumable: %+v", member.Control)
	}
}

func TestBackupExecutorPersistsSnapshotRecord(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{memberSummary(0, state.StatefulMemberReady)})
	createReq := backupCreateRequest(t)
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	fake := &fakeStatefulOps{}
	recipe := &recordingRecipe{name: "nats-jetstream"}
	result, err := (&sagastate.Executor{
		Store: sagas,
		Steps: backupStepMap(store, fake, recipe),
		Owner: "test",
	}).Execute(ctx, "saga_stateful_backup")
	if err != nil {
		t.Fatalf("execute saga: %v", err)
	}
	if result.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	record, err := stateruntime.ReadBackupRecord(ctx, store, "orders-stream", "backup_01JABC")
	if err != nil {
		t.Fatalf("read backup record: %v", err)
	}
	if record.SnapshotID != "snap-123" || record.ProviderOperation.ID != "snapshot/vol-123" || record.ProviderID != "snap-123" {
		t.Fatalf("unexpected backup record: %+v", record)
	}
	if !reflect.DeepEqual(recipe.calls, []string{"0:backup"}) {
		t.Fatalf("recipe calls = %v, want backup hook", recipe.calls)
	}
}

func TestRestoreExecutorWaitsForApprovalThenRecordsRestore(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	seedStatefulControls(t, ctx, store, []schema.StatefulMemberSummary{memberSummary(0, state.StatefulMemberReady)})
	fake := &fakeStatefulOps{}
	if _, err := (stateruntime.BackupRunner{Objects: store, State: state.NewClient(store), Provider: fake}).SnapshotMember(ctx, stateruntime.SnapshotMemberRequest{
		BackupID:    "backup_01JABC",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		OperationID: "op_backup",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	}); err != nil {
		t.Fatalf("SnapshotMember: %v", err)
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, restoreCreateRequest(t)); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	steps := stepMap(append(statefulsteps.NewWithProvider(store, fake, nil), approval.Manual{Binary: "skiff"}))
	first, err := (&sagastate.Executor{
		Store: sagas,
		Steps: steps,
		Owner: "test",
	}).Execute(ctx, "saga_stateful_restore")
	if err != nil {
		t.Fatalf("execute waiting saga: %v", err)
	}
	if first.Status != schema.SagaRunning || len(first.WaitingSteps) != 1 || first.WaitingSteps[0] != "approve-restore" {
		t.Fatalf("unexpected waiting result: %+v", first)
	}
	if _, err := approval.Approve(ctx, sagas, approval.DecisionRequest{
		SagaID: "saga_stateful_restore",
		StepID: "approve-restore",
		Actor:  schema.Actor{ID: "operator", Type: "user"},
		Reason: "restore target prepared",
	}); err != nil {
		t.Fatalf("approve restore: %v", err)
	}
	second, err := (&sagastate.Executor{
		Store: sagas,
		Steps: steps,
		Owner: "test",
	}).Execute(ctx, "saga_stateful_restore")
	if err != nil {
		t.Fatalf("execute approved saga: %v", err)
	}
	if second.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected approved result: %+v", second)
	}
	obj, err := store.Get(ctx, "stateful/orders-stream/restores/restore_01JABC/record.json")
	if err != nil {
		t.Fatalf("restore record missing: %v", err)
	}
	var record stateruntime.RestoreRecord
	if err := json.Unmarshal(obj.Body, &record); err != nil {
		t.Fatalf("decode restore record: %v", err)
	}
	if record.Status != stateruntime.RestoreStatusPlanned || record.SnapshotID != "snap-123" || record.ProviderID != "snap-123" {
		t.Fatalf("unexpected restore record: %+v", record)
	}
}

func TestRestoreVerifyBackupRejectsExpiredRecord(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	body := mustJSON(t, stateruntime.BackupRecord{
		SchemaVersion: schema.Version,
		BackupID:      "backup_expired",
		Group:         "orders-stream",
		Env:           "prod",
		Member:        0,
		VolumeID:      "vol-123",
		SnapshotID:    "snap-123",
		ProviderID:    "snap-123",
		Status:        stateruntime.BackupStatusAvailable,
		CreatedAt:     "2026-05-18T04:00:00Z",
		ExpiresAt:     "2026-05-18T05:00:00Z",
	})
	if _, err := store.Create(ctx, "stateful/orders-stream/backups/backup_expired/record.json", body, objstore.PutOptions{}); err != nil {
		t.Fatalf("create backup record: %v", err)
	}
	params := mustJSON(t, statefulsteps.RestoreParams{
		RestoreID:   "restore_expired",
		BackupID:    "backup_expired",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		OperationID: "op_restore",
	})
	result, err := (statefulsteps.RestoreVerifyBackup{Store: store, Clock: func() time.Time {
		return time.Date(2026, 5, 18, 6, 0, 0, 0, time.UTC)
	}}).Run(ctx, steps.StepRequest{
		SagaID:  "saga_restore",
		TraceID: "tr_restore",
		Intent:  schema.SagaIntent{SagaID: "saga_restore", Actor: schema.Actor{ID: "operator", Type: "user"}},
		Node:    schema.SagaNode{ID: "verify-backup", Kind: statefulsteps.KindRestoreVerifyBackup, Params: params},
	})
	if err != nil {
		t.Fatalf("RestoreVerifyBackup: %v", err)
	}
	if result.Status != steps.StatusFailed || result.Failure == nil || result.Failure.Code != "STATEFUL_BACKUP_STALE" {
		t.Fatalf("unexpected restore verify result: %+v", result)
	}
	findings, err := (statefulsteps.RestoreVerifyBackup{Store: store, Clock: func() time.Time {
		return time.Date(2026, 5, 18, 6, 0, 0, 0, time.UTC)
	}}).Doctor(ctx, steps.StepRequest{Node: schema.SagaNode{Params: params}})
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != "STATEFUL_BACKUP_STALE" {
		t.Fatalf("unexpected doctor findings: %+v", findings)
	}
}

func orderedUpdateCreateRequest(t *testing.T, members []int) sagastate.CreateRequest {
	t.Helper()
	req, err := templates.StatefulOrderedUpdate(templates.StatefulOrderedUpdateRequest{
		SagaID:             "saga_stateful_update",
		OperationID:        "op_stateful_update",
		Group:              "orders-stream",
		Env:                "prod",
		ReleaseID:          "rel_new",
		ReleaseManifestKey: "services/orders-stream/releases/rel_new/release.json",
		RuntimeManifestKey: "services/orders-stream/releases/rel_new/runtime-manifest.json",
		Members:            members,
		MaxUnavailable:     1,
		Recipe:             "nats-jetstream",
		TraceID:            "tr_stateful_update",
		Actor:              schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:          time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulOrderedUpdate: %v", err)
	}
	return req
}

func replaceMemberCreateRequest(t *testing.T) sagastate.CreateRequest {
	t.Helper()
	req, err := templates.StatefulReplaceMember(templates.StatefulReplaceMemberRequest{
		SagaID:      "saga_stateful_replace",
		OperationID: "op_stateful_replace",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		Reason:      "member failed health checks",
		TraceID:     "tr_stateful_replace",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:   time.Date(2026, 5, 18, 3, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulReplaceMember: %v", err)
	}
	return req
}

func backupCreateRequest(t *testing.T) sagastate.CreateRequest {
	t.Helper()
	req, err := templates.StatefulBackup(templates.StatefulBackupRequest{
		SagaID:      "saga_stateful_backup",
		OperationID: "op_stateful_backup",
		BackupID:    "backup_01JABC",
		Group:       "orders-stream",
		Env:         "prod",
		Members:     []int{0},
		TraceID:     "tr_stateful_backup",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:   time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulBackup: %v", err)
	}
	return req
}

func restoreCreateRequest(t *testing.T) sagastate.CreateRequest {
	t.Helper()
	req, err := templates.StatefulRestore(templates.StatefulRestoreRequest{
		SagaID:      "saga_stateful_restore",
		OperationID: "op_stateful_restore",
		RestoreID:   "restore_01JABC",
		BackupID:    "backup_01JABC",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		ApprovalID:  "approval-123",
		TraceID:     "tr_stateful_restore",
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:   time.Date(2026, 5, 18, 4, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulRestore: %v", err)
	}
	return req
}

func seedStatefulControls(t *testing.T, ctx context.Context, store *memory.Store, summaries []schema.StatefulMemberSummary) {
	t.Helper()
	client := state.NewClient(store)
	if _, err := client.CreateStatefulGroupControl(ctx, schema.StatefulGroupControl{
		Group:     "orders-stream",
		Env:       "prod",
		Replicas:  len(summaries),
		Members:   summaries,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	for _, summary := range summaries {
		if _, err := client.CreateStatefulMemberControl(ctx, schema.StatefulMemberControl{
			Group:      "orders-stream",
			Env:        "prod",
			Member:     summary.Member,
			Zone:       "us-west-2a",
			InstanceID: "i-old",
			VolumeID:   "vol-123",
			DNSName:    "orders-stream.internal",
			Generation: summary.Generation,
			Phase:      summary.Phase,
			UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
		}); err != nil {
			t.Fatalf("create member %d: %v", summary.Member, err)
		}
	}
}

func memberSummary(member int, phase string) schema.StatefulMemberSummary {
	return schema.StatefulMemberSummary{Member: member, Generation: 1, InstanceID: "i-old", VolumeID: "vol-123", DNSName: "orders-stream.internal", Phase: phase}
}

func stepMap(items []steps.Step) map[string]steps.Step {
	out := make(map[string]steps.Step, len(items))
	for _, item := range items {
		out[item.Kind()] = item
	}
	return out
}

func backupStepMap(store objstore.ObjectStore, provider provider.StatefulOperations, recipe stateruntime.Recipe) map[string]steps.Step {
	items := []steps.Step{check.Preflight{Store: store, Provider: fakeProviderIdentity{name: "fake"}}}
	items = append(items, statefulsteps.NewWithProvider(store, provider, recipe)...)
	return stepMap(items)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return body
}

type fakeProviderIdentity struct {
	name string
}

func (f fakeProviderIdentity) Name() string { return f.name }

type recordingRecipe struct {
	name  string
	fail  string
	calls []string
}

func (r *recordingRecipe) Name() string { return r.name }

func (r *recordingRecipe) Stop(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	return r.record(ctx, req, "stop")
}

func (r *recordingRecipe) Start(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	return r.record(ctx, req, "start")
}

func (r *recordingRecipe) Health(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	return r.record(ctx, req, "health")
}

func (r *recordingRecipe) Backup(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	return r.record(ctx, req, "backup")
}

func (r *recordingRecipe) Restore(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error) {
	return r.record(ctx, req, "recover")
}

func (r *recordingRecipe) DetectRole(ctx context.Context, req stateruntime.RecipeRequest) (*stateruntime.RoleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.calls = append(r.calls, callName(req, "detect_role"))
	return &stateruntime.RoleResult{Role: "primary", Primary: req.Member == 0}, nil
}

func (r *recordingRecipe) record(ctx context.Context, req stateruntime.RecipeRequest, hook string) (*stateruntime.RecipeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.calls = append(r.calls, callName(req, hook))
	if r.fail == hook {
		return nil, errors.New(hook + " failed")
	}
	return &stateruntime.RecipeResult{OK: true, Summary: hook + " ok"}, nil
}

func callName(req stateruntime.RecipeRequest, hook string) string {
	return fmt.Sprintf("%d:%s", req.Member, hook)
}

type fakeStatefulOps struct {
	replacementID string
	failAttach    bool
	calls         []string
}

func (f *fakeStatefulOps) Name() string { return "fake" }

func (f *fakeStatefulOps) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	f.calls = append(f.calls, "fence")
	return &provider.FenceInstanceResult{ProviderOperation: f.op("fence-instance", "fence/"+req.InstanceID), FencedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	f.calls = append(f.calls, "detach")
	return &provider.VolumeAttachmentResult{ProviderOperation: f.op("detach-volume", "detach/"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	f.calls = append(f.calls, "launch")
	return &provider.ReplacementInstance{ProviderOperation: f.op("launch-replacement", "launch/"+f.replacementID), InstanceID: f.replacementID, Zone: req.Zone, LaunchedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	f.calls = append(f.calls, "attach")
	if f.failAttach {
		return nil, &provider.Error{Code: provider.CodeProvider, Provider: "fake", Op: "attach_volume", Summary: "attach failed"}
	}
	return &provider.VolumeAttachmentResult{ProviderOperation: f.op("attach-volume", "attach/"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	f.calls = append(f.calls, "dns")
	return &provider.DNSUpdateResult{ProviderOperation: f.op("update-member-dns", "dns/"+req.DNSName), DNSName: req.DNSName, UpdatedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	f.calls = append(f.calls, "snapshot")
	return &provider.VolumeSnapshot{ProviderOperation: f.op("snapshot-volume", "snapshot/"+req.VolumeID), SnapshotID: "snap-123", VolumeID: req.VolumeID, CreatedAt: time.Now().UTC()}, ctx.Err()
}

func (f *fakeStatefulOps) op(kind, id string) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{Provider: "fake", Kind: kind, ID: id}
}
