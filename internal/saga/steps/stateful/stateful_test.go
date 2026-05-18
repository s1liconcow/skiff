package stateful_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
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
